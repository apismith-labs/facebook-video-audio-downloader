# Actor audit — 2026-10-28

The contract this kit is built against, and the evidence for each claim.

| Item | Value |
| --- | --- |
| Actor | `apple_yang/facebook-transcript-scraper-api` |
| Audited Build | `0.0.18` |
| Build id | `fZIlpYENof2jYqIJc` |
| Contract file | [`contracts/facebook-transcript-scraper-api-0.0.18.json`](contracts/facebook-transcript-scraper-api-0.0.18.json) |
| Evidence status | `verified_from_build_api_and_live_runs` |

## What was checked, and how

| Claim | Evidence |
| --- | --- |
| Input has exactly one field, `videoUrl` | Build input schema |
| Output has 17 declared-required fields | Build dataset schema |
| Billing is two events | Actor pricing plus a recorded charge of $0.001 + $0.0045 |
| `duration` is seconds, `createTime` is Unix seconds | A live item returned `42.261` and `1791609487` |
| `segments` are `{start, end, text}` in seconds | Same item, 13 segments, `start <= end` |
| A failed Run still writes one item and charges $0.001 | Run with `errMsg: "no audio url found"` |
| `videoUrl`/`audioUrl` are `fbcdn.net` links usable without UA/Referer | `curl -I` returned 200 and `video/mp4` |
| `audioUrl` is audio-only MP4, not MP3 | Content type and container inspection |
| The pinned Build is what actually runs | `buildNumber` + `buildId` compared on every Run |

## What the media URLs look like

| Field | Host | Container | Notes |
| --- | --- | --- | --- |
| `videoUrl` | `fbcdn.net` | MP4 | Signed query, `Range` supported |
| `audioUrl` | `fbcdn.net` | MP4 (audio-only, AAC) | Signed query, `Range` supported |
| `avatarUri` | `fbcdn.net` | JPG | Signed query, uploader avatar |

All three carry an expiring signature. This kit strips the query string before persisting them.

## Retry semantics derived from the audit

- **Create** is never retried: a POST that may have been accepted is `creation_unknown`.
- **Poll** and **Dataset read** are retried up to 4 times; they are idempotent.
- **Media download** is retried 3 times on 5xx/429/network errors, never on 403.

## Known gap that affects downloads

A completed Run can carry `errMsg: "no audio url found"` with `duration: 0`. It is indistinguishable
from a video with no audio track, costs $0.001, and succeeded on an immediate retry in 2 of 11
observed attempts. The kit surfaces it as `actor_error` (exit 4) and recommends one retry.

## Not verified

The nine actor-level items are listed in the contract's `unverified` array. The three
download-specific ones are in `downloadLayer.unverified`. The most consequential:

- Exact signature TTL.
- Behaviour for private, deleted, or region-locked videos.
- Whether `fb.watch` short codes resolve inside the Actor.
- Whether a video with no audio track still returns a usable `audioUrl`.
