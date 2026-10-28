"""Cost model for the Facebook Transcript Actor.

Two pay-per-event charges apply to a successful transcription:

* ``apify-default-dataset-item`` — a flat price per dataset item;
* ``speech2text`` — a per-minute transcription price that depends on the Apify plan tier, with
  any duration under one minute billed as one minute.

Prices were captured on 2026-10-28 from the public Actor metadata. They are estimates, not a
quote: an Actor Run can be charged differently if pricing changes or if an event does not fire.
"""
from __future__ import annotations

import math
from decimal import Decimal

DATASET_ITEM_USD = Decimal("0.001")

# Ordered cheapest-last for display; all paid tiers collapse to $0.002 at GOLD and above.
TIER_PRICES_USD_PER_MINUTE = {
    "FREE": Decimal("0.0045"),
    "BRONZE": Decimal("0.0035"),
    "SILVER": Decimal("0.003"),
    "GOLD": Decimal("0.002"),
    "PLATINUM": Decimal("0.002"),
    "DIAMOND": Decimal("0.002"),
}

PRICING_CAPTURED_AT = "2026-10-28"


def billed_minutes(duration_seconds: float) -> int:
    """Return billed minutes: at least one, otherwise rounded up."""
    if isinstance(duration_seconds, bool) or not isinstance(duration_seconds, (int, float)):
        raise ValueError("duration_seconds must be a finite non-negative number")
    if not math.isfinite(float(duration_seconds)) or duration_seconds < 0:
        raise ValueError("duration_seconds must be a finite non-negative number")
    return max(1, math.ceil(float(duration_seconds) / 60.0))


def estimate_cost_usd(
    *,
    videos: int = 1,
    duration_seconds: float | None = None,
    tier: str = "FREE",
) -> dict:
    """Estimate the USD cost of one or more single-video Runs.

    ``duration_seconds=None`` means "unknown"; the estimate then assumes the one-minute billing
    floor per video and is a lower bound, not an expectation.
    """
    if isinstance(videos, bool) or not isinstance(videos, int) or videos < 1:
        raise ValueError("videos must be a positive integer")
    selected_tier = (tier or "FREE").strip().upper()
    if selected_tier not in TIER_PRICES_USD_PER_MINUTE:
        raise ValueError(
            "tier must be one of " + ", ".join(TIER_PRICES_USD_PER_MINUTE)
        )

    minutes_per_video = billed_minutes(duration_seconds if duration_seconds is not None else 0)
    item_cost = DATASET_ITEM_USD * videos
    speech_cost = TIER_PRICES_USD_PER_MINUTE[selected_tier] * minutes_per_video * videos
    total = item_cost + speech_cost
    return {
        "tier": selected_tier,
        "videos": videos,
        "durationSeconds": duration_seconds,
        "billedMinutesPerVideo": minutes_per_video,
        "datasetItemUsd": float(item_cost),
        "speechToTextUsd": float(speech_cost),
        "totalUsd": float(total),
        "lowerBound": duration_seconds is None,
        "pricingCapturedAt": PRICING_CAPTURED_AT,
        "note": (
            "Estimate only. The speech2text event is billed per started minute; whether it fires "
            "for silent videos or failed transcriptions is unverified."
        ),
    }


def format_estimate(estimate: dict) -> str:
    """Render a compact one-line summary for CLI output."""
    duration = estimate["durationSeconds"]
    per_video = "unknown duration, 1-minute floor" if duration is None else f"{duration:g}s per video"
    return (
        f"{estimate['videos']} video(s), {per_video}, {estimate['tier']} tier -> "
        f"${estimate['totalUsd']:.4f} estimated "
        f"(dataset ${estimate['datasetItemUsd']:.4f} + speech ${estimate['speechToTextUsd']:.4f})"
    )
