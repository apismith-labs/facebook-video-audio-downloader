/** Pipeline tests: fake Actor Run plus a local media server, no network. */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { FakeTransport, tempDir, withServer } from './support.mjs';
import { APPROVED_BUILD_ID, APPROVED_BUILD_NUMBER } from '../../examples/nodejs/client.mjs';
import { DOWNLOAD_FAILED_EXIT, downloadOne } from '../../examples/nodejs/pipeline.mjs';

const VIDEO = Buffer.from(`\u0000\u0000\u0000\u0018ftypmp42${'v'.repeat(5000)}`, 'binary');
const AUDIO = Buffer.from(`\u0000\u0000\u0000\u0018ftypmp42${'a'.repeat(2000)}`, 'binary');

function makeItem(videoUrl, audioUrl, { text = 'hello world', errMsg = null } = {}) {
  return {
    url: 'https://www.facebook.com/reel/9/',
    title: 'Sunset Timelapse',
    videoUrl,
    audioUrl,
    createTime: 1791609487,
    playCount: 10,
    commentCount: 1,
    reactionCount: 33,
    likesCount: 33,
    shareCount: 2,
    duration: 42.261,
    nickname: 'ttvnews',
    avatarUri: 'https://scontent.xx.fbcdn.net/v/avatar.jpg?oh=secret',
    text,
    segments: text ? [{ start: 0, end: 1.5, text: 'hello' }, { start: 1.5, end: 3, text: 'world' }] : [],
    errMsg,
    timestamp: '2026-10-28T00:00:00.000Z',
  };
}

function responsesFor(item, { runId = 'RUN1', datasetId = 'DS1' } = {}) {
  const created = { data: { id: runId, defaultDatasetId: datasetId, buildNumber: APPROVED_BUILD_NUMBER, buildId: APPROVED_BUILD_ID } };
  const finished = { data: { status: 'SUCCEEDED', defaultDatasetId: datasetId, buildNumber: APPROVED_BUILD_NUMBER, buildId: APPROVED_BUILD_ID } };
  return [created, finished, [item]];
}

async function withToken(run) {
  const previous = process.env.APIFY_TOKEN;
  process.env.APIFY_TOKEN = 'offline-test-token';
  try {
    return await run();
  } finally {
    if (previous === undefined) delete process.env.APIFY_TOKEN;
    else process.env.APIFY_TOKEN = previous;
  }
}

test('downloads video, transcript, and metadata', async () => {
  const out = await tempDir();
  await withToken(async () => {
    await withServer(VIDEO, {}, async (videoUrl) => {
      await withServer(AUDIO, {}, async (audioUrl) => {
        const transport = new FakeTransport(responsesFor(makeItem(videoUrl, audioUrl)));
        const { report, exitCode } = await downloadOne('https://www.facebook.com/reel/9/', { outDir: out, transport });
        assert.equal(exitCode, 0, report.error);
        const folder = report.outputDir;
        assert.equal(path.basename(folder), 'reel-9-sunset-timelapse');
        assert.deepEqual(await fs.readFile(path.join(folder, 'video.mp4')), VIDEO);
        assert.equal(await fs.readFile(path.join(folder, 'transcript.txt'), 'utf8'), 'hello world\n');
        assert.deepEqual(JSON.parse(await fs.readFile(path.join(folder, 'segments.json'), 'utf8')), [
          { start: 0, end: 1.5, text: 'hello' },
          { start: 1.5, end: 3, text: 'world' },
        ]);
        const metadata = JSON.parse(await fs.readFile(path.join(folder, 'metadata.json'), 'utf8'));
        assert.equal(metadata.videoUrl, videoUrl);
        assert.equal(metadata.likesCount, 33);
        const manifest = JSON.parse(await fs.readFile(path.join(folder, 'manifest.json'), 'utf8'));
        assert.equal(manifest.schemaVersion, 1);
        assert.equal(manifest.runId, 'RUN1');
        assert.equal(manifest.duration, 42.261);
        assert.equal(report.segments, 2);
        await assert.rejects(fs.access(path.join(folder, 'audio.mp4')));
        assert.equal(transport.postCount, 1);
      });
    });
  });
  await fs.rm(out, { recursive: true, force: true });
});

