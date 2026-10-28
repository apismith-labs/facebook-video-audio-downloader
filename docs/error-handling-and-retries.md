# Error handling and retries

Two independent failure domains: the **Actor Run** and the **media download**. They fail
differently and must not be retried the same way.

## Status vocabulary

| Status | Meaning | Media downloaded | Exit |
| --- | --- | --- | --- |
| `success` | Transcript and media available | yes | 0 |
| `no_transcript` | Media available, no usable speech | yes | 0 |
| `actor_error` | Item carries a non-empty `errMsg` | no | 4 |
| `empty` | Dataset has no items | no | 5 |
| `unknown` | Malformed or unsupported response | no | 7 |
| `transport_error` | Polling or Dataset read failed | no | 8 |
| `run_failed` | The Run itself ended unsuccessfully | no | 9 |
| `creation_unknown` | The POST may have been accepted | no | 10 |
| `config_error` | Bad URL, token, cap, or flag | no | 2 |
| `download_failed` | Run succeeded, media transfer failed | partial | 11 |

`no_transcript` is **not** a failure for this kit. The video and audio are the deliverable; an empty
transcript is a fact about the video, not an error.

## Retry rules

### Run creation — never retried

A POST that times out, returns 5xx, or answers 429 is reported as `creation_unknown`. A second POST
could create a second paid Run. Instead:

1. Inspect the error for a Run ID.
2. Reconcile with `GET /v2/actor-runs?desc=true&limit=5`.
3. If a Run exists, resume it with `--resume <RUN_ID>` (GET only, no new charge).

### Polling and Dataset reads — retried

Up to 4 attempts on 429, 5xx, and network errors, honouring `Retry-After` when present, with
exponential backoff capped at 8 s. These calls are idempotent.

### Media download — retried 3 times

Retryable: 408, 425, 429, 500, 502, 503, 504, network drops, truncated bodies.
Not retryable: **403** (expired signature — the same URL cannot work), 404, 410.

Backoff is 1 s, 2 s, 4 s. A `.part` file survives between attempts, so a retry resumes rather than
restarts.

## The intermittent `no audio url found`

Observed on 2026-10-28: 2 of 11 otherwise-valid attempts returned a **completed** Run with one
dataset item carrying:

```json
{ "errMsg": "no audio url found", "duration": 0, "text": null }
```

Re-running the same URL immediately produced a normal result. This is indistinguishable from a
video with no audio track, and a failing Run **still costs $0.001** (dataset item only, no
`speech2text`).

Guidance: retry **once** with a new Run. Do not loop, and do not fan out concurrently — each attempt
is billed.

## What each client does with each error

| Error | Python | Node | Go | Java |
| --- | --- | --- | --- | --- |
| Bad URL | `ConfigurationError` → 2, no POST | same | same | `ConfigurationException` → 2 |
| Creation ambiguous | `CreationUnknownError` → 10 | same | same | `CreationUnknownException` → 10 |
| Build mismatch | `ProtocolError` → 7, no replacement Run | same | same | `ProtocolException` → 7 |
| Transport during poll | `TranscriptTransportError` → 8 | `TransportError` | `TransportError` | `TransportException` |
| Media 403 | `MediaError` status 403 → 11 | same | same | `MediaException` status 403 |

## Logging hygiene

- Error messages are redacted before they reach a file: `Bearer …`, `token=…`, `sessionid=…`.
- Raw HTTP headers are never persisted.
- Signed media URLs are stripped from `metadata.json`, `manifest.json`, and the JSON report.
