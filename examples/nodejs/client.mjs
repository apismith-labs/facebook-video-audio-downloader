/**
 * Synchronous integration API for the Facebook Transcript Actor.
 *
 * Importing this module reads no credentials, creates no files, and performs no network calls.
 */
import {
  ApiError,
  APPROVED_BUILD_ID,
  APPROVED_BUILD_NUMBER,
  DEFAULT_ACTOR_ID,
  MemoryJournal,
  RestTransport,
  buildRunInput,
  executeInput,
  normalizeSegments,
  safeError,
  validateBuild,
  validateRunChargeCap,
} from './reliable-actor.mjs';

export const DEFAULT_TIMEOUT_SECONDS = 900;
export const DEFAULT_DATASET_LIMIT = 100;

const ALLOWED_HOSTS = new Set([
  'facebook.com',
  'www.facebook.com',
  'm.facebook.com',
  'web.facebook.com',
  'fb.watch',
  'www.fb.watch',
]);

const RESERVED_SEGMENTS = new Set([
  'about',
  'business',
  'events',
  'fundraisers',
  'gaming',
  'groups',
  'help',
  'login',
  'login.php',
  'marketplace',
  'messages',
  'permalink.php',
  'photo',
  'plugins',
  'policies',
  'settings',
  'sharer',
  'stories',
  'story.php',
  'watchparty',
]);

const WATCH = /^\/watch\/?$/i;
const VIDEO_PHP = /^\/video\.php$/i;
const PAGE_VIDEO = /^\/(?<slug>[A-Za-z0-9._-]+)\/videos\/(?<id>\d+)\/?$/i;
const VIDEOS = /^\/videos\/(?<id>\d+)\/?$/i;
const REEL = /^\/reel\/(?<id>\d+)\/?$/i;
const SHARE = /^\/share\/(?<kind>[vr])\/(?<code>[A-Za-z0-9_-]+)\/?$/i;
const SHORT = /^\/(?<code>[A-Za-z0-9_-]+)\/?$/;
const ACTOR_ID = /^[A-Za-z0-9][A-Za-z0-9._~/-]*$/;

export class TranscriptClientError extends Error {}
export class ConfigurationError extends TranscriptClientError {}
export class CreationUnknownError extends TranscriptClientError {}
export class TransportError extends TranscriptClientError {
  constructor(message, { runId = null, datasetId = null } = {}) {
    super(message);
    this.runId = runId;
    this.datasetId = datasetId;
  }
}
export class ProtocolError extends TranscriptClientError {
  constructor(message, { runId = null, datasetId = null } = {}) {
    super(message);
    this.runId = runId;
    this.datasetId = datasetId;
  }
}

function singleV(searchParams) {
  const values = searchParams.getAll('v');
  if (values.length !== 1 || !/^\d+$/.test(values[0])) return null;
  return values[0];
}

/**
 * Validate a public Facebook video URL and return { canonicalUrl, form }.
 * Tracking parameters are dropped; unrecognized forms are rejected rather than guessed at.
 */
