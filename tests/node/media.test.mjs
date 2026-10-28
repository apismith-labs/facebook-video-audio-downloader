/**
 * Media layer tests: resumption, verification, retries, and URL redaction.
 * Uses a local HTTP server so `Range` handling is exercised over a real socket.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createHash } from 'node:crypto';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import {
  MediaError,
  audioTarget,
  buildFfmpegArgs,
  downloadMedia,
  mediaHost,
  redactMediaUrl,
  transcodeAudio,
} from '../../examples/nodejs/media.mjs';

const PAYLOAD = Buffer.alloc(102400);
for (let index = 0; index < PAYLOAD.length; index += 1) PAYLOAD[index] = index % 256;

async function withServer(payload, { supportRange = true } = {}, run) {
  const server = http.createServer((request, response) => {
    const header = supportRange ? request.headers.range : undefined;
    let start = 0;
    if (header?.startsWith('bytes=')) {
      start = Number(header.slice(6).split('-')[0] || 0);
      if (start >= payload.length) {
        response.writeHead(416, { 'Content-Range': `bytes */${payload.length}`, 'Content-Length': '0' });
        response.end();
        return;
      }
    }
    const body = payload.subarray(start);
    const headers = { 'Content-Type': 'video/mp4', 'Content-Length': String(body.length) };
    if (start > 0) headers['Content-Range'] = `bytes ${start}-${payload.length - 1}/${payload.length}`;
    response.writeHead(start > 0 ? 206 : 200, headers);
    response.end(body);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}/media.mp4`;
  try {
    return await run(url);
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

async function tempDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), 'fbdl-'));
}

const silentDelay = async () => {};

test('downloads a full file and verifies the hash', async () => {
  const dir = await tempDir();
  const destination = path.join(dir, 'video.mp4');
  await withServer(PAYLOAD, {}, async (url) => {
    const asset = await downloadMedia(url, destination);
    assert.equal(asset.bytes, PAYLOAD.length);
    assert.equal(asset.sha256, createHash('sha256').update(PAYLOAD).digest('hex'));
    assert.equal(asset.contentType, 'video/mp4');
    assert.equal(asset.skipped, false);
  });
  const onDisk = await fs.readFile(destination);
  assert.deepEqual(onDisk, PAYLOAD);
  await assert.rejects(fs.access(`${destination}.part`));
  await fs.rm(dir, { recursive: true, force: true });
});

test('a large transfer does not accumulate write-stream listeners', async () => {
  // Regression: the previous drain/error race left one listener behind per stalled chunk, which
  // surfaced as MaxListenersExceededWarning on multi-megabyte videos.
  const big = Buffer.alloc(6 * 1024 * 1024);
  for (let index = 0; index < big.length; index += 4096) big[index] = index % 256;
  const dir = await tempDir();
  const destination = path.join(dir, 'video.mp4');
  const warnings = [];
  const onWarning = (warning) => warnings.push(warning);
  process.on('warning', onWarning);
  try {
    await withServer(big, {}, async (url) => {
      const asset = await downloadMedia(url, destination);
      assert.equal(asset.bytes, big.length);
    });
    await new Promise((resolve) => setImmediate(resolve));
  } finally {
    process.off('warning', onWarning);
  }
  assert.deepEqual(
    warnings.filter((warning) => warning.name === 'MaxListenersExceededWarning').map((w) => w.message),
    [],
  );
  await fs.rm(dir, { recursive: true, force: true });
});

test('resumes from a partial file using Range', async () => {
  const dir = await tempDir();
  const destination = path.join(dir, 'video.mp4');
  await fs.writeFile(`${destination}.part`, PAYLOAD.subarray(0, 40000));
  await withServer(PAYLOAD, {}, async (url) => {
    const asset = await downloadMedia(url, destination);
    assert.equal(asset.bytes, PAYLOAD.length);
  });
  assert.deepEqual(await fs.readFile(destination), PAYLOAD);
  await fs.rm(dir, { recursive: true, force: true });
});

test('restarts when the server ignores Range', async () => {
  const dir = await tempDir();
  const destination = path.join(dir, 'video.mp4');
  await fs.writeFile(`${destination}.part`, PAYLOAD.subarray(0, 40000));
  await withServer(PAYLOAD, { supportRange: false }, async (url) => {
    await downloadMedia(url, destination);
  });
  assert.deepEqual(await fs.readFile(destination), PAYLOAD);
  await fs.rm(dir, { recursive: true, force: true });
});

