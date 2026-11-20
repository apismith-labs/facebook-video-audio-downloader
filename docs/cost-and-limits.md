# Cost and limits

Only the **Actor Run** costs money. Downloading the media is a CDN transfer that Apify does not
bill through this Actor.

## The billed event

| Event | Price | Fires when |
| --- | --- | --- |
| `apify-default-dataset-item` | $0.005 per item | A Run writes its dataset item — **including an item that carries `errMsg`** |

There is no `speech2text` event and no per-minute tier: the price is flat per dataset item. The
Store headline, `$5.00 / 1,000 results`, matches this rate.

## Worked examples

| Case | Calculation | Cost |
| --- | --- | --- |
| One resolved video | 1 × $0.005 | **$0.005** |
| Failed Run that wrote an item (`no audio url found`) | 1 × $0.005 | **$0.005** |
| Run that wrote no item (empty Dataset) | 0 × $0.005 | **$0.000** |
| Re-run of a downloaded URL | one new Run | **$0.005** |
| 100-video batch | 100 × $0.005 | **$0.50** |

## Estimating before you spend

```bash
python3 examples/python/batch_download.py sample-data/videos.csv
```

```text
Estimate: 3 video(s) -> $0.0150 estimated ($0.0050 per result)
```

The estimate is exact for the Run charge if every row writes one item. It does not include Apify
platform usage outside this Actor's pay-per-event price.

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
| Videos per Run | 1 in this kit; the Actor's `videoUrls` array accepts more |
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
4. Remember that an `actor_error` still costs $0.005 if the Run wrote an item.
