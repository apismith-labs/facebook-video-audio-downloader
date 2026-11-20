# Compatibility and verification status

What has been observed against the live service, and what has not.

## Actor contract

| Item | Value |
| --- | --- |
| Actor | `apple_yang/facebook-video-audio-downloader` |
| Pinned Build | `0.0.17` (build id `WGTQiWxajo00DTQ5X`) |
| Input | `videoUrls`: array of strings (the kit sends one element) |
| Dataset fields | 15, all declared required; no `text` or `segments` |
| Billing | `apify-default-dataset-item` at $0.005 per item |

The field list, input schema, build number, and price were read from the public Actor metadata on
2026-11-20. The pinned Build is verified on every Run: the client compares `buildNumber` and
`buildId` from the create response (or a follow-up GET) and refuses to continue on a mismatch,
without creating a replacement Run. See [the audit](audit/actor_audit_20261120.md).

## Accepted URL forms

| Form | Example | Verified on Build 0.0.17 |
| --- | --- | --- |
| Share (reel) | `https://www.facebook.com/share/r/1N1rvxbAse/` | not yet |
| Watch | `https://www.facebook.com/watch/?v=<id>` | not yet |
| Page video | `https://www.facebook.com/ttvnews/videos/<id>/` | not yet |
| Reel | `https://www.facebook.com/reel/<id>/` | not yet |
| `video.php` | `https://www.facebook.com/video.php?v=<id>` | not yet |
| Bare videos path | `https://www.facebook.com/videos/<id>/` | not yet |
| Short link | `https://fb.watch/<code>/` | not yet |

The URL allow-list and canonicalisation were exercised against the live service on the sibling
transcript Actor (2026-10-28); `video.php` and the bare videos path failed inside Facebook there.
They have not been re-run against Build 0.0.17, so treat the table as inherited behaviour rather
than fresh evidence.

## Download layer

| Behaviour | Verified |
| --- | --- |
| `Range` resumption and `206` handling | yes, against a local server in the offline suite |
| `416` promotion of a completed `.part` | yes, offline |
| Truncated body detection | yes, offline |
| Signed query redaction | yes, offline |
| Same video via two URL forms | two folders and two Runs — `share/r/<code>` and `watch/?v=<id>` are not deduplicated against each other |
| `videoUrl` / `audioUrl` answer 200 without UA/Referer | not re-verified on Build 0.0.17 |

## Language support

| Client | Single URL | Batch | Resume `.part` | ffmpeg remux | Tests |
| --- | --- | --- | --- | --- | --- |
| Python 3.10+ | yes | yes | yes | yes | 57 |
| Node.js 18+ | yes | yes | yes | yes | 32 |
| Go 1.22+ | yes | yes | yes | yes | 57 |
| Java 17+ | yes | yes | yes | yes | runner prints count |
| cURL | documented example | not implemented | not implemented | no | — |

Rust is intentionally absent: adding a client that cannot be compiled in CI would be worse than not
shipping it.

## Cross-client differences

All four clients share the folder layout, status vocabulary, and flat cost model. Two known
divergences remain, neither of which affects the media:

| Difference | Detail |
| --- | --- |
| Accented titles in folder names | Python, Node, and Java fold `Café` to `cafe` via NFKD; Go has no normalisation without a module dependency, so it drops the letter and produces `caf`. ASCII, CJK, and digit-only titles are identical everywhere. |
| Batch estimate wording | Python and Node print the per-result and total figures; Go and Java print the total only. The number is the same. |

## Not verified

- Any live Run against Build 0.0.17; this migration is built from the published contract.
- The exact TTL of a signed CDN link.
- Whether `likesCount` and `shareCount` are populated for every item type.
- Real behaviour for private, deleted, region-locked, or age-restricted videos.
- Whether a video with no audio track yields a usable `audioUrl`.
- Whether `fb.watch` short codes resolve inside the Actor.
- Throughput and rate limits at high batch concurrency; the default is 3.
- Whether the Actor ever returns more than one item when `videoUrls` has one element.
