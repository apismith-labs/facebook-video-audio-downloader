# Live run record — download layer, 2026-10-28

Every claim below comes from a real billable Run against Build `0.0.18`
(`fZIlpYENof2jYqIJc`). No mocked transport, no recorded fixtures.

Test video: `https://www.facebook.com/share/r/1N1rvxbAse/` — 42.261 s, Traditional Chinese
transcript, 3,365,805-byte video.

## Runs and cost

| Run | UTC start | Client | Purpose | Cost |
| --- | --- | --- | --- | --- |
| `P8okQ1oFYPkHCAOeg` | 07:36:25 | Python | First live download (video + transcript) | $0.0055 |
| `6U7TKaoQJC7UVo00t` | 07:37:19 | Python | Idempotent rerun, m4a remux | $0.0055 |
| `dRh8FfpebDUfhbeD5` | 07:38:23 | Node | Parity check, m4a remux | $0.0055 |
| `dQQI1aRyT4wsFMBNN` | 07:39:59 | Node | `--force` re-download | $0.0055 |
| `vT16D0iiEOhQdc5sj` | 07:43:53 | Python | `--force`, m4a re-derived | $0.0055 |
| `1ToRh1LfmpVx0oQhg` | 07:44:22 | Python | `--force` repeat for a clean report | $0.0055 |
| `y8d0KFgEVdjQPWWZf` | 07:47:01 | Go | Parity check (before the slug fix) | $0.0055 |
| `iey7ggWBbRl7KNu25` | 07:50:39 | Go | Parity check (after the slug fix) | $0.0055 |
| `wXFoYs4hPLUICJLRz` | 07:52:23 | Java | Parity check | $0.0055 |
| `WWKAi5so9drqy0DgI` | 07:56:42 | Python batch | CSV row 1, `success` | $0.0055 |
| `f3Ia7Ke3gQKXqBp60` | 07:56:41 | Python batch | CSV row 2, `actor_error` | $0.0010 |
| `RHPuEQVwz6tRLsgHG` | 08:00:19 | Python | Run from `examples/python`, proving the upward `.env` search | $0.0055 |

A twelfth call reused `RHPuEQVwz6tRLsgHG` through `--resume`, which only issues GETs: it reported all
three assets as `skipped: true` and cost nothing.

Total: **$0.0615** for 12 Runs. A `success` Run always cost $0.0055 ($0.001 dataset item +
$0.0045 speech-to-text); the `actor_error` Run cost $0.0010 because no speech was billed.

Every Run reported `SUCCEEDED` at the Apify level. The row-2 failure was an **item-level** error
(`errMsg: "no audio url found"`), which is why the Run itself still succeeded and why only the
dataset event was charged — the second billing event is speech-to-text, and there was none.

## Media reproducibility

All four clients downloaded the same signed links and produced byte-identical files:

| File | Bytes | SHA-256 (all four clients) |
| --- | --- | --- |
| `video.mp4` | 3,365,805 | `467dc1b263965dcae04fd47e23f3d676623dd7b652515657918cc18a5afa5f30` |
| `audio.mp4` | 254,559 | `b7067ac751ecb79ef15edbb37a3da46b174a6881eb59c51137033cd0239baeb2` |
| `audio.m4a` (remux) | 251,320 | not hashed (ffmpeg output, not a byte copy) |

The same digests also came back from a Run created minutes apart, so the CDN payload is stable for
the lifetime of the signature.

The transcript is **not** stable: the same video returned 13 segments in one Run and 16 in another,
with `台`/`臺` spelled differently. `transcript.txt` and `segments.json` are best-effort captures of
that Run's ASR output, and `docs/compatibility.md` records this.

## What each client proved

| Check | Result |
| --- | --- |
| Folder name from the canonical URL | `share-r-1N1rvxbAse-51531` in all four clients |
| Signed URLs stripped from `metadata.json` / `manifest.json` | yes — `scheme://host/path` only |
| `--audio` fetches an audio-only MP4 | yes, `video/mp4`, AAC |
| `--audio-format m4a` is a stream copy | yes, 251,320 bytes, `ffprobe` reports a single `aac` stream |
| Second run skips existing media | yes, `skipped: true`, no media request issued |
| `--resume <runId>` reuses a Run without a new charge | yes, all three assets reported as skipped |
| `--force` re-derives the converted container | yes, after the fix described below |
| Batch plan mode reads no token | yes, prints the estimate and exits 0 |
| Batch `--execute` writes one JSON report with per-row exit codes | yes |

## Defects found by running it for real

| Defect | Symptom | Fix |
| --- | --- | --- |
| Node listener leak | `MaxListenersExceededWarning` on a 3.2 MB video | Replaced the hand-rolled drain/error race with `stream/promises.pipeline`; regression test streams 6 MB |
| `--force` left a stale `audio.m4a` | The raw track was re-downloaded but the m4a was not, and the report omitted it | Convert when `--force` or the target is missing; the asset is always listed, with `skipped` reflecting whether it was rebuilt |
| Go CLI rejected the documented argument order | `download <url> -audio` exited 2, because `flag` stops at the first positional | `internal/cliargs.Reorder` moves flags first; shared by both Go commands |
| Go `.env` lookup was cwd-only | Running from `examples/go` reported `APIFY_TOKEN is required` while the root `.env` existed | `internal/dotenv` walks up four levels; Python, Node, and Java now do the same |
| Go folder name diverged from the other clients | `share-r-1N1rvxbAse-51-5-31` instead of `…-51531` | Non-ASCII characters no longer act as a word boundary in `Slugify`; pinned by tests in all four clients |
| Go batch could not read the shipped sample CSV | `CSV must have a 'videoUrl' or 'url' column` (UTF-8 BOM) | Header lookup strips the BOM; covered by `cmd/batch-download/main_test.go` |

Each fix ships with a test that fails without it. The full offline suite (Python 61, Node 34,
Go 62, Java 149 assertions) passes with `APIFY_SKIP_DOTENV=1`, so no test can reach a real token.

## Deliberately not verified

- Signed-link TTL: all links were used within a minute of being minted. The kit downloads
  immediately for this reason, and `exit 11` documents the expired-link path.
- Whether `share/r/<code>` and `watch/?v=<id>` for the same video could be deduplicated: they
  produced two folders and two Runs, because the identity comes from the URL form. Merging them
  would need the Actor to expose a canonical video id, which it does not.
- Concurrent batch behaviour above the default of 3 workers.