export function validateVideoUrl(url) {
  if (typeof url !== 'string' || !url || url !== url.trim()) {
    throw new ConfigurationError('url must be a bare Facebook video URL without surrounding whitespace');
  }
  if (url.length > 2048) throw new ConfigurationError('url is longer than 2048 characters');
  if (url.toLowerCase().startsWith('http://')) {
    throw new ConfigurationError('url must use https://; http links are rejected');
  }
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    throw new ConfigurationError('url must be an absolute https:// Facebook video URL');
  }
  if (parsed.protocol !== 'https:') {
    throw new ConfigurationError('url must be an absolute https:// Facebook video URL');
  }
  if (parsed.username || parsed.password) {
    throw new ConfigurationError('url must not contain user information');
  }
  if (parsed.port && parsed.port !== '443') {
    throw new ConfigurationError('url must not use a non-default port');
  }
  const host = parsed.hostname.toLowerCase().replace(/\.$/, '');
  if (!ALLOWED_HOSTS.has(host)) {
    throw new ConfigurationError(
      'url host must be facebook.com, www.facebook.com, m.facebook.com, web.facebook.com, or fb.watch',
    );
  }
  if (parsed.hash) throw new ConfigurationError('url must not contain a fragment');

  const path = parsed.pathname || '/';

  if (host === 'fb.watch' || host === 'www.fb.watch') {
    const match = SHORT.exec(path);
    if (!match) throw new ConfigurationError('fb.watch URLs must look like https://fb.watch/<code>/');
    return { canonicalUrl: `https://fb.watch/${match.groups.code}/`, form: 'short_link' };
  }

  if (WATCH.test(path)) {
    const videoId = singleV(parsed.searchParams);
    if (videoId === null) {
      throw new ConfigurationError('watch URLs must include exactly one numeric ?v= parameter');
    }
    return { canonicalUrl: `https://www.facebook.com/watch/?v=${videoId}`, form: 'watch' };
  }

  if (VIDEO_PHP.test(path)) {
    const videoId = singleV(parsed.searchParams);
    if (videoId === null) {
      throw new ConfigurationError('video.php URLs must include exactly one numeric ?v= parameter');
    }
    return { canonicalUrl: `https://www.facebook.com/video.php?v=${videoId}`, form: 'video_php' };
  }

  let match = PAGE_VIDEO.exec(path);
  if (match) {
    const { slug, id } = match.groups;
    if (RESERVED_SEGMENTS.has(slug.toLowerCase())) {
      throw new ConfigurationError('url points at a site section, not a video');
    }
    return { canonicalUrl: `https://www.facebook.com/${slug}/videos/${id}/`, form: 'page_video' };
  }

  match = VIDEOS.exec(path);
  if (match) return { canonicalUrl: `https://www.facebook.com/videos/${match.groups.id}/`, form: 'video' };

  match = REEL.exec(path);
  if (match) return { canonicalUrl: `https://www.facebook.com/reel/${match.groups.id}/`, form: 'reel' };

  match = SHARE.exec(path);
  if (match) {
    const kind = match.groups.kind.toLowerCase();
    return { canonicalUrl: `https://www.facebook.com/share/${kind}/${match.groups.code}/`, form: 'share' };
  }

  throw new ConfigurationError(
    'unsupported Facebook URL form; see docs/input-output-fields.md#accepted-url-forms',
  );
}

function selectToken(token) {
  const selected = token ?? process.env.APIFY_TOKEN;
  if (typeof selected !== 'string' || !selected.trim()) {
    throw new ConfigurationError('APIFY_TOKEN is required; pass token or set it in the process environment');
  }
  return selected.trim();
}

function selectActor(actorId) {
  const selected = actorId ?? process.env.APIFY_ACTOR_ID ?? DEFAULT_ACTOR_ID;
  if (typeof selected !== 'string' || !selected.trim() || selected.trim().startsWith('/') || !ACTOR_ID.test(selected.trim())) {
    throw new ConfigurationError('actorId must be a non-empty Apify Actor ID or owner/name');
  }
  return selected.trim();
}

function positiveTimeout(value) {
  if (value === undefined || value === null) return DEFAULT_TIMEOUT_SECONDS;
  const result = Number(value);
  if (!Number.isFinite(result) || result <= 0) {
    throw new ConfigurationError('timeout must be a finite positive number of seconds');
  }
  return result;
}

/**
 * Run one public Facebook video URL through the pinned Actor Build.
 * Passing runId performs a GET-only resume of an existing Run.
 */
