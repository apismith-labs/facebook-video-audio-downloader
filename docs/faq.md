# FAQ

### Is the audio file an MP3?

No. Facebook serves the audio track as an **audio-only MP4** (AAC in an MP4 container), and that is
what `audioUrl` points at. `--audio-format raw` (the default) saves it unchanged as `audio.mp4`.
Use `--audio-format m4a` for a lossless remux with an audio extension, or `mp3` to re-encode at
192 kbit/s.

### Why is there no transcript file?

The Actor is a video and audio downloader; its dataset has no `text` or `segments` field. If you
need a transcript, use the sibling `apple_yang/facebook-transcript-scraper-api` Actor.

### Do I need ffmpeg?

Only for `--audio-format m4a` or `mp3`. Everything else, including `raw` audio, is dependency-free.
`brew install ffmpeg` on macOS.

### How long is a download link valid?

Hours, not days, based on how Facebook signs CDN links. The exact TTL of a link from Build 0.0.17
has not been measured. The kit downloads immediately after the Run and never stores the URL, so this
only matters if you keep a URL outside the tool.

### Can I resume a download tomorrow?

Not against the old link — it is probably expired, and a 403 is not retried. Re-run the URL (a new
Run, $0.005) and the new link is downloaded into the same folder. If the old `.part` belongs to a
different link, delete it or pass `--force`.

### Why does the folder name have no title for Chinese videos?

Folder names must be portable across filesystems and shells. The slug is ASCII-only, so a
Chinese-only title contributes nothing and the folder is just `reel-9`. Use `--name` to choose your
own.

### Why does re-running still cost money?

Because it creates a new Actor Run. The media is skipped (no transfer), but resolution is the paid
step ($0.005). Use `--resume <RUN_ID>` to reuse an existing Run without a new charge when you only
need to re-download the files.

### Can I download a private or friends-only video?

No. The Actor works on public videos only; there is no login, cookie, or session support, and this
kit does not attempt to bypass access controls.

### Can I download an entire page or profile?

No. One URL is one video. Batching means a CSV of individual video URLs, one Run each.

### What happens if the same URL is in the CSV twice?

Both rows create a Run, but the second writes into the same folder and finds the media already
present, so it is marked `skipped: true`. You still pay for the second Run.

### Is a 429 on Run creation retried?

No. `creation_unknown` is returned with exit code 10. A second POST could create a second paid Run,
so you reconcile by Run ID instead.

### Does the kit upload anything anywhere?

No. It calls the Apify API and downloads from `fbcdn.net`. Media stays on your machine and is not
sent to Apify beyond the URL you submit.

### Is downloading a Facebook video legal?

It depends on the content, your relationship to it, and your jurisdiction. The kit grants no license
to any video and is not a circumvention tool. Download only what you have the right to download.

### Why is there no Rust client?

This repository ships Python, Node.js, Go, Java, and cURL examples. A Rust client that could not be
compiled and tested in CI would be a liability, so it is deliberately absent.
