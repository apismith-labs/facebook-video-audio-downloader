# Facebook Video & Audio Downloader

Download the **video file, the audio track, the transcript, and the metadata** for a public
Facebook video. The page is resolved by the Apify Actor
[`apple_yang/facebook-transcript-scraper-api`](https://apify.com/apple_yang/facebook-transcript-scraper-api);
this repository is the download kit around it — four reference clients, a CSV batch runner, cost
estimation, an audited contract, and offline tests.

It is **not** the Actor source and it hosts no service.

**[Actor on Apify](https://apify.com/apple_yang/facebook-transcript-scraper-api)** ·
**[Pricing](https://apify.com/apple_yang/facebook-transcript-scraper-api/pricing)** ·
**[Quick start](docs/quickstart.md)**

## What you get per video

```
downloads/<form>-<id>-<title-slug>/
├── video.mp4          the video track (default)
├── audio.mp4          audio-only MP4, on request; .m4a/.mp3 after an optional remux
├── transcript.txt     plain text, empty when the video has no speech
├── segments.json      [{ "start": 0.0, "end": 1.68, "text": "..." }, ...] in seconds
├── metadata.json      the Actor item with signed URLs reduced to scheme://host/path
└── manifest.json      Run ID, sizes, SHA-256, download time, and redacted media sources
```

Every language client writes this exact layout, so a folder started by one can be resumed or
inspected by another.

## Quick start

```bash
export APIFY_TOKEN=apify_api_...
python3 examples/python/download.py 'https://www.facebook.com/watch/?v=1131784255863745' --audio
```

```text
success: downloads/watch-1131784255863745-51531  segments=13  video.mp4, audio.mp4
```

Node.js, Go, and Java do the same thing:

```bash
node examples/nodejs/download.mjs 'https://www.facebook.com/watch/?v=1131784255863745' --audio
(cd examples/go && go run ./cmd/download 'https://www.facebook.com/watch/?v=1131784255863745' -audio)
(cd examples/java && ./run_tests.sh >/dev/null && java -cp out com.apismith.facebookdownload.Download \
  'https://www.facebook.com/watch/?v=1131784255863745' --audio)
```

## How it works

| Stage | What happens | Cost |
| --- | --- | --- |
| 1. Resolve | One Actor Run per URL, pinned to Build 0.0.18 and verified against `buildId` | $0.0055 for a video under one minute |
| 2. Download | Signed `videoUrl` / `audioUrl` are fetched immediately, resuming with `Range` | free (CDN transfer, not billed by Apify) |
| 3. Write | Text, segments, metadata, and a manifest land next to the media | free |

- **The links expire.** They are signed CDN URLs, valid for hours, not days. Download right after
  the Run; do not store a URL and fetch it tomorrow.
- **A failed Run still costs $0.001** (one dataset item, no `speech2text` event).
- **Re-running is cheap and idempotent.** The same URL reuses its folder and skips files that are
  still the recorded size; only the Actor Run is billed again.
- **A missing transcript is not a download failure.** Silent videos still produce media; the status
  is `no_transcript` and the exit code stays 0.

## Clients

| Client | Single URL | CSV batch | Resume a partial download | ffmpeg remux | Offline tests |
| --- | --- | --- | --- | --- | --- |
| [Python 3.10+](examples/python/README.md) | `download.py` | `batch_download.py` | yes | `--audio-format` | 61 tests |
| [Node.js 18+](examples/nodejs/README.md) | `download.mjs` | `batch-download.mjs` | yes | `--audio-format` | 34 tests |
| [Go 1.22+](examples/go/README.md) | `cmd/download` | `cmd/batch-download` | yes | `-audio-format` | 62 tests |
| [Java 17+](examples/java/README.md) | `Download` | `BatchDownload` | yes | `--audio-format` | 149 assertions |

All four are dependency-free: Python uses the standard library, Node uses built-in `fetch`, Go uses
`net/http`, and Java uses `java.net.http` with a small hand-written JSON reader.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Media downloaded (or already present). `no_transcript` also exits 0 |
| 2 | Configuration error — bad URL, missing token, bad charge cap |
| 4 | The Actor reported an item error (`errMsg`), no media downloaded |
| 5 | The Dataset was empty |
| 7 | Unknown status or a protocol violation |
| 8 | Transport failure while polling or reading the Dataset |
| 9 | The Run itself failed |
| 10 | Run creation outcome unknown — reconcile before retrying, do not POST again |
| 11 | The Run succeeded but a media file could not be downloaded |

## Safety defaults

- **Signed URLs are never written to disk** unless you pass `--keep-media-urls`. `metadata.json`
  and `manifest.json` keep only `scheme://host/path`.
- **One Run per URL, never retried on creation.** A POST that may have been accepted is reported as
  `creation_unknown` with exit code 10 so you can reconcile instead of paying twice.
- **Charge caps are enforced before the POST.** Set `APIFY_MAX_TOTAL_CHARGE_USD` (the examples use
  $0.10) and optionally `APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD=1` to refuse to run without one.
- **Downloaded media stays out of git.** `downloads/` and `sample-output/generated/` are ignored.

## Documentation

| Page | Contents |
| --- | --- |
| [Quick start](docs/quickstart.md) | Token, first download, folder layout |
| [Download behaviour](docs/download-behaviour.md) | Naming, resumption, idempotency, expiry, ffmpeg |
| [Input and output fields](docs/input-output-fields.md) | Accepted URL forms, every Actor field, sidecar schemas |
| [Batch processing](docs/batch-processing-guide.md) | CSV format, plan-only mode, concurrency, result file |
| [Cost and limits](docs/cost-and-limits.md) | The two billed events, worked examples, caps |
| [Error handling](docs/error-handling-and-retries.md) | Status vocabulary, retry rules, the intermittent `no audio url found` |
| [Compatibility](docs/compatibility.md) | What is verified live and what is not |
| [FAQ](docs/faq.md) | Media type, expiry, silent videos, legal use |
| [Actor audit](docs/audit/actor_audit_20261028.md) | The contract this kit is built against |
| [Live run record](docs/audit/live_run_download_20261028.md) | Every real Run, its cost, and the defects running it exposed |

## Scope and limits

- Public videos only. No login, no cookies, no private or friends-only content.
- One URL per Run. There is no bulk mode in the Actor; batching is client-side.
- `audioUrl` is an **audio-only MP4** (DASH), not an MP3. Use `--audio-format m4a` for a lossless
  remux or `mp3` to re-encode.
- Download the media you have the right to download. The kit does not grant any license to the
  content and is not a circumvention tool.

## Tests

Everything runs offline, without a token and without creating a Run:

```bash
APIFY_SKIP_DOTENV=1 python3 -m unittest discover -s tests/python
APIFY_SKIP_DOTENV=1 node --test tests/node/*.test.mjs
(cd examples/go && go test ./...)
(cd examples/java && ./run_tests.sh)
```

`APIFY_SKIP_DOTENV=1` matters: without it a local `.env` could turn a test into a paid Run.

## License

MIT — see [LICENSE](LICENSE).
