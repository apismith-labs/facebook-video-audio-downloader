# Node.js client

Node 18+ (uses the built-in `fetch` and `node:test`). No dependencies.

## Files

| File | Role |
| --- | --- |
| `client.mjs` | Actor API: URL validation, `getTranscript()`, pinned Build verification |
| `reliable-actor.mjs` | Three-stage runner: create once, poll, read the Dataset |
| `media.mjs` | Resumable download, SHA-256, redaction, ffmpeg remux |
| `layout.mjs` | Folder naming, sidecar files, manifest |
| `pipeline.mjs` | `downloadOne()` shared by both CLIs |
| `download.mjs` | Single-URL CLI |
| `batch-download.mjs` | CSV batch CLI (plan-only by default) |

## Single URL

```bash
node examples/nodejs/download.mjs 'https://www.facebook.com/watch/?v=1131784255863745'
node examples/nodejs/download.mjs '<url>' --audio --audio-format mp3 --out /tmp/videos
node examples/nodejs/download.mjs '<url>' --json
```

Flags mirror the Python client: `--out`, `--name`, `--no-video`, `--audio`, `--audio-format`,
`--force`, `--resume`, `--json`, `--keep-media-urls`, `--max-total-charge-usd`,
`--require-charge-cap`, `--quiet`.

## Library use

```javascript
import { downloadOne } from './examples/nodejs/pipeline.mjs';

const { report, exitCode } = await downloadOne('https://www.facebook.com/watch/?v=1131784255863745', {
  outDir: 'downloads',
  wantAudio: true,
});
console.log(exitCode, report.outputDir, report.assets);
```

`transport` and `journal` are injectable, so tests never reach the network.

## Batch

```bash
node examples/nodejs/batch-download.mjs sample-data/videos.csv            # plan only
node examples/nodejs/batch-download.mjs sample-data/videos.csv --execute  # billed per row
```

## Tests

```bash
cd ../..
APIFY_SKIP_DOTENV=1 node --test tests/node/*.test.mjs
```

34 tests over four files: media resumption against a local HTTP server, layout rules, the full
pipeline against a fake Actor, and `.env` discovery. `node --test` does not accept a directory in
Node 22, hence the glob.
