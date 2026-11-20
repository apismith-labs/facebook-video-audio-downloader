# Input and output fields

Two schemas matter: what the Actor accepts and returns, and what this kit writes to disk.

## Actor input

One field. The Actor accepts an array, but this kit always sends a single-element array — one URL
per Run. There is no profile mode, cookie, or session field.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `videoUrls` | string[] | yes | Public Facebook video URLs, `https://` only; the kit sends `[canonical]` |

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

All 15 are declared required.

| Field | Type | Meaning |
| --- | --- | --- |
| `url` | string | The submitted URL, echoed verbatim |
| `title` | string | Video title |
| `videoUrl` | string | Signed MP4 video link on `fbcdn.net` |
| `audioUrl` | string | Signed **audio-only MP4** (AAC) link |
| `createTime` | integer | Upload time, Unix seconds |
| `playCount` | integer | Plays |
| `commentCount` | integer | Comments |
| `reactionCount` | integer | Reactions |
| `likesCount` | integer | Likes |
| `shareCount` | integer | Shares |
| `duration` | number | Length in **seconds** |
| `nickname` | string | Uploader display name |
| `avatarUri` | string | Uploader avatar (signed JPG) |
| `errMsg` | string | Failure message, empty on success |
| `timestamp` | string | Item creation time (ISO 8601) |

The field list and types come from the Build 0.0.17 dataset schema. No live item has been observed
for this Build; the media-URL behaviour described below is inherited from the sibling transcript
Actor and has not been re-verified.

Precedence: an empty Dataset is `empty`; a non-empty `errMsg` is `actor_error`; otherwise an item
with a non-empty `videoUrl` or `audioUrl` is `success`; an item with neither is `unknown`.

## Files this kit writes

| File | Always | Contents |
| --- | --- | --- |
| `video.mp4` | when `wantVideo` | Bytes as served by the CDN, no re-mux |
| `audio.mp4` / `.m4a` / `.mp3` | when `wantAudio` | Audio-only track, container per `--audio-format` |
| `metadata.json` | always | The Actor item, signed URLs reduced to `scheme://host/path` |
| `manifest.json` | after the media step | Provenance, sizes, SHA-256, timing |

### `manifest.json`

| Key | Type | Meaning |
| --- | --- | --- |
| `schemaVersion` | integer | `1` |
| `sourceUrl` | string | URL as supplied by the user |
| `canonicalUrl` | string | URL as submitted to the Actor |
| `urlForm` | string | `watch`, `page_video`, `reel`, `share`, `short_link`, … |
| `status` | string | Final status (`success`, `actor_error`, …) |
| `runId` / `datasetId` | string | Apify identifiers for reconciliation |
| `title` / `nickname` | string | From the item |
| `duration` | number \| null | Seconds |
| `downloadedAt` | string | ISO 8601 UTC |
| `assets` | array | `{kind, path, bytes, sha256, contentType, sourceHost, skipped}` |
| `mediaSources` | array | `{kind, url}` with the query string removed |
| `notes` | array | Reminders about expiry and idempotency |

`assets[].skipped` is `true` when the file already existed and was reused, which is how a re-run
proves it did not re-download anything.

## Report printed by `--json`

| Key | Type | Notes |
| --- | --- | --- |
| `status` | string | Same vocabulary as the manifest |
| `sourceUrl` / `canonicalUrl` / `urlForm` | string \| null | |
| `runId` / `datasetId` | string \| null | |
| `outputDir` | string \| null | Absolute or relative to the working directory |
| `duration` / `title` / `nickname` | number/string \| null | `null`, never `""`, when absent |
| `assets` | array | Same shape as in the manifest |
| `error` | string \| null | Redacted |