export async function getTranscript(url, options = {}) {
  const {
    token,
    actorId,
    build,
    timeout,
    maxTotalChargeUsd,
    requireChargeCap,
    datasetLimit = DEFAULT_DATASET_LIMIT,
    runId = null,
    rowId = null,
    transport,
    journal,
  } = options;

  const { canonicalUrl, form } = validateVideoUrl(url);
  const selectedToken = selectToken(token);
  const selectedActor = selectActor(actorId);

  let selectedBuild;
  let cap;
  let requireCap;
  try {
    selectedBuild = validateBuild(build ?? APPROVED_BUILD_NUMBER, { required: true });
    cap = maxTotalChargeUsd ?? process.env.APIFY_MAX_TOTAL_CHARGE_USD ?? null;
    requireCap = requireChargeCap ?? process.env.APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD === '1';
    validateRunChargeCap(cap, { required: requireCap });
  } catch (error) {
    throw new ConfigurationError(error.message);
  }

  const runTimeout = positiveTimeout(timeout);
  if (!Number.isInteger(datasetLimit) || datasetLimit <= 0) {
    throw new ConfigurationError('datasetLimit must be a positive integer');
  }
  if (runId !== null && (typeof runId !== 'string' || !runId.trim() || runId !== runId.trim())) {
    throw new ConfigurationError('runId must be a non-empty Run ID without whitespace');
  }

  const activeTransport = transport ?? new RestTransport(selectedToken);
  const activeJournal = journal ?? new MemoryJournal();

  let outcome;
  try {
    outcome = await executeInput({
      transport: activeTransport,
      actorId: selectedActor,
      runInput: buildRunInput(canonicalUrl),
      sourceUrl: canonicalUrl,
      journal: activeJournal,
      deadlineSeconds: runTimeout,
      maxTotalChargeUsd: cap,
      requireChargeCap: Boolean(requireCap),
      build: selectedBuild,
      requireBuild: true,
      datasetLimit,
      runId,
      rowId,
    });
  } catch (error) {
    if (error instanceof ApiError) throw new TransportError(error.message);
    throw error;
  }

  if (!outcome || typeof outcome.status !== 'string') throw new ProtocolError('Runner returned a malformed result');
  const status = outcome.status;
  if (status === 'creation_unknown') {
    throw new CreationUnknownError(
      `Run creation outcome is unknown (Run ID unavailable): ${
        outcome.error || 'reconcile before retrying'
      }; do not call getTranscript again blindly`,
    );
  }
  if (status === 'transport_error') {
    if (outcome.stage === 'build_mismatch') {
      throw new ProtocolError(
        `Run was created but its Build could not be verified: ${
          outcome.error || 'Build mismatch'
        }; do not create a replacement Run automatically`,
        { runId: outcome.runId, datasetId: outcome.datasetId },
      );
    }
    let detail = outcome.error || 'Apify transport failed';
    if (outcome.runId) {
      detail += `; Run ${outcome.runId} already exists. Reconcile or resume that Run; do not call getTranscript again to replace it`;
    }
    throw new TransportError(detail, { runId: outcome.runId, datasetId: outcome.datasetId });
  }

  const rawItems = outcome.rawItems ?? [];
  if (!Array.isArray(rawItems) || rawItems.some((item) => item === null || typeof item !== 'object' || Array.isArray(item))) {
    throw new ProtocolError('Runner returned malformed Dataset items', {
      runId: outcome.runId,
      datasetId: outcome.datasetId,
    });
  }

  const base = {
    items: rawItems,
    runId: outcome.runId,
    datasetId: outcome.datasetId,
    sourceUrl: url,
    canonicalUrl,
    urlForm: form,
  };

  if (status === 'run_failed') {
    return {
      ...base,
      status: 'run_failed',
      text: null,
      segments: [],
      error: outcome.error || 'Actor Run failed',
      datasetComplete: false,
      title: null,
      duration: null,
      nickname: null,
      succeeded: false,
    };
  }

  const businessStatuses = new Set(['success', 'actor_error', 'empty', 'no_transcript', 'unknown']);
  if (!businessStatuses.has(status)) {
    throw new ProtocolError(`Runner returned unsupported status: ${JSON.stringify(status)}`, {
      runId: outcome.runId,
      datasetId: outcome.datasetId,
    });
  }

  const transcriptItem = rawItems.find((item) => typeof item.text === 'string' && item.text.trim()) ?? null;
  const text = transcriptItem ? transcriptItem.text : null;
  if (status === 'success' && text === null) {
    throw new ProtocolError('Runner reported success without a non-empty transcript', {
      runId: outcome.runId,
      datasetId: outcome.datasetId,
    });
  }
  const metadata = transcriptItem ?? rawItems[0] ?? {};
  const duration = typeof metadata.duration === 'number' && Number.isFinite(metadata.duration) ? metadata.duration : null;

  return {
    ...base,
    status,
    text,
    segments: transcriptItem ? normalizeSegments(transcriptItem.segments) : [],
    error: outcome.error || null,
    datasetComplete: outcome.datasetComplete ?? null,
    title: typeof metadata.title === 'string' ? metadata.title : null,
    duration,
    nickname: typeof metadata.nickname === 'string' ? metadata.nickname : null,
    succeeded: status === 'success',
  };
}

export { APPROVED_BUILD_ID, APPROVED_BUILD_NUMBER, DEFAULT_ACTOR_ID, safeError };