test('audio is downloaded on request', async () => {
  const out = await tempDir();
  await withToken(async () => {
    await withServer(VIDEO, {}, async (videoUrl) => {
      await withServer(AUDIO, {}, async (audioUrl) => {
        const transport = new FakeTransport(responsesFor(makeItem(videoUrl, audioUrl)));
        const { report, exitCode } = await downloadOne('https://www.facebook.com/reel/9/', {
          outDir: out, transport, wantVideo: false, wantAudio: true,
        });
        assert.equal(exitCode, 0);
        assert.deepEqual(await fs.readFile(path.join(report.outputDir, 'audio.mp4')), AUDIO);
        await assert.rejects(fs.access(path.join(report.outputDir, 'video.mp4')));
        assert.deepEqual(report.assets.map((asset) => asset.kind), ['audio']);
      });
    });
  });
  await fs.rm(out, { recursive: true, force: true });
});

test('a second run reuses the folder and skips media', async () => {
  const out = await tempDir();
  await withToken(async () => {
    await withServer(VIDEO, {}, async (videoUrl) => {
      await withServer(AUDIO, {}, async (audioUrl) => {
        const item = makeItem(videoUrl, audioUrl);
        const first = await downloadOne('https://www.facebook.com/reel/9/', {
          outDir: out, transport: new FakeTransport(responsesFor(item)),
        });
        const second = await downloadOne('https://www.facebook.com/reel/9/', {
          outDir: out, transport: new FakeTransport(responsesFor(item)),
        });
        assert.equal(second.exitCode, 0);
        assert.equal(first.report.outputDir, second.report.outputDir);
        assert.equal(second.report.assets[0].skipped, true);
      });
    });
  });
  await fs.rm(out, { recursive: true, force: true });
});

test('a silent video still downloads its media', async () => {
  const out = await tempDir();
  await withToken(async () => {
    await withServer(VIDEO, {}, async (videoUrl) => {
      await withServer(AUDIO, {}, async (audioUrl) => {
        const transport = new FakeTransport(responsesFor(makeItem(videoUrl, audioUrl, { text: null })));
        const { report, exitCode } = await downloadOne('https://www.facebook.com/reel/9/', { outDir: out, transport });
        assert.equal(exitCode, 0);
        assert.equal(report.status, 'no_transcript');
        assert.equal(report.segments, 0);
        assert.equal(await fs.readFile(path.join(report.outputDir, 'transcript.txt'), 'utf8'), '');
      });
    });
  });
  await fs.rm(out, { recursive: true, force: true });
});

test('an actor error writes metadata but no media', async () => {
  const out = await tempDir();
  await withToken(async () => {
    await withServer(VIDEO, {}, async (videoUrl) => {
      await withServer(AUDIO, {}, async (audioUrl) => {
        const item = makeItem(videoUrl, audioUrl, { text: null, errMsg: 'no audio url found' });
        const transport = new FakeTransport(responsesFor(item));
        const { report, exitCode } = await downloadOne('https://www.facebook.com/reel/9/', { outDir: out, transport });
        assert.equal(exitCode, 4);
        assert.equal(report.status, 'actor_error');
        await fs.access(path.join(report.outputDir, 'metadata.json'));
        await assert.rejects(fs.access(path.join(report.outputDir, 'video.mp4')));
        assert.equal(transport.postCount, 1);
      });
    });
  });
  await fs.rm(out, { recursive: true, force: true });
});

