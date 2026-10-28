/**
 * One-video download pipeline: Actor Run, media download, sidecar files, manifest.
 *
 * Shared by `download.mjs` and `batch-download.mjs` so a single URL and a CSV row produce the same
 * folder layout and the same status vocabulary as the Python, Go, and Java clients.
 */
import path from 'node:path';
import { promises as fs } from 'node:fs';
import {
  CreationUnknownError,
  ConfigurationError,
  ProtocolError,
  TransportError,
  getTranscript,
} from './client.mjs';
import {
  buildManifest,
  chooseOutputDir,
  folderName,
  sanitizeItem,
  transcriptSegments,
  writeJson,
  writeText,
} from './layout.mjs';
import { MediaError, audioTarget, downloadMedia, redactMediaUrl, transcodeAudio } from './media.mjs';

export const DOWNLOAD_FAILED_EXIT = 11;
/** Runs that produced usable media; a missing transcript does not block the download. */
export const DOWNLOADABLE_STATUSES = new Set(['success', 'no_transcript']);
const TRANSCRIPT_EXIT_CODES = { actor_error: 4, empty: 5, no_transcript: 6, unknown: 7, run_failed: 9 };

export function emptyReport(sourceUrl) {
  return {
    status: 'unknown',
    sourceUrl,
    canonicalUrl: null,
    urlForm: null,
    runId: null,
    datasetId: null,
    outputDir: null,
    duration: null,
    title: null,
    nickname: null,
    segments: 0,
    assets: [],
    transcript: null,
    error: null,
  };
}

/**
 * Resolve one URL, download the requested media, and write the sidecar files.
 * Returns `{ report, exitCode }`.
 */
export async function downloadOne(url, options = {}) {
  const {
    outDir = 'downloads',
    wantVideo = true,
    wantAudio = false,
    audioFormat = 'raw',
    force = false,
    keepMediaUrls = false,
    name = null,
    build = null,
    actorId = null,
    timeout = null,
    maxTotalChargeUsd = null,
    requireChargeCap = null,
    datasetLimit = 100,
    resume = null,
    rowId = null,
    token = null,
    transport = null,
    journal = null,
    progress = null,
    fetchImpl = globalThis.fetch,
  } = options;

  const report = emptyReport(url);
  let result;
  try {
    result = await getTranscript(url, {
      token, actorId, build, timeout, maxTotalChargeUsd, requireChargeCap,
      datasetLimit, runId: resume, rowId, transport, journal,
    });
  } catch (error) {
    if (error instanceof ConfigurationError) {
      return { report: { ...report, status: 'config_error', error: error.message }, exitCode: 2 };
    }
    if (error instanceof CreationUnknownError) {
      return { report: { ...report, status: 'creation_unknown', error: error.message }, exitCode: 10 };
    }
    if (error instanceof ProtocolError) {
      return {
        report: { ...report, status: 'unknown', runId: error.runId ?? null, datasetId: error.datasetId ?? null, error: error.message },
        exitCode: 7,
      };
    }
    if (error instanceof TransportError) {
      return {
        report: { ...report, status: 'transport_error', runId: error.runId ?? null, datasetId: error.datasetId ?? null, error: error.message },
        exitCode: 8,
      };
    }
    throw error;
  }

  Object.assign(report, {
    status: result.status,
    canonicalUrl: result.canonicalUrl,
    urlForm: result.urlForm,
    runId: result.runId ?? null,
    datasetId: result.datasetId ?? null,
    duration: result.duration ?? null,
    title: result.title ?? null,
    nickname: result.nickname ?? null,
    error: result.error ?? null,
  });

  const item = result.items?.[0] ?? {};
  const folder = name ?? folderName(result.canonicalUrl, result.urlForm, result.title);
  const target = await chooseOutputDir(outDir, folder, result.canonicalUrl);
  report.outputDir = target;

  if (!DOWNLOADABLE_STATUSES.has(result.status)) {
    await writeJson(path.join(target, 'metadata.json'), sanitizeItem(item, { keepMediaUrls }));
    return { report, exitCode: TRANSCRIPT_EXIT_CODES[result.status] ?? 7 };
  }

  const segments = transcriptSegments(result.segments);
  const transcriptText = (result.text ?? '').trim();
  const transcriptBytes = await writeText(
    path.join(target, 'transcript.txt'),
    transcriptText ? `${transcriptText}\n` : '',
  );
  await writeJson(path.join(target, 'segments.json'), segments);
  await writeJson(path.join(target, 'metadata.json'), sanitizeItem(item, { keepMediaUrls }));
  report.segments = segments.length;
  report.transcript = { bytes: transcriptBytes, segments: segments.length };

  const jobs = [];
  if (wantVideo && typeof item.videoUrl === 'string' && item.videoUrl) {
    jobs.push({ kind: 'video', url: item.videoUrl, destination: path.join(target, 'video.mp4'), converted: null });
  }
  if (wantAudio && typeof item.audioUrl === 'string' && item.audioUrl) {
    const rawPath = path.join(target, 'audio.mp4');
    const { target: finalPath, needsRemux } = audioTarget(rawPath, audioFormat);
    jobs.push({ kind: 'audio', url: item.audioUrl, destination: rawPath, converted: needsRemux ? finalPath : null });
  }

  const assets = [];
  let failure = null;
  if (jobs.length === 0) {
    failure = new MediaError(`the Dataset item carries no media URL for the requested output (${result.status})`);
    report.error = failure.message;
  }

  for (const job of jobs) {
    try {
      const asset = await downloadMedia(job.url, job.destination, { kind: job.kind, force, progress, fetchImpl });
      assets.push(asset);
      if (job.converted) {
        const exists = await fs.access(job.converted).then(() => true, () => false);
        // `--force` re-derives the converted container too; otherwise a re-downloaded audio.mp4
        // would sit next to a stale audio.m4a.
        const rebuilt = force || !exists;
        if (rebuilt) await transcodeAudio(job.destination, job.converted, audioFormat);
        const stat = await fs.stat(job.converted);
        assets.push({
          kind: 'audio-converted', path: job.converted, bytes: stat.size, sha256: '',
          contentType: null, sourceHost: '', skipped: !rebuilt,
        });
      }
    } catch (error) {
      if (!(error instanceof MediaError)) throw error;
      failure = error;
      report.error = `${job.kind} download failed: ${error.message}`;
      break;
    }
  }

  const manifest = buildManifest({
    sourceUrl: result.sourceUrl,
    canonicalUrl: result.canonicalUrl,
    urlForm: result.urlForm,
    result,
    assets,
    transcriptBytes,
    segmentCount: segments.length,
  });
  manifest.mediaSources = jobs.map((job) => ({ kind: job.kind, url: redactMediaUrl(job.url) }));
  await writeJson(path.join(target, 'manifest.json'), manifest);
  report.assets = assets;

  return { report, exitCode: failure ? DOWNLOAD_FAILED_EXIT : 0 };
}
