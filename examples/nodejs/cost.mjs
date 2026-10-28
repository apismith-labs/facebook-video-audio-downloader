/**
 * Cost model for the Facebook Transcript Actor. Prices captured 2026-10-28.
 * Estimates only: the speech2text event may not fire for silent or failed videos.
 */
export const DATASET_ITEM_USD = 0.001;

export const TIER_PRICES_USD_PER_MINUTE = {
  FREE: 0.0045,
  BRONZE: 0.0035,
  SILVER: 0.003,
  GOLD: 0.002,
  PLATINUM: 0.002,
  DIAMOND: 0.002,
};

export const PRICING_CAPTURED_AT = '2026-10-28';

export function billedMinutes(durationSeconds) {
  if (typeof durationSeconds !== 'number' || !Number.isFinite(durationSeconds) || durationSeconds < 0) {
    throw new Error('durationSeconds must be a finite non-negative number');
  }
  return Math.max(1, Math.ceil(durationSeconds / 60));
}

export function estimateCostUsd({ videos = 1, durationSeconds = null, tier = 'FREE' } = {}) {
  if (!Number.isInteger(videos) || videos < 1) throw new Error('videos must be a positive integer');
  const selectedTier = String(tier || 'FREE').trim().toUpperCase();
  if (!(selectedTier in TIER_PRICES_USD_PER_MINUTE)) {
    throw new Error(`tier must be one of ${Object.keys(TIER_PRICES_USD_PER_MINUTE).join(', ')}`);
  }
  const minutes = billedMinutes(durationSeconds === null ? 0 : durationSeconds);
  const datasetItemUsd = DATASET_ITEM_USD * videos;
  const speechToTextUsd = TIER_PRICES_USD_PER_MINUTE[selectedTier] * minutes * videos;
  return {
    tier: selectedTier,
    videos,
    durationSeconds,
    billedMinutesPerVideo: minutes,
    datasetItemUsd,
    speechToTextUsd,
    totalUsd: datasetItemUsd + speechToTextUsd,
    lowerBound: durationSeconds === null,
    pricingCapturedAt: PRICING_CAPTURED_AT,
  };
}

export function formatEstimate(estimate) {
  const duration =
    estimate.durationSeconds === null
      ? 'unknown duration, 1-minute floor'
      : `${estimate.durationSeconds}s per video`;
  return (
    `${estimate.videos} video(s), ${duration}, ${estimate.tier} tier -> ` +
    `$${estimate.totalUsd.toFixed(4)} estimated ` +
    `(dataset $${estimate.datasetItemUsd.toFixed(4)} + speech $${estimate.speechToTextUsd.toFixed(4)})`
  );
}
