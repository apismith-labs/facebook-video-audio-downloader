# Actor audit — 2026-11-20

The contract this kit is built against, and the evidence for each claim.

| Item | Value |
| --- | --- |
| Actor | `apple_yang/facebook-video-audio-downloader` |
| Audited Build | `0.0.17` |
| Build id | `WGTQiWxajo00DTQ5X` |
| Contract file | [`contracts/facebook-video-audio-downloader-0.0.17.json`](contracts/facebook-video-audio-downloader-0.0.17.json) |
| Evidence status | `verified_from_build_api_no_live_run` |

## What was checked, and how

| Claim | Evidence |
| --- | --- |
| Input is one required array, `videoUrls` | Build input schema |
| Output has 15 declared-required fields | Build dataset schema |
| There is no transcript field | The dataset schema has no `text` or `segments` |
| Billing is one event at $0.005 per item | Actor pricing metadata and the Store headline `$5.00 / 1,000 results` |
| The pinned Build is `0.0.17` | Build metadata read from the public API |

## What the media URLs look like

The schema declares `videoUrl`, `audioUrl`, and `avatarUri` as strings. The Actor description calls
them downloadable video and audio links. Their host, container, and signature behaviour have **not**
been re-observed on Build 0.0.17; see [compatibility](../compatibility.md).

Working assumption, inherited from the sibling Actor and treated as untrusted:

| Field | Host | Container | Notes |
| --- | --- | --- | --- |
| `videoUrl` | `fbcdn.net` | MP4 | Signed query, `Range` expected |
| `audioUrl` | `fbcdn.net` | MP4 (audio-only, AAC) | Signed query, `Range` expected |
| `avatarUri` | `fbcdn.net` | JPG | Signed query, uploader avatar |

The kit strips the query string before persisting any of them.

## Retry semantics

- **Create** is never retried: a POST that may have been accepted is `creation_unknown` (exit 10).
- **Poll** and **Dataset read** are retried up to 4 times; they are idempotent.
- **Media download** is retried 3 times on 5xx/429/network errors, never on 403.

## Known gap carried over from the sibling Actor

`errMsg: "no audio url found"` was observed on the sibling transcript Actor, not on this Actor. It
is indistinguishable from a video with no audio track, and a completed Run that wrote the item still
costs $0.005. The kit surfaces it as `actor_error` (exit 4) and recommends one retry.

## Not verified

The eight actor-level items are listed in the contract's `unverified` array. The most consequential:

- No live Run has been executed against Build 0.0.17.
- Exact signature TTL.
- Whether `videoUrl` and `audioUrl` are usable without a browser User-Agent or `Referer`.
- Behaviour for private, deleted, or region-locked videos.
- Whether `fb.watch` short codes resolve inside the Actor.
- Whether a video with no audio track still returns a usable `audioUrl`.
