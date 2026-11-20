# Batch processing

One CSV row becomes one Actor Run, one media folder, and one line in the result file.

## CSV format

```csv
videoUrl
https://www.facebook.com/watch/?v=1131784255863745
https://www.facebook.com/reel/1612151150942907
```

- The header must contain `videoUrl` or `url` (case-insensitive). Extra columns, such as a
  `durationSeconds` left over from another tool, are ignored.
- Blank lines are skipped. `rowId` is the **CSV line number**, so a duplicated URL stays traceable
  to its input line.
- Every row is validated before the first Run. One bad row aborts the whole batch with exit 2 and a
  line number — no partial spend on a file you have to fix anyway.

## Plan first, pay later

```bash
python3 examples/python/batch_download.py sample-data/videos.csv
```

```text
CSV: sample-data/videos.csv
Rows: 3
Estimate: 3 video(s) -> $0.0150 estimated ($0.0050 per result)
Output: /repo/downloads
Plan only. No Run was created and no token was read.
```

Plan mode validates URLs, computes the estimate, and exits without touching the token. Add
`--execute` to download.

## Concurrency

`--max-concurrency` defaults to 3. Each worker owns one row: Run, download, write. Rows are
independent, so ordering does not matter, but the result array preserves input order.

Three is deliberately conservative: the Actor is billed per Run, and a burst of failures costs real
money. Raise it only after the failure rate is known to be near zero.

## Result file

Written next to the CSV as `facebook-batch-download-<timestamp>.json`, never overwriting an earlier
batch. Mode 0600.

```json
{
  "generatedAt": "2026-11-20T05:27:17Z",
  "actorId": "apple_yang/facebook-video-audio-downloader",
  "build": "0.0.17",
  "source": "/repo/sample-data/videos.csv",
  "outputDir": "/repo/downloads",
  "estimate": { "videos": 3, "resultUsd": 0.005, "totalUsd": 0.015 },
  "counts": { "success": 3, "actor_error": 0, "processed": 3, "failed": 0 },
  "results": [
    {
      "rowId": "2",
      "status": "success",
      "runId": "CSLnsRnS9xsbaxc6J",
      "outputDir": "/repo/downloads/watch-1131784255863745-...",
      "assets": [{ "kind": "video", "bytes": 4382219, "sha256": "…", "skipped": false }],
      "exitCode": 0
    }
  ]
}
```

`counts.failed` is the number of rows whose exit code is non-zero; a row is a success only when its
status is `success` and the media was written or already present.

The batch process itself exits **1** when any row ends with a non-zero `exitCode`, **2** on a
configuration error before the first Run, and **0** only when every row succeeded. The per-row codes
are preserved in `results[].exitCode`, so a CI job can tell an `actor_error` (4) from a
`download_failed` (11).

## Re-running after a partial failure

```bash
python3 examples/python/batch_download.py sample-data/videos.csv --execute --limit 2
```

- Rows whose media already verifies are skipped without a request.
- Rows that failed are retried as new Runs (and billed again).
- Use `--force` to re-download media that is already present.

The Batch runner does not read a previous result file; to resume only the failed rows, filter the
CSV by `rowId` yourself. That keeps the input the single source of truth.

## Idempotency guarantees

| Situation | Result |
| --- | --- |
| Same CSV, second run | Folders reused, media skipped, Runs billed again |
| Two rows, same URL | Second row writes into the same folder and skips the media |
| Two rows, different URLs, same slug | Second row gets `<folder>-2` |
| Process killed mid-download | `.part` survives; the next run resumes it |

## Per-language entry points

| Client | Command |
| --- | --- |
| Python | `python3 examples/python/batch_download.py <csv> --execute` |
| Node.js | `node examples/nodejs/batch-download.mjs <csv> --execute` |
| Go | `(cd examples/go && go run ./cmd/batch-download -execute <csv>)` |
| Java | `java -cp out com.apismith.facebookdownload.BatchDownload --execute <csv>` |
