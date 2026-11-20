#!/usr/bin/env node
/**
 * Download a CSV of Facebook video URLs: one Run and one media folder per non-empty row.
 *
 * Without `--execute` this only validates the file and prints a plan; it reads no token and
 * creates no Run.
 */
import { parseArgs } from 'node:util';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { APPROVED_BUILD_NUMBER, ConfigurationError, validateVideoUrl } from './client.mjs';
import { estimateCostUsd, formatEstimate } from './cost.mjs';
import { downloadOne } from './pipeline.mjs';
import { loadEnvFile } from './download.mjs';

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const COUNTER_KEYS = [
  'success', 'actor_error', 'empty', 'unknown',
  'transport_error', 'run_failed', 'creation_unknown', 'download_failed', 'config_error',
];

/** Minimal RFC 4180 reader: quotes, escaped quotes, CRLF, and a trailing newline. */
export function parseCsv(text) {
  const rows = [];
  let row = [];
  let field = '';
  let quoted = false;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quoted) {
      if (char === '"' && text[index + 1] === '"') {
        field += '"';
        index += 1;
      } else if (char === '"') {
        quoted = false;
      } else {
        field += char;
      }
    } else if (char === '"') {
      quoted = true;
    } else if (char === ',') {
      row.push(field);
      field = '';
    } else if (char === '\n' || char === '\r') {
      if (char === '\r' && text[index + 1] === '\n') index += 1;
      row.push(field);
      rows.push(row);
      row = [];
      field = '';
    } else {
      field += char;
    }
  }
  if (field !== '' || row.length) {
    row.push(field);
    rows.push(row);
  }
  return rows.filter((entry) => entry.some((value) => value.trim() !== ''));
}

export function parseRows(text) {
  const table = parseCsv(text.replace(/^\uFEFF/, ''));
  if (table.length === 0) throw new ConfigurationError('CSV contains no rows');
  const header = table[0].map((name) => name.trim().toLowerCase());
  const urlIndex = header.indexOf('videourl') >= 0 ? header.indexOf('videourl') : header.indexOf('url');
  if (urlIndex < 0) throw new ConfigurationError("CSV must have a 'videoUrl' or 'url' column");
  const rows = [];
  for (let index = 1; index < table.length; index += 1) {
    const lineNumber = String(index + 1);
    const url = (table[index][urlIndex] ?? '').trim();
    if (!url) continue;
    let canonical;
    let form;
    try {
      ({ canonicalUrl: canonical, form } = validateVideoUrl(url));
    } catch (error) {
      throw new ConfigurationError(`CSV line ${lineNumber}: ${error.message}`);
    }
    rows.push({ rowId: lineNumber, url, canonicalUrl: canonical, urlForm: form });
  }
  if (rows.length === 0) throw new ConfigurationError('CSV contains no non-empty URL rows');
  return rows;
}

export function estimateRows(rows) {
  return estimateCostUsd({ videos: rows.length });
}

export function countResults(results) {
  const counts = Object.fromEntries(COUNTER_KEYS.map((key) => [key, 0]));
  for (const row of results) {
    const status = COUNTER_KEYS.includes(row.status) ? row.status : 'unknown';
    counts[status] += 1;
  }
  counts.processed = results.length;
  counts.failed = counts.processed - counts.success;
  return counts;
}

