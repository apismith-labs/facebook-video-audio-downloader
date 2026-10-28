/**
 * Staged Apify REST runner for the Facebook Transcript Actor.
 *
 * Importing this module performs no I/O. The three stages — create, poll, read Dataset — have
 * different retry rules on purpose. Run creation is never retried.
 */
import { appendFileSync, chmodSync, existsSync, mkdirSync, openSync, readFileSync, closeSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { randomUUID } from 'node:crypto';

export const TERMINAL = new Set(['SUCCEEDED', 'FAILED', 'TIMED-OUT', 'ABORTED']);

export const STATUSES = [
  'success',
  'actor_error',
  'empty',
  'no_transcript',
  'unknown',
  'transport_error',
  'run_failed',
  'creation_unknown',
];

export const EXIT_CODES = {
  success: 0,
  actor_error: 4,
  empty: 5,
  no_transcript: 6,
  unknown: 7,
  transport_error: 8,
  run_failed: 9,
  creation_unknown: 10,
};

// Pinned build: see docs/audit/actor_audit_20261028.md.
export const APPROVED_BUILD_NUMBER = '0.0.18';
export const APPROVED_BUILD_ID = 'fZIlpYENof2jYqIJc';
export const DEFAULT_ACTOR_ID = 'apple_yang/facebook-transcript-scraper-api';

export function buildRunInput(videoUrl) {
  return { videoUrl };
}

export function validateBuild(value, { required = false } = {}) {
  if (value === null || value === undefined || (typeof value === 'string' && value.trim() === '')) {
    if (required) throw new Error('APIFY_BUILD is required when a Build must be pinned');
    return null;
  }
  const build = String(value).trim();
  if (!/^\d+(?:\.\d+){0,2}$/.test(build) || build.toLowerCase() === 'latest') {
    throw new Error('APIFY_BUILD must be an explicit numeric Build number');
  }
  if (required && build !== APPROVED_BUILD_NUMBER) {
    throw new Error(`APIFY_BUILD must be ${APPROVED_BUILD_NUMBER} in this client`);
  }
  return build;
}

export function validateRunChargeCap(value, { required = false } = {}) {
  if (value === null || value === undefined || (typeof value === 'string' && value.trim() === '')) {
    if (required) throw new Error('APIFY_MAX_TOTAL_CHARGE_USD is required for capped test mode');
    return null;
  }
  const amount = Number(String(value).trim());
  if (!Number.isFinite(amount) || amount <= 0) {
    throw new Error('APIFY_MAX_TOTAL_CHARGE_USD must be a finite positive number');
  }
  return amount;
}

export class ApiError extends Error {
  constructor(message, { status = null, retryAfter = null, ambiguous = false } = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.retryAfter = retryAfter;
    this.ambiguous = ambiguous;
  }
}

export class RestTransport {
  constructor(token, { baseUrl = 'https://api.apify.com/v2', fetchImpl = globalThis.fetch } = {}) {
    this.token = token;
    this.baseUrl = baseUrl.replace(/\/+$/, '');
    this.fetchImpl = fetchImpl;
  }

  async request(method, path, body = undefined, { timeout = 30, query = undefined } = {}) {
    let target = this.baseUrl + path;
    if (query) {
      const params = new URLSearchParams();
      for (const [key, value] of Object.entries(query)) params.set(key, String(value));
      target += (target.includes('?') ? '&' : '?') + params.toString();
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), Math.max(1, timeout) * 1000);
    let response;
    try {
      response = await this.fetchImpl(target, {
        method: method.toUpperCase(),
        headers: {
          Authorization: `Bearer ${this.token}`,
          'Content-Type': 'application/json',
        },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      });
    } catch (error) {
      throw new ApiError(`Apify ${method.toUpperCase()} transport failure: ${error?.name || 'Error'}`, {
        ambiguous: method.toUpperCase() === 'POST',
      });
    } finally {
      clearTimeout(timer);
    }

    if (!response.ok) {
      const retryAfter = parseRetryAfter(response.headers.get('retry-after'));
      throw new ApiError(`Apify HTTP ${response.status}`, {
        status: response.status,
        retryAfter,
        ambiguous: method.toUpperCase() === 'POST' && response.status >= 500,
      });
    }
    const text = await response.text();
    if (!text) return {};
    try {
      return JSON.parse(text);
    } catch {
      throw new ApiError('Apify returned invalid JSON', { ambiguous: method.toUpperCase() === 'POST' });
    }
  }
}