test('an existing file is skipped without any request', async () => {
  const dir = await tempDir();
  const destination = path.join(dir, 'video.mp4');
  await fs.writeFile(destination, PAYLOAD);
  const asset = await downloadMedia('http://127.0.0.1:9/never.mp4', destination, {
    fetchImpl: () => {
      throw new Error('network must not be used');
    },
  });
  assert.equal(asset.skipped, true);
  assert.equal(asset.sha256, createHash('sha256').update(PAYLOAD).digest('hex'));
  await fs.rm(dir, { recursive: true, force: true });
});

test('force replaces an existing file', async () => {
  const dir = await tempDir();
  const destination = path.join(dir, 'video.mp4');
  await fs.writeFile(destination, Buffer.from('stale'));
  await withServer(PAYLOAD, {}, async (url) => {
    const asset = await downloadMedia(url, destination, { force: true });
    assert.equal(asset.skipped, false);
  });
  assert.deepEqual(await fs.readFile(destination), PAYLOAD);
  await fs.rm(dir, { recursive: true, force: true });
});

test('a 403 is not retried', async () => {
  const dir = await tempDir();
  let calls = 0;
  const fetchImpl = async () => {
    calls += 1;
    return new Response('', { status: 403 });
  };
  await assert.rejects(
    downloadMedia('https://cdn.example/v.mp4', path.join(dir, 'v.mp4'), { fetchImpl, delay: silentDelay }),
    (error) => error instanceof MediaError && error.status === 403,
  );
  assert.equal(calls, 1);
  await fs.rm(dir, { recursive: true, force: true });
});

test('a transient 503 is retried', async () => {
  const dir = await tempDir();
  let calls = 0;
  const fetchImpl = async () => {
    calls += 1;
    if (calls === 1) return new Response('', { status: 503 });
    return new Response(PAYLOAD, { status: 200, headers: { 'Content-Type': 'video/mp4' } });
  };
  const asset = await downloadMedia('https://cdn.example/v.mp4', path.join(dir, 'v.mp4'), {
    fetchImpl, delay: silentDelay,
  });
  assert.equal(calls, 2);
  assert.equal(asset.bytes, PAYLOAD.length);
  await fs.rm(dir, { recursive: true, force: true });
});

test('a truncated body is reported', async () => {
  const dir = await tempDir();
  const fetchImpl = async () => new Response(PAYLOAD.subarray(0, 100), {
    status: 200,
    headers: { 'Content-Length': String(PAYLOAD.length), 'Content-Type': 'video/mp4' },
  });
  await assert.rejects(
    downloadMedia('https://cdn.example/v.mp4', path.join(dir, 'v.mp4'), {
      fetchImpl, delay: silentDelay, maxAttempts: 2,
    }),
    /truncated download/,
  );
  await fs.rm(dir, { recursive: true, force: true });
});

test('signed query parameters are removed', () => {
  assert.equal(
    redactMediaUrl('https://scontent.xx.fbcdn.net/v/t39/1/n.mp4?oh=abc&oe=def&sig=ghi'),
    'https://scontent.xx.fbcdn.net/v/t39/1/n.mp4',
  );
  assert.equal(mediaHost('https://scontent.xx.fbcdn.net/v/a.mp4?oh=1'), 'scontent.xx.fbcdn.net');
  assert.equal(redactMediaUrl('not a url'), '[unparseable media url]');
});

test('audio formats map to containers', () => {
  assert.equal(audioTarget('out/audio.mp4', 'raw').needsRemux, false);
  assert.equal(audioTarget('out/audio.mp4', 'm4a').target, path.join('out', 'audio.m4a'));
  assert.equal(audioTarget('out/audio.mp4', 'mp3').target, path.join('out', 'audio.mp3'));
  assert.throws(() => audioTarget('out/audio.mp4', 'flac'), MediaError);
  assert.ok(buildFfmpegArgs('a.mp4', 'a.m4a', 'm4a').includes('copy'));
  assert.ok(buildFfmpegArgs('a.mp4', 'a.mp3', 'mp3').includes('libmp3lame'));
});

test('ffmpeg failures surface the last stderr line', async () => {
  const dir = await tempDir();
  const source = path.join(dir, 'audio.mp4');
  await fs.writeFile(source, 'x');
  await assert.rejects(
    transcodeAudio(source, path.join(dir, 'audio.m4a'), 'm4a', {
      runner: () => ({ status: 1, stderr: 'boom\ninvalid data found\n' }),
    }),
    /invalid data found/,
  );
  await fs.rm(dir, { recursive: true, force: true });
});

test('non-http URLs are rejected', async () => {
  await assert.rejects(downloadMedia('ftp://example.invalid/x', '/tmp/x'), MediaError);
});
