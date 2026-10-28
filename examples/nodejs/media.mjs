/**
 * Media download layer: resumable HTTP transfers to disk.
 *
 * A signed fbcdn link is short-lived, so download right after the Run succeeds. Downloads resume
 * with `Range`, verify the byte count and SHA-256, and are renamed into place only when complete.
 */
import { createHash } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { promises as fs } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';

export const CHUNK_SIZE = 1 << 20;
export const DEFAULT_TIMEOUT_SECONDS = 60;
export const DEFAULT_MAX_ATTEMPTS = 3;
const RETRYABLE_STATUS = new Set([408, 425, 429, 500, 502, 503, 504]);
const SIGNED_QUERY_KEYS = new Set([
  'oh', 'oe', 'sig', 'signature', 'token', 'hdnea', 'hash', 'expires', 'expire',
  'x-amz-signature', 'x-amz-credential', 'x-amz-security-token',
]);

export class MediaError extends Error {
  constructor(message, { status = null, retryable = false } = {}) {
    super(message);
    this.name = 'MediaError';
    this.status = status;
    this.retryable = retryable;
  }
}

export function redactMediaUrl(value) {
  if (typeof value !== 'string' || !value) return '';
  try {
    const parsed = new URL(value);
    return `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
  } catch {
    return '[unparseable media url]';
  }
}

export function mediaHost(value) {
  try {
    return new URL(value).hostname.toLowerCase().replace(/\.$/, '');
  } catch {
    return '';
  }
}

async function sha256Of(target, chunkSize = CHUNK_SIZE) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(target, { highWaterMark: chunkSize })) hash.update(chunk);
  return hash.digest('hex');
}

function parseContentRange(value) {
  if (!value) return null;
  const match = /\/(\d+)\s*$/.exec(value);
  return match ? Number(match[1]) : null;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const RETRYABLE_ERROR_CODES = new Set([
  'ECONNRESET', 'ECONNREFUSED', 'ECONNABORTED', 'EPIPE', 'ETIMEDOUT', 'EAI_AGAIN',
  'ENETDOWN', 'ENETUNREACH', 'EHOSTUNREACH', 'ERR_STREAM_PREMATURE_CLOSE',
]);

function isRetryableTransportFailure(error) {
  if (!error) return false;
  if (error.name === 'AbortError' || error instanceof TypeError) return true;
  return typeof error.code === 'string' && RETRYABLE_ERROR_CODES.has(error.code);
}

/**
 * Download `url` to `destination`, resuming a `.part` file when the server supports Range.
 * An already-verified destination is returned with `skipped: true`.
 */
export async function downloadMedia(url, destination, options = {}) {
  const {
    kind = 'video',
    timeout = DEFAULT_TIMEOUT_SECONDS,
    maxAttempts = DEFAULT_MAX_ATTEMPTS,
    force = false,
    progress = null,
    fetchImpl = globalThis.fetch,
    delay = sleep,
  } = options;

  if (typeof url !== 'string' || !/^https?:\/\//i.test(url)) {
    throw new MediaError('media URL must be an absolute http(s) URL');
  }
  if (!Number.isFinite(timeout) || timeout <= 0) throw new MediaError('timeout must be a finite positive number of seconds');
  if (!Number.isInteger(maxAttempts) || maxAttempts <= 0) throw new MediaError('maxAttempts must be a positive integer');

  const host = mediaHost(url);
  if (!force) {
    try {
      const stat = await fs.stat(destination);
      return {
        kind, path: destination, bytes: stat.size, sha256: await sha256Of(destination),
        contentType: null, sourceHost: host, skipped: true,
      };
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }

  await fs.mkdir(path.dirname(destination), { recursive: true });
  const partPath = `${destination}.part`;
  if (force) {
    await fs.rm(partPath, { force: true });
    await fs.rm(destination, { force: true });
  }

  let lastError = null;
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    let resumeFrom = 0;
    try {
      resumeFrom = (await fs.stat(partPath)).size;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }

    try {
      const headers = resumeFrom ? { Range: `bytes=${resumeFrom}-` } : {};
      const response = await fetchImpl(url, { headers, signal: AbortSignal.timeout(timeout * 1000) });
      if (response.status !== 200 && response.status !== 206) {
        throw new MediaError(`media server returned HTTP ${response.status}`, {
          status: response.status,
          retryable: RETRYABLE_STATUS.has(response.status),
        });
      }
      if (!response.body) throw new MediaError('media server returned an empty body');

      const append = resumeFrom > 0 && response.status === 206;
      const declared = Number(response.headers.get('content-length') ?? '0') || null;
      const total = parseContentRange(response.headers.get('content-range'));
      const expected = total ?? (append ? resumeFrom + (declared ?? 0) : declared);
      const contentType = response.headers.get('content-type');

      const stream = createWriteStream(partPath, { flags: append ? 'a' : 'w' });
      let written = append ? resumeFrom : 0;
      // `pipeline` owns backpressure and error propagation; a hand-rolled drain/error race leaks one
      // listener per stalled chunk, which trips MaxListenersExceededWarning on large files.
      const counter = new Transform({
        transform(chunk, _encoding, callback) {
          written += chunk.length;
          if (progress) progress(kind, written, expected);
          callback(null, chunk);
        },
        flush(callback) {
          if (progress) progress(kind, written, expected);
          callback();
        },
      });
      const source = typeof response.body.getReader === 'function'
        ? Readable.fromWeb(response.body)
        : Readable.from(response.body);
      await pipeline(source, counter, stream);

      const produced = (await fs.stat(partPath)).size;
      if (expected && produced !== expected) {
        throw new MediaError(`truncated download: expected ${expected} bytes, wrote ${produced}`, { retryable: true });
      }
      await fs.rename(partPath, destination);
      return {
        kind, path: destination, bytes: produced, sha256: await sha256Of(destination),
        contentType, sourceHost: host, skipped: false,
      };
    } catch (error) {
      if (error instanceof MediaError) {
        lastError = error;
        if (!error.retryable || attempt + 1 >= maxAttempts) throw error;
      } else if (isRetryableTransportFailure(error)) {
        // Pipeline errors surface as AbortError, TypeError, or a system error carrying `code`;
        // all of them mean the transfer died mid-flight and the `.part` file can be resumed.
        lastError = new MediaError(`media download transport failure: ${error.code ?? error.name ?? 'Error'}`, { retryable: true });
        if (attempt + 1 >= maxAttempts) throw lastError;
      } else {
        throw error;
      }
    }
    await delay(Math.min(2 ** attempt, 8) * 1000);
  }
  throw lastError ?? new MediaError('media download failed');
}

export function audioTarget(destination, audioFormat) {
  if (!['raw', 'm4a', 'mp3'].includes(audioFormat)) {
    throw new MediaError('audioFormat must be one of: raw, m4a, mp3');
  }
  if (audioFormat === 'raw') return { target: destination, needsRemux: false };
  const parsed = path.parse(destination);
  return { target: path.join(parsed.dir, `${parsed.name}.${audioFormat}`), needsRemux: true };
}

export function buildFfmpegArgs(source, target, audioFormat) {
  if (audioFormat === 'm4a') {
    return ['ffmpeg', '-nostdin', '-y', '-loglevel', 'error', '-i', source,
      '-vn', '-c:a', 'copy', '-movflags', '+faststart', target];
  }
  if (audioFormat === 'mp3') {
    return ['ffmpeg', '-nostdin', '-y', '-loglevel', 'error', '-i', source,
      '-vn', '-c:a', 'libmp3lame', '-b:a', '192k', target];
  }
  throw new MediaError('transcode is only defined for m4a and mp3');
}

export async function transcodeAudio(source, target, audioFormat, { runner = spawnSync } = {}) {
  try {
    await fs.access(source);
  } catch {
    throw new MediaError(`audio source is missing: ${source}`);
  }
  await fs.mkdir(path.dirname(target), { recursive: true });
  const [command, ...args] = buildFfmpegArgs(source, target, audioFormat);
  const result = runner(command, args, { encoding: 'utf8' });
  if (result.error) {
    throw new MediaError(`ffmpeg is required for --audio-format ${audioFormat}; install it or use raw`);
  }
  if (result.status !== 0) {
    const detail = (result.stderr ?? '').trim().split('\n').pop()?.slice(0, 160) ?? `exit code ${result.status}`;
    throw new MediaError(`ffmpeg failed: ${detail}`);
  }
  return target;
}
