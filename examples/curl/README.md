# cURL examples

The Actor is a normal Apify Run, so `curl` is enough to see what the download kit automates.

## 1. Create one Run

```bash
curl -sS -X POST \
  "https://api.apify.com/v2/acts/apple_yang~facebook-transcript-scraper-api/runs?build=0.0.18&maxTotalChargeUsd=0.10" \
  -H "Authorization: Bearer $APIFY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"videoUrl":"https://www.facebook.com/watch/?v=1131784255863745"}'
```

Never retry this call blindly: a second POST can create a second paid Run.

## 2. Poll the Run

```bash
curl -sS "https://api.apify.com/v2/actor-runs/<RUN_ID>" \
  -H "Authorization: Bearer $APIFY_TOKEN" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(d["status"], d["defaultDatasetId"])'
```

Repeat until the status is terminal (`SUCCEEDED`, `FAILED`, `TIMED-OUT`, `ABORTED`).

## 3. Read the Dataset

```bash
curl -sS "https://api.apify.com/v2/datasets/<DATASET_ID>/items?limit=1" \
  -H "Authorization: Bearer $APIFY_TOKEN" \
  | python3 -c '
import json, sys
item = json.load(sys.stdin)[0]
print(item["title"], item["duration"], len(item["segments"]))
print(item["videoUrl"][:60] + "...")   # signed: expires, do not store
'
```

## 4. Download the media

```bash
curl -L --fail --retry 3 -o video.mp4 "<videoUrl>"
curl -L --fail --retry 3 -o audio.mp4 "<audioUrl>"
```

`curl -C -` resumes a partial file with `Range`, which is what the clients do automatically.

## What the kit adds

| `curl` | The kit |
| --- | --- |
| Manual Run creation and polling | `download.py <url>` |
| Manual Dataset parsing | Transcript, segments, metadata, manifest on disk |
| Manual `curl -C -` | Resumable download with verification and idempotency |
| Signed URLs in your shell history | Redacted in every persisted file |
| Easy to double-POST | `creation_unknown` guard, exit 10 |
