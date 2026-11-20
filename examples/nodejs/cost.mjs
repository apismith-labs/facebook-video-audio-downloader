/**
 * Cost model for the Facebook Video & Audio Downloader Actor. Prices captured 2026-11-20.
 * A Run is billed per dataset item it writes, including a Run whose item carries `errMsg`.
 */
export const RESULT_USD = 0.005;

export const PRICING_CAPTURED_AT = '2026-11-20';

export function estimateCostUsd({ videos = 1 } = {}) {
  if (!Number.isInteger(videos) || videos < 1) throw new Error('videos must be a positive integer');
  return {
    videos,
    resultUsd: RESULT_USD,
    totalUsd: RESULT_USD * videos,
    pricingCapturedAt: PRICING_CAPTURED_AT,
  };
}

export function formatEstimate(estimate) {
  return (
    `${estimate.videos} video(s) -> $${estimate.totalUsd.toFixed(4)} estimated ` +
    `($${estimate.resultUsd.toFixed(4)} per result)`
  );
}
