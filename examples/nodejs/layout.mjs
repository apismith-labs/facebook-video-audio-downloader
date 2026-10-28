/**
 * Output layout: deterministic folder names and the sidecar files written next to the media.
 *
 * Every language client produces the same layout, so a download started with Python can be
 * resumed or inspected by the Node, Go, or Java examples.
 */
import { promises as fs } from 'node:fs';
import path from 'node:path';

export const MANIFEST_SCHEMA_VERSION = 1;
const TITLE_LIMIT = 48;
const MEDIA_FIELDS = ['videoUrl', 'audioUrl', 'avatarUri'];

export function utcNow() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
}

export function slugify(value, limit = TITLE_LIMIT) {
  if (!value || typeof value !== 'string') return '';
  const ascii = value
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^\x00-\x7F]/g, '');
  const slug = ascii.replace(/[^A-Za-z0-9]+/g, '-').replace(/^-+|-+$/g, '').toLowerCase();
  return slug.length > limit ? slug.slice(0, limit).replace(/-+$/, '') : slug;
}

export function videoIdentity(canonicalUrl, urlForm) {
  const parsed = new URL(canonicalUrl);
  const pathname = parsed.pathname || '/';
  if (urlForm === 'watch' || urlForm === 'video_php') {
    const id = parsed.searchParams.get('v');
    const prefix = urlForm === 'watch' ? 'watch' : 'video';
    return /^\d+$/.test(id ?? '') ? `${prefix}-${id}` : `${prefix}-unknown`;
  }
  let match = pathname.match(/^\/([A-Za-z0-9._-]+)\/videos\/(\d+)\/?$/);
  if (match && urlForm === 'page_video') return `page-${slugify(match[1], 24)}-${match[2]}`;
  match = pathname.match(/^\/videos\/(\d+)\/?$/);
  if (match) return `video-${match[1]}`;
  match = pathname.match(/^\/reel\/(\d+)\/?$/i);
  if (match) return `reel-${match[1]}`;
  match = pathname.match(/^\/share\/([A-Za-z]+)\/([A-Za-z0-9_-]+)\/?$/i);
  if (match) return `share-${match[1].toLowerCase()}-${match[2]}`;
  if (urlForm === 'short_link') {
    match = pathname.match(/^\/([A-Za-z0-9_-]+)\/?$/);
    if (match) return `short-${match[1]}`;
  }
  return `${urlForm || 'video'}-unknown`;
}

export function folderName(canonicalUrl, urlForm, title) {
  const identity = videoIdentity(canonicalUrl, urlForm);
  const suffix = slugify(title);
  return suffix ? `${identity}-${suffix}` : identity;
}

/** Reuse an existing folder for the same URL; never overwrite media from a different one. */
export async function chooseOutputDir(base, folder, canonicalUrl) {
  let candidate = path.join(base, folder);
  let index = 2;
  for (;;) {
    let manifest = null;
    try {
      manifest = JSON.parse(await fs.readFile(path.join(candidate, 'manifest.json'), 'utf8'));
    } catch (error) {
      if (error.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error;
      if (error.code === 'ENOENT') return candidate;
      return candidate; // Unreadable manifest: treat the folder as ours.
    }
    if (!manifest.canonicalUrl || manifest.canonicalUrl === canonicalUrl) return candidate;
    candidate = path.join(base, `${folder}-${index}`);
    index += 1;
  }
}

/** Reduce signed media URLs to scheme://host/path before persisting them. */
export function sanitizeItem(item, { keepMediaUrls = false } = {}) {
  if (!item || typeof item !== 'object' || Array.isArray(item)) return {};
  const cleaned = { ...item };
  for (const field of MEDIA_FIELDS) {
    const value = cleaned[field];
    if (typeof value !== 'string' || !value || keepMediaUrls) continue;
    try {
      const parsed = new URL(value);
      cleaned[field] = `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
    } catch {
      // Leave unparseable values untouched; they carry no usable credentials anyway.
    }
  }
  return cleaned;
}

export function transcriptSegments(segments) {
  if (!Array.isArray(segments)) return [];
  const result = [];
  for (const entry of segments) {
    if (!entry || typeof entry !== 'object') continue;
    const { start, end, text } = entry;
    if (typeof start !== 'number' || typeof end !== 'number') continue;
    if (!Number.isFinite(start) || !Number.isFinite(end)) continue;
    if (start < 0 || end < start) continue;
    if (typeof text !== 'string') continue;
    result.push({ start, end, text });
  }
  return result;
}

export async function writeText(target, text) {
  await fs.mkdir(path.dirname(target), { recursive: true });
  const temporary = `${target}.tmp`;
  await fs.writeFile(temporary, text, 'utf8');
  await fs.rename(temporary, target);
  return Buffer.byteLength(text, 'utf8');
}

export async function writeJson(target, payload) {
  return writeText(target, `${JSON.stringify(payload, null, 2)}\n`);
}

export function buildManifest({ sourceUrl, canonicalUrl, urlForm, result, assets, transcriptBytes, segmentCount }) {
  return {
    schemaVersion: MANIFEST_SCHEMA_VERSION,
    sourceUrl,
    canonicalUrl,
    urlForm,
    status: result.status ?? null,
    runId: result.runId ?? null,
    datasetId: result.datasetId ?? null,
    title: result.title ?? null,
    nickname: result.nickname ?? null,
    duration: result.duration ?? null,
    downloadedAt: utcNow(),
    assets,
    transcript: { bytes: transcriptBytes, segments: segmentCount },
    notes: [
      'Media links returned by the Actor are signed and short-lived; they are not stored here.',
      'Re-running the same URL reuses this folder and skips files whose size still matches.',
    ],
  };
}
