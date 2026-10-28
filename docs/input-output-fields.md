# Input and output fields

Two schemas matter: what the Actor accepts and returns, and what this kit writes to disk.

## Actor input

Exactly one field. There is no `bulkUrls`, no profile mode, no cookie or session field.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `videoUrl` | string | yes | A public Facebook video URL, `https://` only |

The canonical form is submitted: tracking parameters are dropped, and only `watch` and `video.php`
keep a single numeric `?v=`.

### Accepted URL forms

| Form | Example | Canonical output |
| --- | --- | --- |
| Watch | `https://www.facebook.com/watch/?v=1234567890` | same, `?v=` preserved |
| Page video | `https://www.facebook.com/SomePage/videos/987654321/` | same |
| Videos path | `https://www.facebook.com/videos/1234567890` | `.../videos/1234567890/` |
| Reel | `https://www.facebook.com/reel/1234567890/` | same |
| Legacy entry | `https://www.facebook.com/video.php?v=1234567890` | same |
| Share | `https://www.facebook.com/share/r/AbCdEf123/` | same |
| Short link | `https://fb.watch/AbCdEf123/` | same |

Rejected: `http://`, non-Facebook hosts, fragments, user-info, non-default ports, site sections
(`/groups/`, `/help/`, …), and anything not recognized rather than guessed at.

## Actor Dataset fields

All 17 are declared required.

| Field | Type | Meaning | Live observation |
| --- | --- | --- | --- |
| `url` | string | The submitted URL, echoed verbatim | unchanged |
| `title` | string | Video title | populated |
| `videoUrl` | string | Signed MP4 video link on `fbcdn.net` | 200, `video/mp4` |
| `audioUrl` | string | Signed **audio-only MP4** (AAC) link | 200, `video/mp4` |
| `createTime` | integer | Upload time, Unix seconds | `1791609487` |
| `playCount` | integer | Plays | populated |
| `commentCount` | integer | Comments | populated |
| `reactionCount` | integer | Reactions | populated |
| `likesCount` | integer | Likes | `33` |
| `shareCount` | integer | Shares | `2` |
| `duration` | number | Length in **seconds** | `42.261` |
| `nickname` | string | Uploader display name | populated |
| `avatarUri` | string | Uploader avatar (signed JPG) | populated |
| `text` | string | Full transcript, may be empty | 13 segments' worth |
| `segments` | array | `{start, end, text}` in seconds | `start <= end` |
| `errMsg` | string | Failure message, empty on success | `no audio url found` when it fails |
| `timestamp` | string | Item creation time (ISO 8601) | populated |

Precedence: an empty Dataset is `empty`; a non-empty `errMsg` is `actor_error` even when `text` is
also present; otherwise non-empty `text` is `success`; missing or empty `text` is `no_transcript`.

## Files this kit writes

| File | Always | Contents |
| --- | --- | --- |
| `video.mp4` | when `wantVideo` | Bytes as served by the CDN, no re-mux |
| `audio.mp4` / `.m4a` / `.mp3` | when `wantAudio` | Audio-only track, container per `--audio-format` |
| `transcript.txt` | on a downloadable status | `text` with a trailing newline; empty file when silent |
| `segments.json` | on a downloadable status | `[{ "start": 0.0, "end": 1.68, "text": "…" }]` |
| `metadata.json` | always | The Actor item, signed URLs reduced to `scheme://host/path` |
| `manifest.json` | after the media step | Provenance, sizes, SHA-256, timing |

### `manifest.json`

| Key | Type | Meaning |
| --- | --- | --- |
| `schemaVersion` | integer | `1` |
| `sourceUrl` | string | URL as supplied by the user |
| `canonicalUrl` | string | URL as submitted to the Actor |
| `urlForm` | string | `watch`, `page_video`, `reel`, `share`, `short_link`, … |
| `status` | string | Final status (`success`, `no_transcript`, …) |
| `runId` / `datasetId` | string | Apify identifiers for reconciliation |
| `title` / `nickname` | string | From the item |
| `duration` | number \| null | Seconds |
| `downloadedAt` | string | ISO 8601 UTC |
| `assets` | array | `{kind, path, bytes, sha256, contentType, sourceHost, skipped}` |
| `transcript` | object | `{bytes, segments}` |
| `mediaSources` | array | `{kind, url}` with the query string removed |
| `notes` | array | Reminders about expiry and idempotency |

`assets[].skipped` is `true` when the file already existed and was reused, which is how a re-run
proves it did not re-download anything.

### `segments.json`

```json
[
  { "start": 0.0, "end": 1.68, "text": "Hello everyone." },
  { "start": 1.68, "end": 4.12, "text": "Today we are testing." }
]
```

Segments with a non-numeric bound, a negative start, or `end < start` are dropped rather than
written.

## Report printed by `--json`

| Key | Type | Notes |
| --- | --- | --- |
| `status` | string | Same vocabulary as the manifest |
| `sourceUrl` / `canonicalUrl` / `urlForm` | string \| null | |
| `runId` / `datasetId` | string \| null | |
| `outputDir` | string \| null | Absolute or relative to the working directory |
| `duration` / `title` / `nickname` | number/string \| null | `null`, never `""`, when absent |
| `segments` | integer | Count of usable segments |
| `assets` | array | Same shape as in the manifest |
| `transcript` | object \| null | `{bytes, segments}` |
| `error` | string \| null | Redacted |
