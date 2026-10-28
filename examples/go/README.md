# Go client

Go 1.22+. Standard library only.

## Files

| Path | Role |
| --- | --- |
| `downloader/client.go` | Actor API: URL validation, `GetTranscript`, pinned Build verification |
| `downloader/reliable_actor.go` | Three-stage runner: create once, poll, read the Dataset |
| `downloader/media.go` | Resumable download, SHA-256, redaction, ffmpeg remux |
| `downloader/layout.go` | Folder naming, sidecar files, manifest |
| `downloader/pipeline.go` | `DownloadOne` shared by both CLIs |
| `cmd/download` | Single-URL CLI |
| `cmd/batch-download` | CSV batch CLI (plan-only by default) |
| `internal/cliargs` | Lets `download <url> -audio` work: Go's `flag` stops at the first positional |
| `internal/dotenv` | Nearest `.env` lookup, walking up from the working directory |

## Single URL

```bash
cd examples/go
go run ./cmd/download 'https://www.facebook.com/watch/?v=1131784255863745'
go run ./cmd/download '<url>' -audio -audio-format m4a -out /tmp/videos
go run ./cmd/download '<url>' -json
```

Flags use the Go single-dash style: `-out`, `-name`, `-no-video`, `-audio`, `-audio-format`,
`-force`, `-resume`, `-json`, `-keep-media-urls`, `-max-total-charge-usd`, `-require-charge-cap`,
`-quiet`.

## Library use

```go
report, code := downloader.DownloadOne(ctx, url, downloader.DownloadOptions{
    OutDir:    "downloads",
    WantVideo: true,
    WantAudio: true,
    Run:       downloader.Options{Token: os.Getenv("APIFY_TOKEN")},
})
```

`Options.Transport`, `Options.Journal`, `Options.Sleep`, and `MediaOptions.Client` are injection
seams for tests.

## Batch

```bash
cd examples/go
go run ./cmd/batch-download ../../sample-data/videos.csv             # plan only
go run ./cmd/batch-download ../../sample-data/videos.csv -execute    # billed per row
```

## Tests

```bash
cd examples/go && go test ./... -v
```

62 tests: media resumption against `httptest`, layout rules, the end-to-end pipeline against a fake
Actor, CSV parsing (including a BOM-prefixed header), argument reordering, and `.env` discovery.