function parseRetryAfter(value) {
  if (!value) return null;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds;
  const date = Date.parse(value);
  if (Number.isFinite(date)) return Math.max(0, (date - Date.now()) / 1000);
  return null;
}

export function redactText(value) {
  return String(value)
    .replace(/(bearer\s+)[A-Za-z0-9._~+/-]+=*/gi, '$1[REDACTED]')
    .replace(/(sessionid|token|authorization|cookie|password|api[_-]?key)(\s*[:=]\s*["']?)[^\s,;&"']+/gi, '$1$2[REDACTED]');
}

export function safeError(error) {
  return redactText(error instanceof Error ? error.message : String(error)).replace(/\n/g, ' ').slice(0, 300);
}

export function writePrivate(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text, { encoding: 'utf8', mode: 0o600 });
  try {
    chmodSync(path, 0o600);
  } catch {
    /* best effort on platforms without POSIX modes */
  }
}

export class RunJournal {
  constructor(path) {
    this.path = path;
  }

  validate() {
    if (!existsSync(this.path)) return;
    const content = readFileSync(this.path, 'utf8');
    content.split('\n').forEach((line, index) => {
      if (!line) return;
      let row;
      try {
        row = JSON.parse(line);
      } catch {
        throw new Error(`Run journal is corrupt at line ${index + 1}; refusing to continue`);
      }
      if (row === null || typeof row !== 'object' || Array.isArray(row)) {
        throw new Error(`Run journal record at line ${index + 1} is not an object; refusing to continue`);
      }
    });
  }

  append(record) {
    this.validate();
    mkdirSync(dirname(this.path), { recursive: true });
    const line = `${JSON.stringify(record)}\n`;
    const descriptor = openSync(this.path, 'a', 0o600);
    try {
      appendFileSync(descriptor, line, { encoding: 'utf8' });
    } finally {
      closeSync(descriptor);
    }
    try {
      chmodSync(this.path, 0o600);
    } catch {
      /* best effort */
    }
  }

  latest(runId) {
    if (!existsSync(this.path)) return null;
    let found = null;
    readFileSync(this.path, 'utf8').split('\n').forEach((line, index) => {
      if (!line) return;
      let row;
      try {
        row = JSON.parse(line);
      } catch {
        throw new Error(`Run journal is corrupt at line ${index + 1}; refusing resume`);
      }
      if (row === null || typeof row !== 'object' || Array.isArray(row)) {
        throw new Error(`Run journal record at line ${index + 1} is not an object; refusing resume`);
      }
      if (row.runId === runId) found = row;
    });
    return found;
  }
}

/** In-memory journal used by the reusable client; creates no files. */
export class MemoryJournal {
  constructor() {
    this.records = [];
  }

  append(record) {
    this.records.push({ ...record });
  }
}

export function loadResumeRecord(journal, runId, actorId) {
  const record = journal.latest(runId);
  if (!record) throw new Error(`No journal record found for Run ID ${runId}; refusing resume`);
  if (record.runId !== runId || record.actorId !== actorId) {
    throw new Error('Run journal Actor/Run ID does not match the requested resume; refusing resume');
  }
  if (typeof record.sourceUrl !== 'string' || !record.sourceUrl.trim()) {
    throw new Error('Run journal is missing the source URL; refusing resume');
  }
  return record;
}

export function classifyItems(items) {
  if (!Array.isArray(items)) return ['unknown', 'Dataset response is not an item list'];
  if (items.length === 0) return ['empty', 'Dataset contains no items'];
  if (!items.every((item) => item !== null && typeof item === 'object' && !Array.isArray(item))) {
    return ['unknown', 'Dataset contains a non-object item'];
  }
  const errors = items
    .filter((item) => item.errMsg !== undefined && item.errMsg !== null && String(item.errMsg).trim())
    .map((item) => String(item.errMsg).trim());
  if (errors.length) return ['actor_error', redactText(errors.join('; ')).slice(0, 500)];
  if (items.some((item) => typeof item.text === 'string' && item.text.trim())) return ['success', ''];
  if (items.some((item) => Object.prototype.hasOwnProperty.call(item, 'text') && item.text === null)) {
    return ['no_transcript', 'Transcript text is null'];
  }
  if (items.some((item) => !Object.prototype.hasOwnProperty.call(item, 'text'))) {
    return ['no_transcript', 'Transcript text is absent'];
  }
  if (items.some((item) => typeof item.text === 'string' && !item.text.trim())) {
    return ['no_transcript', 'Transcript text is empty'];
  }
  return ['unknown', 'Transcript text has an unsupported type'];
}

export function normalizeSegments(value) {
  if (!Array.isArray(value)) return [];
  const result = [];
  for (const entry of value) {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) continue;
    const { start, end, text } = entry;
    if (typeof start !== 'number' || typeof end !== 'number') continue;
    if (!Number.isFinite(start) || !Number.isFinite(end)) continue;
    if (start < 0 || end < start) continue;
    if (typeof text !== 'string') continue;
    result.push({ start, end, text });
  }
  return result;
}

export const defaultSleep = (seconds) => new Promise((resolve) => setTimeout(resolve, seconds * 1000));

async function retryGet(transport, path, { deadline, maxAttempts, sleep, clock }) {
  let last;
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    if (clock() >= deadline) throw new ApiError('Overall run deadline exceeded');
    try {
      const timeout = Math.max(1, Math.min(30, (deadline - clock()) / 1000));
      return await transport.request('GET', path, undefined, { timeout });
    } catch (error) {
      last = error;
      if (!(error instanceof ApiError)) throw error;
      const retryable = error.status === 429 || (error.status !== null && error.status >= 500 && error.status < 600) || error.status === null;
      if (!retryable || attempt + 1 >= maxAttempts) throw error;
      const delay = error.retryAfter ?? Math.min(2 ** attempt, 8);
      if (clock() + delay * 1000 >= deadline) throw error;
      await sleep(delay);
    }
  }
  throw last;
}

