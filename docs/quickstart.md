# Quick start

Get from a Facebook URL to files on disk in three steps.

## 1. Get an Apify token

Create a token at [console.apify.com/account/integrations](https://console.apify.com/account/integrations)
and put it in `.env` next to the repository root:

```bash
cp .env.example .env
# then edit .env
APIFY_TOKEN=apify_api_...
```

`.env` is git-ignored. The clients read only `APIFY_*` keys, never override variables that are
already exported, and skip the file entirely when `APIFY_SKIP_DOTENV=1` is set. All four look for
the nearest `.env` by walking up to four directories from the working directory, so running a
client from `examples/go` still finds the root file.

## 2. Download one video

```bash
python3 examples/python/download.py 'https://www.facebook.com/watch/?v=1131784255863745'
```

```text
  video: 4.2 MB (100%)
success: downloads/watch-1131784255863745-...  segments=13  video.mp4
```

Add the audio track and a converted container:

```bash
python3 examples/python/download.py 'https://www.facebook.com/watch/?v=1131784255863745' \
  --audio --audio-format m4a
```

`m4a` is a stream copy (no quality loss) and needs `ffmpeg` on `PATH`. `raw` keeps Facebook's
audio-only MP4 untouched.

## 3. Inspect what was written

```bash
tree downloads/watch-1131784255863745-*/
```

```
├── video.mp4
├── audio.m4a
├── transcript.txt
├── segments.json
├── metadata.json
└── manifest.json
```

`manifest.json` is the receipt:

```json
{
  "schemaVersion": 1,
  "status": "success",
  "sourceUrl": "https://www.facebook.com/watch/?v=1131784255863745",
  "runId": "nLYG45kAbUi9k3QIw",
  "duration": 42.261,
  "downloadedAt": "2026-10-28T05:21:16Z",
  "assets": [{ "kind": "video", "bytes": 4382219, "sha256": "…" }]
}
```

## Other clients

```bash
# Node.js
node examples/nodejs/download.mjs 'https://www.facebook.com/watch/?v=1131784255863745'

# Go
(cd examples/go && go run ./cmd/download 'https://www.facebook.com/watch/?v=1131784255863745')

# Java
(cd examples/java && ./run_tests.sh >/dev/null)
(cd examples/java && java -cp out com.apismith.facebookdownload.Download \
  'https://www.facebook.com/watch/?v=1131784255863745')
```

All three write the same folder layout and print the same status vocabulary.

## Batch

```bash
python3 examples/python/batch_download.py sample-data/videos.csv            # plan only, free
python3 examples/python/batch_download.py sample-data/videos.csv --execute  # billed per row
```

Plan-only mode validates every row and prints a cost estimate without reading the token or creating
a Run.

## Common flags

| Flag | Effect |
| --- | --- |
| `--out DIR` | Output base directory (default `downloads`) |
| `--audio` | Also download the audio track |
| `--audio-format raw\|m4a\|mp3` | Container for the audio track (default `raw`) |
| `--no-video` | Skip `video.mp4` |
| `--force` | Re-download even when files exist |
| `--resume RUN_ID` | Reuse an existing Run instead of creating a new one |
| `--json` | Print the machine-readable report |
| `--keep-media-urls` | Keep the signed URLs in `metadata.json` (off by default) |

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Exit 2, `APIFY_TOKEN is required` | No token in `.env` or the environment | Set `APIFY_TOKEN` |
| Exit 2, `url must use https://` | `http://` or a share text with extra characters | Pass the bare `https://` URL |
| Exit 4, `no audio url found` | Known intermittent Actor failure | Re-run once with a new Run |
| Exit 11, `media server returned HTTP 403` | The signed link expired before the download | Re-run so a fresh link is minted |
| `ffmpeg is required` | `--audio-format m4a`/`mp3` without ffmpeg | `brew install ffmpeg` or use `raw` |
