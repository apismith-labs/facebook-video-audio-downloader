# Compatibility and verification status

What has been observed against the live service, and what has not.

## Actor contract

| Item | Value |
| --- | --- |
| Actor | `apple_yang/facebook-transcript-scraper-api` |
| Pinned Build | `0.0.18` (build id `fZIlpYENof2jYqIJc`) |
| Input | One field: `videoUrl` |
| Dataset fields | 17, all declared required |
| Billing | `apify-default-dataset-item` $0.001 + `speech2text` per started minute |

The pinned Build is verified on every Run: the client compares `buildNumber` and `buildId` from the
create response (or a follow-up GET) and refuses to continue on a mismatch, without creating a
replacement Run. See [the audit](audit/actor_audit_20261028.md).

## Accepted URL forms

| Form | Example | Verified live |
| --- | --- | --- |
| Share (reel) | `https://www.facebook.com/share/r/1N1rvxbAse/` | yes — 42.261 s video |
| Watch | `https://www.facebook.com/watch/?v=<id>` | yes |
| Page video | `https://www.facebook.com/ttvnews/videos/<id>/` | yes |
| Reel | `https://www.facebook.com/reel/<id>/` | yes |
| `video.php` | `https://www.facebook.com/video.php?v=<id>` | no — Facebook returned its own error page |
| Bare videos path | `https://www.facebook.com/videos/<id>/` | no — `Not Found` without page context |
| Short link | `https://fb.watch/<code>/` | not tested — codes can only be minted in the apps |

"Verified live" means the Actor returned the same 42.261 s video for that form. The two rejected
forms failed inside Facebook, not in this kit: three attempts each, while the page-scoped variant of
the same video id succeeded in the same minute.

## Download layer

| Behaviour | Verified |
| --- | --- |
| `videoUrl` and `audioUrl` answer 200 without UA/Referer | yes |
| Both URLs are `fbcdn.net`, `Content-Type: video/mp4` | yes |
| `audioUrl` is audio-only MP4 (AAC), not MP3 | yes |
| `Range` resumption and `206` handling | yes, against a local server in the offline suite |
| `416` promotion of a completed `.part` | yes, offline |
| Truncated body detection | yes, offline |
| Signed query redaction | yes, offline |
| Signature lifetime | hours, not days (same-day links worked; exact TTL unverified) |
| Same video via two URL forms | two folders and two Runs — `share/r/<code>` and `watch/?v=<id>` are not deduplicated against each other |
| Transcript stability across Runs | not stable; the ASR output varies (`台`/`臺`, segment boundaries) |

## Language support

| Client | Single URL | Batch | Resume `.part` | ffmpeg remux | Tests |
| --- | --- | --- | --- | --- | --- |
| Python 3.10+ | yes | yes | yes | yes | 61 |
| Node.js 18+ | yes | yes | yes | yes | 34 |
| Go 1.22+ | yes | yes | yes | yes | 62 |
| Java 17+ | yes | yes | yes | yes | 149 assertions |
| cURL | documented example | not implemented | not implemented | no | — |

Rust is intentionally absent: the reference project for the Actor does not ship one either, and
adding a client that cannot be compiled in CI would be worse than not shipping it.

## Cross-client differences

All four clients were run against the same live URL, producing the same folder name, the same byte
counts, and identical SHA-256 digests for `video.mp4` and `audio.mp4`. Three known divergences,
none of which affect the media:

| Difference | Detail |
| --- | --- |
| Accented titles in folder names | Python, Node, and Java fold `Café` to `cafe` via NFKD; Go has no normalisation without a module dependency, so it drops the letter and produces `caf`. ASCII, CJK, and digit-only titles are identical everywhere. |
| Batch estimate wording | Python and Node print the dataset/speech split; Go and Java print the total only. The number is the same. |
| Transcript text | The Actor's ASR output varies between Runs of the same video (`台`/`臺`, different segment boundaries), so `transcript.txt` and `segments.json` are not byte-stable. Media files are. |


## Not verified

- The exact TTL of a signed CDN link.
- Whether `likesCount` and `shareCount` are populated for every item type.
- Real behaviour for private, deleted, region-locked, or age-restricted videos.
- Whether a video with no audio track yields a usable `audioUrl`.
- Whether `fb.watch` short codes resolve inside the Actor.
- Throughput and rate limits at high batch concurrency; the default is 3.
- Whether the Actor ever returns more than one item per Run.
