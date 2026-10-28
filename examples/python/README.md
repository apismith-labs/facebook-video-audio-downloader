# Python client

Standard library only. Python 3.10+.

## Files

| File | Role |
| --- | --- |
| `client.py` | Actor API: URL validation, `get_transcript()`, pinned Build verification |
| `reliable_actor.py` | Three-stage runner: create once, poll, read the Dataset |
| `media.py` | Resumable download, SHA-256, expiry-safe redaction, ffmpeg remux |
| `layout.py` | Folder naming, sidecar files, manifest |
| `pipeline.py` | One-video pipeline shared by the CLI and the batch runner |
| `download.py` | Single-URL CLI |
| `batch_download.py` | CSV batch CLI (plan-only by default) |
| `cost.py` | Two-event cost model |

## Single URL

```bash
python3 examples/python/download.py 'https://www.facebook.com/watch/?v=1131784255863745'
python3 examples/python/download.py '<url>' --audio --audio-format m4a --out /tmp/videos
python3 examples/python/download.py '<url>' --json --report report.json
```

| Flag | Effect |
| --- | --- |
| `--out DIR` | Output base directory (default `downloads`) |
| `--name NAME` | Override the folder name |
| `--no-video` / `--audio` | Choose which media to fetch |
| `--audio-format raw\|m4a\|mp3` | Audio container (default `raw`) |
| `--force` | Re-download existing files |
| `--resume RUN_ID` | Reuse a Run (GET only, no new charge) |
| `--json` / `--report PATH` | Machine-readable output |
| `--keep-media-urls` | Keep signed URLs in `metadata.json` |
| `--max-total-charge-usd` / `--require-charge-cap` | Spend guard |

## Library use

```python
from examples.python.pipeline import download_one

report, exit_code = download_one(
    "https://www.facebook.com/watch/?v=1131784255863745",
    out_dir="downloads",
    want_audio=True,
)
print(report["outputDir"], report["assets"])
```

`pipeline.download_one` performs no argparse and prints nothing, so it composes in a worker or a
service. Inject `transport=` and `journal=` to test without a network.

## Tests

```bash
cd ../..
APIFY_SKIP_DOTENV=1 python3 -m unittest discover -s tests/python -v
```

61 tests: media resumption against a local HTTP server, layout rules, the full pipeline against a
fake Actor, CSV validation, `.env` discovery, and cost arithmetic. None of them touch the network or
create a Run.
