/** .env discovery: all four clients resolve the nearest file, never a hard-coded path. */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { findEnvFile, loadEnvFile } from '../../examples/nodejs/download.mjs';

async function tempDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), 'fbdl-env-'));
}

test('findEnvFile walks up to the nearest .env', async () => {
  const root = await tempDir();
  const nested = path.join(root, 'examples', 'nodejs');
  await fs.mkdir(nested, { recursive: true });
  await fs.writeFile(path.join(root, '.env'), 'APIFY_TOKEN=outer\n');
  assert.equal(findEnvFile(nested), path.join(root, '.env'));

  await fs.writeFile(path.join(nested, '.env'), 'APIFY_TOKEN=inner\n');
  assert.equal(findEnvFile(nested), path.join(nested, '.env'));
  await fs.rm(root, { recursive: true, force: true });
});

test('findEnvFile returns null when nothing is above the start directory', async () => {
  const root = await tempDir();
  assert.equal(findEnvFile(root), null);
  await fs.rm(root, { recursive: true, force: true });
});

test('loadEnvFile keeps existing values and ignores non-APIFY keys', async () => {
  const root = await tempDir();
  const file = path.join(root, '.env');
  await fs.writeFile(file, '# comment\nAPIFY_TOKEN=from-file\nAPIFY_KEEP=ignored\nNOT_APIFY=skipped\n');
  const previous = {
    token: process.env.APIFY_TOKEN,
    keep: process.env.APIFY_KEEP,
    other: process.env.NOT_APIFY,
    skip: process.env.APIFY_SKIP_DOTENV,
  };
  try {
    // The suite runs with APIFY_SKIP_DOTENV=1 so a real token never leaks in; this test exercises
    // the loader itself, so the switch has to be off for its duration.
    delete process.env.APIFY_SKIP_DOTENV;
    delete process.env.APIFY_TOKEN;
    process.env.APIFY_KEEP = 'keep';
    delete process.env.NOT_APIFY;
    loadEnvFile(file);
    assert.equal(process.env.APIFY_TOKEN, 'from-file');
    assert.equal(process.env.APIFY_KEEP, 'keep');
    assert.equal(process.env.NOT_APIFY, undefined);
  } finally {
    if (previous.token === undefined) delete process.env.APIFY_TOKEN;
    else process.env.APIFY_TOKEN = previous.token;
    if (previous.keep === undefined) delete process.env.APIFY_KEEP;
    else process.env.APIFY_KEEP = previous.keep;
    if (previous.other !== undefined) process.env.NOT_APIFY = previous.other;
    if (previous.skip !== undefined) process.env.APIFY_SKIP_DOTENV = previous.skip;
  }
  await fs.rm(root, { recursive: true, force: true });
});