export async function executeInput({
  transport,
  actorId,
  runInput,
  sourceUrl,
  journal,
  deadlineSeconds = 900,
  maxAttempts = 4,
  sleep = defaultSleep,
  clock = Date.now,
  runId = null,
  datasetId = null,
  rowId = null,
  maxTotalChargeUsd = null,
  requireChargeCap = false,
  build = null,
  requireBuild = false,
  datasetLimit = null,
} = {}) {
  const deadline = clock() + deadlineSeconds * 1000;
  const recordId = rowId || randomUUID();
  const record = {
    rowId: recordId,
    sourceUrl,
    actorId,
    runId,
    datasetId,
    stage: 'start',
    status: 'unknown',
    updatedAt: new Date().toISOString(),
  };
  const save = (stage, status = 'unknown', error = null) => {
    record.stage = stage;
    record.status = status;
    record.updatedAt = new Date().toISOString();
    if (error) record.error = safeError(error);
    else delete record.error;
    journal.append({ ...record });
  };

  let chargeCap;
  let selectedBuild;
  try {
    chargeCap = runId === null ? validateRunChargeCap(maxTotalChargeUsd, { required: requireChargeCap }) : null;
    selectedBuild = validateBuild(build, { required: requireBuild });
  } catch (error) {
    return { rowId: recordId, sourceUrl, status: 'transport_error', runId, error: error.message };
  }
  if (datasetLimit !== null && (!Number.isInteger(datasetLimit) || datasetLimit <= 0)) {
    return {
      rowId: recordId,
      sourceUrl,
      status: 'transport_error',
      runId,
      error: 'Dataset page limit must be a positive integer',
    };
  }

  let currentRunId = runId;
  let currentDatasetId = datasetId;

  if (currentRunId === null) {
    try {
      save('creating');
    } catch (error) {
      return { rowId: recordId, sourceUrl, status: 'transport_error', runId: null, error: safeError(error) };
    }
    if (clock() >= deadline) {
      save('create_failed', 'transport_error', 'Overall run deadline exceeded before POST');
      return { rowId: recordId, sourceUrl, status: 'transport_error', runId: null, error: 'Overall run deadline exceeded before POST' };
    }
    const actorPath = `/acts/${actorId.replace('/', '~')}/runs`;
    const query = {};
    if (chargeCap !== null || selectedBuild !== null) query.restartOnError = 'false';
    if (chargeCap !== null) query.maxTotalChargeUsd = chargeCap;
    if (selectedBuild !== null) query.build = selectedBuild;

    let created;
    try {
      created = await transport.request('POST', actorPath, runInput, {
        timeout: Math.max(1, Math.min(30, (deadline - clock()) / 1000)),
        query: Object.keys(query).length ? query : undefined,
      });
    } catch (error) {
      let state = 'transport_error';
      let effective = error;
      if (error instanceof ApiError) {
        if (error.status === 429) {
          state = 'creation_unknown';
          effective = new ApiError('HTTP 429 on Run creation; acceptance semantics are unverified; POST was not retried', { status: 429 });
        } else if (error.ambiguous || error.status === null) {
          state = 'creation_unknown';
        }
      }
      save(state === 'creation_unknown' ? 'creation_unknown' : 'create_failed', state, effective);
      return { rowId: recordId, sourceUrl, status: state, error: safeError(effective), runId: null };
    }

    const payload = created && typeof created === 'object' && created.data ? created.data : created;
    currentRunId = payload?.id ?? null;
    currentDatasetId = payload?.defaultDatasetId ?? null;
    if (!currentRunId) {
      save('creation_unknown', 'creation_unknown', 'POST response did not include a run ID');
      return { rowId: recordId, sourceUrl, status: 'creation_unknown', runId: null, error: 'POST response did not include a run ID' };
    }
    record.runId = currentRunId;
    record.datasetId = currentDatasetId;
    save('run_created');

    if (selectedBuild !== null) {
      let buildNumber = payload?.buildNumber ?? null;
      let buildId = payload?.buildId ?? null;
      let verification = 'response';
      if (!buildNumber || !buildId) {
        try {
          const verified = await retryGet(transport, `/actor-runs/${currentRunId}`, { deadline, maxAttempts, sleep, clock });
          const run = verified?.data ?? verified;
          buildNumber = buildNumber || run?.buildNumber || null;
          buildId = buildId || run?.buildId || null;
          verification = 'run_get';
        } catch (error) {
          record.buildNumber = buildNumber;
          record.buildId = buildId;
          record.buildVerification = 'failed';
          save('build_mismatch', 'transport_error', `Could not verify actual Build identity: ${safeError(error)}`);
          return {
            rowId: recordId,
            sourceUrl,
            runId: currentRunId,
            datasetId: currentDatasetId,
            status: 'transport_error',
            error: 'Could not verify actual Build identity',
            stage: 'build_mismatch',
            buildNumber,
            buildId,
            buildVerification: 'failed',
          };
        }
      }
      const mismatch =
        !buildNumber ||
        !buildId ||
        String(buildNumber) !== selectedBuild ||
        (selectedBuild === APPROVED_BUILD_NUMBER && buildId !== APPROVED_BUILD_ID);
      if (mismatch) {
        record.buildNumber = buildNumber;
        record.buildId = buildId;
        record.buildVerification = buildNumber || buildId ? 'mismatch' : 'missing';
        save('build_mismatch', 'transport_error', 'Actual Build identity is missing or does not match the requested Build');
        return {
          rowId: recordId,
          sourceUrl,
          runId: currentRunId,
          datasetId: currentDatasetId,
          status: 'transport_error',
          error: 'Actual Build identity is missing or does not match the requested Build',
          stage: 'build_mismatch',
          buildNumber,
          buildId,
          buildVerification: record.buildVerification,
        };
      }
      record.buildNumber = String(buildNumber);
      record.buildId = buildId;
      record.buildVerification = verification;
      save('build_verified');
    }
  } else {
    record.runId = currentRunId;
    record.datasetId = currentDatasetId;
    save('resuming');
  }

  try {
    let status = null;
    while (true) {
      if (clock() >= deadline) throw new ApiError('Overall run deadline exceeded');
      const response = await retryGet(transport, `/actor-runs/${currentRunId}`, { deadline, maxAttempts, sleep, clock });
      const run = response?.data ?? response;
      if (selectedBuild !== null && !record.buildVerification) {
        const buildNumber = run?.buildNumber ?? null;
        const buildId = run?.buildId ?? null;
        const mismatch =
          String(buildNumber) !== selectedBuild ||
          !buildId ||
          (selectedBuild === APPROVED_BUILD_NUMBER && buildId !== APPROVED_BUILD_ID);
        if (mismatch) {
          record.buildNumber = buildNumber;
          record.buildId = buildId;
          record.buildVerification = buildNumber || buildId ? 'mismatch' : 'missing';
          save('build_mismatch', 'transport_error', 'Run Build identity is missing or does not match the requested Build');
          return {
            rowId: recordId,
            sourceUrl,
            runId: currentRunId,
            datasetId: currentDatasetId,
            status: 'transport_error',
            error: 'Run Build identity is missing or does not match the requested Build',
            stage: 'build_mismatch',
            buildNumber,
            buildId,
            buildVerification: record.buildVerification,
          };
        }
        record.buildNumber = String(buildNumber);
        record.buildId = buildId;
        record.buildVerification = 'run_get';
        save('build_verified');
      }
      status = run?.status ?? null;
      if (TERMINAL.has(status)) {
        if (status !== 'SUCCEEDED') {
          save('run_finished', 'run_failed', `Run ended with ${status}`);
          return {
            rowId: recordId,
            sourceUrl,
            runId: currentRunId,
            datasetId: currentDatasetId,
            status: 'run_failed',
            error: `Run ended with ${status}`,
          };
        }
        const runDatasetId = run?.defaultDatasetId ?? null;
        if (currentDatasetId && runDatasetId && currentDatasetId !== runDatasetId) {
          throw new ApiError('Journal Dataset ID does not match the Run response');
        }
        currentDatasetId = currentDatasetId || runDatasetId;
        record.datasetId = currentDatasetId;
        save('run_finished');
        break;
      }
      await sleep(Math.min(2, Math.max(0, (deadline - clock()) / 1000)));
    }

    if (!currentDatasetId) {
      save('recoverable', 'transport_error', 'Run succeeded without a dataset ID');
      return {
        rowId: recordId,
        sourceUrl,
        runId: currentRunId,
        status: 'transport_error',
        error: 'Run succeeded without a dataset ID',
      };
    }

    let datasetPath = `/datasets/${currentDatasetId}/items`;
    if (datasetLimit !== null) datasetPath += `?limit=${datasetLimit}&offset=0`;
    let items = await retryGet(transport, datasetPath, { deadline, maxAttempts, sleep, clock });
    for (let attempt = 0; attempt < 2; attempt += 1) {
      if (!(Array.isArray(items) && items.length === 0)) break;
      if (clock() + (attempt + 1) * 1000 >= deadline) break;
      await sleep(attempt + 1);
      items = await retryGet(transport, datasetPath, { deadline, maxAttempts, sleep, clock });
    }

    const datasetComplete = !(datasetLimit !== null && Array.isArray(items) && items.length >= datasetLimit);
    let [state, message] = classifyItems(items);
    if (!datasetComplete) {
      state = 'unknown';
      message = `Dataset reached the ${datasetLimit}-item page limit; completeness is unverified`;
    }
    save('complete', state, message || null);
    return {
      rowId: recordId,
      sourceUrl,
      runId: currentRunId,
      datasetId: currentDatasetId,
      status: state,
      error: message,
      rawItems: items,
      datasetLimit,
      datasetComplete,
      buildNumber: record.buildNumber,
      buildId: record.buildId,
      buildVerification: record.buildVerification,
    };
  } catch (error) {
    save('recoverable', 'transport_error', error);
    return {
      rowId: recordId,
      sourceUrl,
      runId: currentRunId,
      datasetId: currentDatasetId,
      status: 'transport_error',
      error: safeError(error),
    };
  }
}

export function summarize(results) {
  const counts = Object.fromEntries(STATUSES.map((status) => [status, 0]));
  for (const result of results) {
    const status = STATUSES.includes(result.status) ? result.status : 'unknown';
    counts[status] += 1;
  }
  counts.processed = Object.values(counts).reduce((sum, value) => sum + value, 0);
  counts.failed = counts.processed - counts.success;
  return counts;
}

const BLOCKED_KEYS = new Set([
  'token',
  'sessionid',
  'cookie',
  'authorization',
  'password',
  'secret',
  'accesstoken',
  'refreshtoken',
  'apikey',
]);

export function sanitize(value) {
  if (Array.isArray(value)) return value.map(sanitize);
  if (value !== null && typeof value === 'object') {
    const result = {};
    for (const [key, item] of Object.entries(value)) {
      const normalized = key.toLowerCase().replace(/[_-]/g, '');
      if (BLOCKED_KEYS.has(normalized) || /(token|secret|password)$/.test(normalized)) continue;
      result[key] = sanitize(item);
    }
    return result;
  }
  if (typeof value === 'string') return redactText(value);
  return value;
}
