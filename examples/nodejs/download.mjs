#!/usr/bin/env node
/**
 * Download the video, audio, transcript, and metadata for one public Facebook video URL.
 *
 * One invocation may create one billable Actor Run ($0.0055 for a video under one minute).
 * The Run resolves the page into signed videoUrl/audioUrl links; those are downloaded at once
 * because the signature expires.
 *
 * Exit codes: 0 downloaded, 2 bad configuration, 4 Actor item error, 5 empty Dataset, 7 unknown,
 * 8 transport failure, 9 Run failed, 10 creation unknown, 11 media download failed.
 */
import { parseArgs } from 'node:util';
import { readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { APPROVED_BUILD_NUMBER } from './client.mjs';
import { downloadOne } from './pipeline.mjs';

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const ENV_SEARCH_DEPTH = 4;

/**
 * Nearest `.env` at or above `start`. The Python, Go, and Java clients walk up from the working
 * directory too, so all four agree on which file backs a run.
 */
export function findEnvFile(start = process.cwd()) {
  let directory = path.resolve(start);
  for (let depth = 0; depth <= ENV_SEARCH_DEPTH; depth += 1) {
    const candidate = path.join(directory, '.env');
    try {
      if (statSync(candidate).isFile()) return candidate;
    } catch {
      // keep walking
    }
    const parent = path.dirname(directory);
    if (parent === directory) return null;
    directory = parent;
  }
  return null;
}

export function loadEnvFile(file = findEnvFile()) {
  if (['1', 'true', 'yes'].includes((process.env.APIFY_SKIP_DOTENV ?? '').trim().toLowerCase())) return;
  if (!file) return;
  let text;
  try {
    text = readFileSync(file, 'utf8');
  } catch {
    return;
  }
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith('#') || !line.includes('=')) continue;
    const index = line.indexOf('=');
    const key = line.slice(0, index).trim();
    const value = line.slice(index + 1).trim().replace(/^(['"])(.*)\1$/, '$2');
    if (key.startsWith('APIFY_') && process.env[key] === undefined) process.env[key] = value;
  }
}

export function humanBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return unit === 0 ? `${size} B` : `${size.toFixed(1)} ${units[unit]}`;
}

function makeProgress(quiet) {
  const state = new Map();
  const step = 5 * 1024 * 1024;
  return (kind, written, total) => {
    if (quiet) return;
    const last = state.get(kind) ?? 0;
    if (written - last < step && !(total && written >= total)) return;
    state.set(kind, written);
    const share = total ? ` (${Math.floor((written * 100) / total)}%)` : '';
    process.stderr.write(`  ${kind}: ${humanBytes(written)}${share}\r`);
  };
}

export async function main(argv = process.argv.slice(2)) {
  const { values, positionals } = parseArgs({
    args: argv,
    allowPositionals: true,
    options: {
      out: { type: 'string', default: 'downloads' },
      name: { type: 'string' },
      'no-video': { type: 'boolean', default: false },
      audio: { type: 'boolean', default: false },
      'audio-format': { type: 'string', default: 'raw' },
      force: { type: 'boolean', default: false },
      'keep-media-urls': { type: 'boolean', default: false },
      json: { type: 'boolean', default: false },
      report: { type: 'string' },
      resume: { type: 'string' },
      'dataset-limit': { type: 'string', default: '100' },
      timeout: { type: 'string', default: '900' },
      build: { type: 'string', default: APPROVED_BUILD_NUMBER },
      'actor-id': { type: 'string' },
      'max-total-charge-usd': { type: 'string' },
      'require-charge-cap': { type: 'boolean', default: false },
      quiet: { type: 'boolean', default: false },
      help: { type: 'boolean', default: false },
    },
  });

  if (values.help || positionals.length !== 1) {
    process.stdout.write(
      'Usage: node examples/nodejs/download.mjs <facebook-video-url> [options]\n'
      + '  --out DIR              output base directory (default: downloads)\n'
      + '  --audio                also download the audio track\n'
      + '  --audio-format F       raw | m4a | mp3 (default: raw)\n'
      + '  --no-video             skip the video file\n'
      + '  --force                re-download existing files\n'
      + '  --json                 print one JSON report\n'
      + '  --resume RUN_ID        reuse an existing Run (GET only)\n'
      + '  --keep-media-urls      keep signed URLs in metadata.json\n',
    );
    return values.help ? 0 : 2;
  }

  loadEnvFile();

  if (values['audio-format'] !== 'raw' && !values.audio) {
    process.stderr.write('--audio-format requires --audio\n');
    return 2;
  }
  if (!['raw', 'm4a', 'mp3'].includes(values['audio-format'])) {
    process.stderr.write('--audio-format must be raw, m4a, or mp3\n');
    return 2;
  }

  const { report, exitCode } = await downloadOne(positionals[0], {
    outDir: values.out,
    wantVideo: !values['no-video'],
    wantAudio: values.audio,
    audioFormat: values['audio-format'],
    force: values.force,
    keepMediaUrls: values['keep-media-urls'],
    name: values.name ?? null,
    build: values.build,
    actorId: values['actor-id'] ?? null,
    timeout: Number(values.timeout),
    maxTotalChargeUsd: values['max-total-charge-usd'] ?? null,
    requireChargeCap: values['require-charge-cap'],
    datasetLimit: Number(values['dataset-limit']),
    resume: values.resume ?? null,
    progress: makeProgress(values.quiet),
  });

  if (values.json) {
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  } else if (!values.quiet) {
    if (exitCode === 0) {
      const files = report.assets.map((asset) => path.basename(asset.path)).join(', ') || 'no media';
      process.stdout.write(`${report.status}: ${report.outputDir}  segments=${report.segments}  ${files}\n`);
    } else {
      process.stderr.write(`${report.status}: ${report.error ?? 'no output'}\n`);
    }
  }
  if (values.report) {
    const { writeFileSync, mkdirSync } = await import('node:fs');
    mkdirSync(path.dirname(values.report), { recursive: true });
    writeFileSync(values.report, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
  }
  return exitCode;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((code) => {
    process.exitCode = code;
  }, (error) => {
    process.stderr.write(`${error?.stack ?? error}\n`);
    process.exitCode = 1;
  });
}

export { REPO_ROOT };