test('a missing media URL reports a download failure', async () => {
  const out = await tempDir();
  await withToken(async () => {
    const transport = new FakeTransport(responsesFor(makeItem('', '')));
    const { report, exitCode } = await downloadOne('https://www.facebook.com/reel/9/', { outDir: out, transport });
    assert.equal(exitCode, DOWNLOAD_FAILED_EXIT);
    assert.match(report.error, /no media URL/);
  });
  await fs.rm(out, { recursive: true, force: true });
});

test('an unreachable media URL reports a download failure but keeps the transcript', async () => {
  const out = await tempDir();
  await withToken(async () => {
    const transport = new FakeTransport(responsesFor(makeItem('http://127.0.0.1:9/x.mp4', 'http://127.0.0.1:9/x.mp4')));
    const { report, exitCode } = await downloadOne('https://www.facebook.com/reel/9/', { outDir: out, transport });
    assert.equal(exitCode, DOWNLOAD_FAILED_EXIT);
    assert.match(report.error, /download failed/);
    await fs.access(path.join(report.outputDir, 'transcript.txt'));
  });
  await fs.rm(out, { recursive: true, force: true });
});

test('--force re-derives the converted audio container', async () => {
  const bin = await tempDir();
  const counter = path.join(bin, 'calls');
  // Stand-in for ffmpeg: copies the `-i` input to the final argument and tags the call number so
  // the test can tell a re-derived file from a stale one.
  await fs.writeFile(path.join(bin, 'ffmpeg'), `#!/bin/sh
count=$(cat "${counter}" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "${counter}"
for last; do :; done
prev=""
for arg in "$@"; do
  if [ "$prev" = "-i" ]; then src="$arg"; fi
  prev="$arg"
done
cat "$src" > "$last"
printf 'call-%s' "$count" >> "$last"
`, { mode: 0o755 });

  const previousPath = process.env.PATH;
  process.env.PATH = `${bin}${path.delimiter}${previousPath ?? ''}`;
  const out = await tempDir();
  try {
    await withToken(async () => {
      await withServer(VIDEO, {}, async (videoUrl) => {
        await withServer(AUDIO, {}, async (audioUrl) => {
          const run = (extra) => downloadOne('https://www.facebook.com/reel/9/', {
            outDir: out, wantVideo: false, wantAudio: true, audioFormat: 'm4a',
            transport: new FakeTransport(responsesFor(makeItem(videoUrl, audioUrl))), ...extra,
          });

          const first = await run({});
          assert.equal(first.exitCode, 0, first.report.error);
          const converted = path.join(first.report.outputDir, 'audio.m4a');
          assert.match(await fs.readFile(converted, 'utf8'), /call-1$/);

          // A plain rerun keeps the existing container but still lists it in the report.
          const second = await run({});
          assert.match(await fs.readFile(converted, 'utf8'), /call-1$/);
          assert.deepEqual(second.report.assets.map((a) => a.kind), ['audio', 'audio-converted']);
          assert.equal(second.report.assets[0].skipped, true);
          // The container was reused, so the report must say so.
          assert.equal(second.report.assets[1].skipped, true);

          // --force re-downloads audio.mp4, so audio.m4a has to be rebuilt from the new bytes.
          const third = await run({ force: true });
          assert.equal(third.exitCode, 0, third.report.error);
          assert.match(await fs.readFile(converted, 'utf8'), /call-2$/);
          assert.deepEqual(third.report.assets.map((a) => a.kind), ['audio', 'audio-converted']);
          assert.equal(third.report.assets[1].skipped, false);
        });
      });
    });
  } finally {
    process.env.PATH = previousPath;
  }
  await fs.rm(bin, { recursive: true, force: true });
  await fs.rm(out, { recursive: true, force: true });
});

test('a bad URL never creates a Run', async () => {
  const out = await tempDir();
  const transport = new FakeTransport([]);
  const { report, exitCode } = await downloadOne('https://example.com/nope', { outDir: out, transport, token: 'x' });
  assert.equal(exitCode, 2);
  assert.equal(report.status, 'config_error');
  assert.equal(transport.postCount, 0);
  await fs.rm(out, { recursive: true, force: true });
});