export async function main(argv = process.argv.slice(2)) {
  const { values, positionals } = parseArgs({
    args: argv,
    allowPositionals: true,
    options: {
      execute: { type: 'boolean', default: false },
      out: { type: 'string', default: 'downloads' },
      limit: { type: 'string' },
      'max-concurrency': { type: 'string', default: '3' },
      output: { type: 'string' },
      'no-video': { type: 'boolean', default: false },
      audio: { type: 'boolean', default: false },
      'audio-format': { type: 'string', default: 'raw' },
      force: { type: 'boolean', default: false },
      timeout: { type: 'string', default: '900' },
      build: { type: 'string', default: APPROVED_BUILD_NUMBER },
      'actor-id': { type: 'string' },
      'dataset-limit': { type: 'string', default: '100' },
      'max-total-charge-usd': { type: 'string' },
      'require-charge-cap': { type: 'boolean', default: false },
    },
  });

  if (positionals.length !== 1) {
    process.stderr.write('Usage: node examples/nodejs/batch-download.mjs <file.csv> [--execute]\n');
    return 2;
  }

  const csvPath = path.resolve(positionals[0]);
  let rows;
  let estimate;
  try {
    rows = parseRows(readFileSync(csvPath, 'utf8'));
    if (values.limit !== undefined) {
      const limit = Number(values.limit);
      if (!Number.isInteger(limit) || limit < 1) throw new ConfigurationError('--limit must be a positive integer');
      rows = rows.slice(0, limit);
    }
    estimate = estimateRows(rows);
  } catch (error) {
    process.stderr.write(`Configuration error: ${error.message}\n`);
    return 2;
  }

  process.stdout.write(`CSV: ${csvPath}\nRows: ${rows.length}\n`);
  process.stdout.write(`Estimate: ${formatEstimate(estimate)}\n`);
  process.stdout.write(`Output: ${path.resolve(values.out)}\n`);

  if (!values.execute) {
    process.stdout.write('Plan only. No Run was created and no token was read.\n');
    process.stdout.write('Re-run with --execute to download these rows.\n');
    return 0;
  }

  loadEnvFile();
  const token = process.env.APIFY_TOKEN;
  if (!token || !token.trim()) {
    process.stderr.write('Configuration error: APIFY_TOKEN is required when --execute is used\n');
    return 2;
  }

  const concurrency = Math.max(1, Number(values['max-concurrency']) || 1);
  const results = new Array(rows.length);
  let cursor = 0;
  async function worker() {
    for (;;) {
      const index = cursor;
      cursor += 1;
      if (index >= rows.length) return;
      const row = rows[index];
      const { report, exitCode } = await downloadOne(row.url, {
        outDir: values.out,
        wantVideo: !values['no-video'],
        wantAudio: values.audio,
        audioFormat: values['audio-format'],
        force: values.force,
        build: values.build,
        actorId: values['actor-id'] ?? null,
        timeout: Number(values.timeout),
        maxTotalChargeUsd: values['max-total-charge-usd'] ?? null,
        requireChargeCap: values['require-charge-cap'],
        datasetLimit: Number(values['dataset-limit']),
        rowId: row.rowId,
        token,
      });
      results[index] = { ...report, rowId: row.rowId, exitCode };
      const assetList = report.assets.map((asset) => path.basename(asset.path)).join(',') || '-';
      process.stdout.write(`row ${row.rowId}: ${report.status} ${assetList}\n`);
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, rows.length) }, worker));

  const counts = countResults(results);
  const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\.\d+Z$/, 'Z');
  let resultPath = path.resolve(values.output ?? path.join(path.dirname(csvPath), `facebook-batch-download-${stamp}.json`));
  if (existsSync(resultPath)) resultPath = resultPath.replace(/\.json$/, `-${Date.now()}.json`);
  writeFileSync(resultPath, `${JSON.stringify({
    generatedAt: new Date().toISOString(),
    actorId: values['actor-id'] ?? process.env.APIFY_ACTOR_ID ?? 'apple_yang/facebook-video-audio-downloader',
    build: values.build,
    source: csvPath,
    outputDir: path.resolve(values.out),
    estimate,
    counts,
    results,
  }, null, 2)}\n`, { mode: 0o600 });

  process.stdout.write(`Result: ${resultPath}\n`);
  process.stdout.write(`Counts: ${Object.entries(counts).map(([key, value]) => `${key}=${value}`).join(', ')}\n`);
  const failed = results.filter((row) => row.exitCode !== 0).map((row) => row.rowId);
  if (failed.length) {
    process.stderr.write(`Rows that did not finish cleanly: ${failed.join(', ')}\n`);
    return 1;
  }
  return 0;
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
