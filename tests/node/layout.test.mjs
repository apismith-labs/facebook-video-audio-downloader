/** Layout tests: deterministic names, folder reuse, and signed-URL redaction. */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { tempDir } from './support.mjs';
import {
  chooseOutputDir,
  folderName,
  sanitizeItem,
  slugify,
  transcriptSegments,
  videoIdentity,
  writeJson,
  writeText,
} from '../../examples/nodejs/layout.mjs';

test('titles become ascii slugs', () => {
  assert.equal(slugify('A Day at the Beach!'), 'a-day-at-the-beach');
  assert.equal(slugify('海边的一天'), '');
  assert.equal(slugify('Trip 2026 — 海边'), 'trip-2026');
  // Digits split by CJK punctuation collapse, matching the Python/Go/Java clients. A dash here
  // would fork the output folder name between clients.
  assert.equal(slugify('韓國瑜 稱有人想當美國第51州、日本第5大島、中國第31省'), '51531');
  assert.equal(slugify('第51州、日本第5大島、中國第31省'), '51531');
  assert.equal(slugify('a'.repeat(60), 10), 'a'.repeat(10));
});

test('identity is stable per URL form', () => {
  const cases = [
    ['https://www.facebook.com/watch/?v=123', 'watch', 'watch-123'],
    ['https://www.facebook.com/video.php?v=123', 'video_php', 'video-123'],
    ['https://www.facebook.com/PageName/videos/123/', 'page_video', 'page-pagename-123'],
    ['https://www.facebook.com/videos/123/', 'video', 'video-123'],
    ['https://www.facebook.com/reel/123/', 'reel', 'reel-123'],
    ['https://www.facebook.com/share/r/AbC-1/', 'share', 'share-r-AbC-1'],
    ['https://fb.watch/AbC123/', 'short_link', 'short-AbC123'],
  ];
  for (const [url, form, expected] of cases) assert.equal(videoIdentity(url, form), expected, url);
});

test('folder name appends the title slug', () => {
  assert.equal(folderName('https://www.facebook.com/reel/9/', 'reel', 'Sunset Timelapse'), 'reel-9-sunset-timelapse');
});

test('the same URL reuses its folder', async () => {
  const base = await tempDir();
  const target = path.join(base, 'reel-1');
  await writeJson(path.join(target, 'manifest.json'), { canonicalUrl: 'https://x/1' });
  assert.equal(await chooseOutputDir(base, 'reel-1', 'https://x/1'), target);
  await fs.rm(base, { recursive: true, force: true });
});

test('a different URL gets a numeric suffix', async () => {
  const base = await tempDir();
  const target = path.join(base, 'reel-1');
  await writeJson(path.join(target, 'manifest.json'), { canonicalUrl: 'https://x/other' });
  assert.equal(await chooseOutputDir(base, 'reel-1', 'https://x/1'), path.join(base, 'reel-1-2'));
  await fs.rm(base, { recursive: true, force: true });
});

test('signed URL queries are stripped', () => {
  const cleaned = sanitizeItem({
    videoUrl: 'https://scontent.xx.fbcdn.net/v/a.mp4?oh=1&oe=2&sig=3',
    audioUrl: 'https://scontent.xx.fbcdn.net/v/b.mp4?oh=1',
    text: 'hello',
  });
  assert.equal(cleaned.videoUrl, 'https://scontent.xx.fbcdn.net/v/a.mp4');
  assert.equal(cleaned.audioUrl, 'https://scontent.xx.fbcdn.net/v/b.mp4');
  assert.equal(cleaned.text, 'hello');
});

test('opting in keeps the signed URLs', () => {
  const cleaned = sanitizeItem({ videoUrl: 'https://x/y.mp4?sig=3' }, { keepMediaUrls: true });
  assert.equal(cleaned.videoUrl, 'https://x/y.mp4?sig=3');
});

test('invalid segments are dropped', () => {
  assert.deepEqual(
    transcriptSegments([
      { start: 0, end: 1.5, text: 'a' },
      { start: 2, end: 1, text: 'reversed' },
      { start: '0', end: 1, text: 'string' },
      { start: 1, end: 2, text: 5 },
      'nope',
    ]),
    [{ start: 0, end: 1.5, text: 'a' }],
  );
});

test('text is written atomically and measured in bytes', async () => {
  const dir = await tempDir();
  const bytes = await writeText(path.join(dir, 'a.txt'), 'hi\n');
  assert.equal(bytes, 3);
  assert.equal(await fs.readFile(path.join(dir, 'a.txt'), 'utf8'), 'hi\n');
  await assert.rejects(fs.access(path.join(dir, 'a.txt.tmp')));
  await fs.rm(dir, { recursive: true, force: true });
});
