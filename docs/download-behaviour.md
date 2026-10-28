# Download behaviour

How the kit turns a signed CDN link into a file that you can trust, resume, and re-run.

## 1. The link is a temporary credential

The Actor returns `videoUrl` and `audioUrl` as signed URLs on `fbcdn.net`. The signature is in the
query string (`oh`, `oe`, `sig`, …). Two consequences drive the whole design:

- **Download immediately.** The link is valid for hours. A URL stored yesterday may answer 403
  today, and retrying the same URL will not help.
- **Never persist the URL.** `metadata.json` and `manifest.json` strip the query string unless you
  pass `--keep-media-urls`. A signed URL in a log or a commit is a leaked credential.

Verified on 2026-10-28: both links answered `200` with `Content-Type: video/mp4` **without** a
browser User-Agent or a `Referer`, and both supported `Range`. No cookie is involved.

## 2. Folder naming

```
<out>/<identity>-<title-slug>/
```

The identity is derived from the canonical URL, never from the title:

| URL form | Identity | Example |
| --- | --- | --- |
| `watch/?v=<id>` | `watch-<id>` | `watch-1131784255863745` |
| `video.php?v=<id>` | `video-<id>` | `video-1131784255863745` |
| `/<page>/videos/<id>/` | `page-<slug>-<id>` | `page-ttvnews-1131784255863745` |
| `/videos/<id>/` | `video-<id>` | `video-1131784255863745` |
| `/reel/<id>/` | `reel-<id>` | `reel-1612151150942907` |
| `/share/<kind>/<code>/` | `share-<kind>-<code>` | `share-r-1N1rvxbAse` |
| `fb.watch/<code>/` | `short-<code>` | `short-AbC123` |

The title slug is ASCII-only, lower-case, dash-separated, truncated to 48 characters. A title with
no ASCII characters (for example a Chinese-only title) contributes no suffix, so the folder stays
`reel-9` rather than becoming unreadable.

Why identity first: renaming a video upstream must not fork the folder, and two videos that share a
title must not collide. If a collision does happen (same slug, different canonical URL),
`manifest.json` is consulted and the second download lands in `<folder>-2`.

The identity comes from the URL form, so **the same video reached two ways gets two folders and two
Runs**: `/share/r/1N1rvxbAse/` and `/watch/?v=1131784255863745` produced
`share-r-1N1rvxbAse-51531` and `watch-1131784255863745-51531` for the same 42.261 s clip. The Actor
exposes no canonical video id, so there is nothing to deduplicate on; pick one URL form per video
when you care about the cost.

## 3. Resumption

Transfers go to `<name>.part` and are renamed into place only when complete:

1. If `<name>.part` exists, send `Range: bytes=<size>-`.
2. `206 Partial Content` → append. `200 OK` → the server ignored the range; truncate and restart.
3. `416 Range Not Satisfiable` with a matching total → the transfer had finished before an
   interrupted rename; promote the file instead of failing.
4. Compare the written byte count with `Content-Range`/`Content-Length`. A short body is a
   retryable error, not a success.
5. `os.replace` / `Files.move` / `rename` — atomic on the same filesystem.
6. Compute SHA-256 over the finished file and record it in `manifest.json`.

A `.part` file belongs to one signed link. Do not resume it against a URL minted by a later Run:
the byte ranges would come from a different object. The clients create a new folder per Run only
when the URL differs; for the same URL the folder is reused, and a stale `.part` from an expired
link should be deleted (`--force` does that).

## 4. Idempotency

Re-running the same URL:

- reuses the existing folder (matched by `canonicalUrl` in `manifest.json`);
- skips a file whose size still matches, without any network request;
- rewrites the transcript, segments, metadata, and manifest from the new Run.

That means a batch can be re-run after a partial failure and only pay for the Runs it repeats. Add
`--force` to re-download the media anyway.

`--force` also re-derives a converted audio container (`audio.m4a` / `audio.mp3`) from the freshly
downloaded track. Without it, an existing container is reused, and the `audio-converted` asset is
listed in the report with the size already on disk so the report always matches the folder.

`skipped: true` in the report's `assets` marks a reused file.

## 5. Audio is an MP4, not an MP3

`audioUrl` points at an **audio-only MP4** (Facebook's DASH audio track). It is a valid MP4
container with an AAC stream and no video.

| `--audio-format` | Result | Cost |
| --- | --- | --- |
| `raw` (default) | `audio.mp4` — byte-for-byte what Facebook served | none, no ffmpeg |
| `m4a` | `audio.m4a` — AAC stream copied, no re-encode | ffmpeg required, fast, lossless |
| `mp3` | `audio.mp3` — 192 kbit/s LAME re-encode | ffmpeg required, lossy |

`raw` is the default because it is dependency-free and lossless. Rename it yourself if a downstream
tool insists on an audio extension; the bytes are already an AAC track.

## 6. What is not downloaded

- **No subtitle or SRT file.** `segments.json` carries `{start, end, text}` in seconds; convert it
  to any subtitle format you need.
- **No thumbnails.** `avatarUri` is the uploader's avatar, not a video frame, and it is redacted in
  `metadata.json` like the media URLs.
- **No playlists.** One URL is one video; there is no profile or listing mode.

## 7. Failure modes

| Situation | Behaviour |
| --- | --- |
| Link expired (403) | Exit 11, error names the HTTP status. Re-run for a fresh link |
| Network drop mid-transfer | Up to 3 attempts with 1/2/4 s backoff; the `.part` survives |
| Server ignores `Range` | Restarts from zero automatically |
| Disk full | Exit 11, `.part` left for inspection |
| Transcript missing | Not an error: media downloads, `transcript.txt` is empty |
| Actor error | Exit 4, only `metadata.json` is written for evidence |
| Run creation ambiguous | Exit 10; reconcile by Run ID, never POST again |
