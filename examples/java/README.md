# Java client

Java 17+. No dependencies; JSON is handled by a small built-in reader/writer.

## Files

| Path | Role |
| --- | --- |
| `src/main/java/.../TranscriptClient.java` | Actor API: URL validation, `getTranscript`, pinned Build verification |
| `src/main/java/.../ReliableActor.java` | Three-stage runner: create once, poll, read the Dataset |
| `src/main/java/.../Media.java` | Resumable download, SHA-256, redaction, ffmpeg remux |
| `src/main/java/.../Layout.java` | Folder naming, sidecar files, manifest |
| `src/main/java/.../Downloader.java` | `downloadOne` shared by both CLIs |
| `src/main/java/.../Download.java` | Single-URL CLI |
| `src/main/java/.../BatchDownload.java` | CSV batch CLI (plan-only by default) |
| `src/main/java/.../Json.java` | Small handwritten JSON parser and writer |
| `src/test/java/.../OfflineTests.java` | Actor client tests |
| `src/test/java/.../DownloadTests.java` | Media, layout, and pipeline tests |

## Build and run

```bash
cd examples/java
./run_tests.sh                       # compiles to out/ and runs both suites
java -cp out com.apismith.facebookdownload.Download \
  'https://www.facebook.com/watch/?v=1131784255863745'
java -cp out com.apismith.facebookdownload.Download '<url>' --audio --audio-format m4a
```

If `javac` is not on `PATH`, point at a JDK explicitly:

```bash
JAVAC=/path/to/jdk/bin/javac JAVA=/path/to/jdk/bin/java ./run_tests.sh
```

Flags: `--out`, `--name`, `--no-video`, `--audio`, `--audio-format`, `--force`, `--resume`,
`--json`, `--keep-media-urls`, `--max-total-charge-usd`, `--require-charge-cap`, `--quiet`.

## Batch

```bash
java -cp out com.apismith.facebookdownload.BatchDownload sample-data/videos.csv
java -cp out com.apismith.facebookdownload.BatchDownload --execute sample-data/videos.csv
```

## Tests

```bash
cd examples/java && ./run_tests.sh
```

Two runners: `OfflineTests` covers the Actor client (94 assertions), `DownloadTests` covers media,
layout, and the full pipeline (55 assertions). Both use a local JDK `HttpServer` and a fake
transport — no network, no token.
