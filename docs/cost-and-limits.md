# Cost and limits

Only the **Actor Run** costs money. Downloading the media is a CDN transfer that Apify does not
bill through this Actor.

## The two billed events

| Event | Price | Fires when |
| --- | --- | --- |
| `apify-default-dataset-item` | $0.001 per item | A Run writes its dataset item — **including failed Runs** |
| `speech2text` | $0.0045 per started minute (FREE tier) | A transcription is attempted |

Per-minute price by plan tier:

| Tier | USD / minute |
| --- | --- |
| FREE | 0.0045 |
| BRONZE | 0.0035 |
| SILVER | 0.003 |
| GOLD and above | 0.002 |

Any duration under one minute is billed as one minute.

## Worked examples

| Case | Calculation | Cost |
| --- | --- | --- |
| 42.261 s video, FREE | $0.001 + $0.0045 × 1 | **$0.0055** |
| 3-minute video, FREE | $0.001 + $0.0045 × 3 | **$0.0145** |
| Failed Run (`no audio url found`) | $0.001 + no speech event | **$0.001** |
| Re-run of a downloaded URL | one new Run + no transfer | **$0.0055** |
| 100-video batch, ~1 min each, FREE | 100 × $0.0055 | **$0.55** |

Recorded live on 2026-10-28: a 42.261 s video was charged exactly $0.0055, itemised as
$0.001 + $0.0045.

## Estimating before you spend

```bash
python3 examples/python/batch_download.py sample-data/videos.csv
```

```text
Estimate: 3 video(s), 3 billed minute(s), FREE tier -> $0.0165 estimated
```

A row without `durationSeconds` assumes the one-minute floor, so the estimate is a **lower bound**.

## Caps

```bash
export APIFY_MAX_TOTAL_CHARGE_USD=0.10          # per Run
export APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD=1     # refuse to run without a cap
```

The cap is sent as `maxTotalChargeUsd` on the create request and validated **before** the POST. With
$0.10 per Run, a runaway batch cannot cost more than $0.10 × rows.

## Limits that are not about money

| Limit | Value |
| --- | --- |
| Videos per Run | 1 |
| Maximum video length | 5 minutes on the FREE plan |
| Login / private content | not supported |
| Dataset items per Run | expected 1; the client reads up to 100 and flags incompleteness |
| Run timeout | 900 s default, configurable |
| Download retries | 3 attempts, 1/2/4 s backoff |
| Batch concurrency | 3 by default |

## What you are not paying for

- Media transfer from `fbcdn.net` to your machine.
- Disk, CPU, and ffmpeg time on your side.
- Re-runs that skip already-downloaded media: those still create a Run, but the transfer is skipped.

## Cost hygiene

1. Run plan-only mode on a new CSV before adding `--execute`.
2. Keep `APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD=1` in CI and in any shared environment.
3. Treat `creation_unknown` (exit 10) as "check before retry", never as "retry".
4. Remember that an `actor_error` still costs $0.001; a batch of dead URLs is not free.
