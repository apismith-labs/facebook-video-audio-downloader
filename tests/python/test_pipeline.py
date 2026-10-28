"""End-to-end pipeline tests with a fake Actor and a local media server (no network)."""
from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "examples" / "python"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import pipeline  # noqa: E402
import reliable_actor  # noqa: E402
from offline_support import FakeTransport, LocalMediaServer  # noqa: E402

VIDEO_BYTES = b"\x00\x00\x00\x18ftypmp42" + b"v" * 5000
AUDIO_BYTES = b"\x00\x00\x00\x18ftypmp42" + b"a" * 2000

BUILD_NUMBER = reliable_actor.APPROVED_BUILD_NUMBER
BUILD_ID = reliable_actor.APPROVED_BUILD_ID


def make_item(video_url: str, audio_url: str, *, text: str | None = "hello world", err: str | None = None):
    return {
        "url": "https://www.facebook.com/reel/9/",
        "title": "Sunset Timelapse",
        "videoUrl": video_url,
        "audioUrl": audio_url,
        "createTime": 1791609487,
        "playCount": 10,
        "commentCount": 1,
        "reactionCount": 33,
        "likesCount": 33,
        "shareCount": 2,
        "duration": 42.261,
        "nickname": "ttvnews",
        "avatarUri": "https://scontent.xx.fbcdn.net/v/avatar.jpg?oh=secret",
        "text": text,
        "segments": [
            {"start": 0, "end": 1.5, "text": "hello"},
            {"start": 1.5, "end": 3.0, "text": "world"},
        ] if text else [],
        "errMsg": err,
        "timestamp": "2026-10-28T00:00:00.000Z",
    }


def run_responses(item: dict, run_id: str = "RUN1", dataset_id: str = "DS1"):
    created = {
        "data": {
            "id": run_id,
            "defaultDatasetId": dataset_id,
            "buildNumber": BUILD_NUMBER,
            "buildId": BUILD_ID,
        }
    }
    finished = {
        "data": {
            "status": "SUCCEEDED",
            "defaultDatasetId": dataset_id,
            "buildNumber": BUILD_NUMBER,
            "buildId": BUILD_ID,
        }
    }
    return [created, finished, [item]]


class PipelineTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.out = Path(self._tmp.name)
        self._env = os.environ.get("APIFY_TOKEN")
        os.environ["APIFY_TOKEN"] = "offline-test-token"
        os.environ.pop("APIFY_MAX_TOTAL_CHARGE_USD", None)

    def tearDown(self):
        self._tmp.cleanup()
        if self._env is None:
            os.environ.pop("APIFY_TOKEN", None)
        else:
            os.environ["APIFY_TOKEN"] = self._env

    def test_downloads_video_transcript_and_metadata(self):
        with LocalMediaServer(VIDEO_BYTES) as video, LocalMediaServer(AUDIO_BYTES) as audio:
            transport = FakeTransport(run_responses(make_item(video.url, audio.url)))
            report, code = pipeline.download_one(
                "https://www.facebook.com/reel/9/", out_dir=self.out, transport=transport
            )

        self.assertEqual(code, 0, report.get("error"))
        folder = Path(report["outputDir"])
        self.assertEqual(folder.name, "reel-9-sunset-timelapse")
        self.assertEqual((folder / "video.mp4").read_bytes(), VIDEO_BYTES)
        self.assertEqual((folder / "transcript.txt").read_text(encoding="utf-8"), "hello world\n")
        self.assertEqual(json.loads((folder / "segments.json").read_text()), [
            {"start": 0.0, "end": 1.5, "text": "hello"},
            {"start": 1.5, "end": 3.0, "text": "world"},
        ])
        metadata = json.loads((folder / "metadata.json").read_text())
        self.assertEqual(metadata["videoUrl"], video.url.split("?")[0])
        self.assertEqual(metadata["likesCount"], 33)
        manifest = json.loads((folder / "manifest.json").read_text())
        self.assertEqual(manifest["schemaVersion"], 1)
        self.assertEqual(manifest["runId"], "RUN1")
        self.assertEqual(manifest["duration"], 42.261)
        self.assertEqual(report["segments"], 2)
        # The audio track is only fetched on request.
        self.assertFalse((folder / "audio.mp4").exists())
        self.assertEqual(transport.post_count, 1)

    def test_audio_is_downloaded_on_request(self):
        with LocalMediaServer(VIDEO_BYTES) as video, LocalMediaServer(AUDIO_BYTES) as audio:
            transport = FakeTransport(run_responses(make_item(video.url, audio.url)))
            report, code = pipeline.download_one(
                "https://www.facebook.com/reel/9/", out_dir=self.out,
                want_video=False, want_audio=True, transport=transport,
            )

        self.assertEqual(code, 0)
        folder = Path(report["outputDir"])
        self.assertEqual((folder / "audio.mp4").read_bytes(), AUDIO_BYTES)
        self.assertFalse((folder / "video.mp4").exists())
        kinds = [asset["kind"] for asset in report["assets"]]
        self.assertEqual(kinds, ["audio"])

    def test_second_run_skips_existing_media(self):
        with LocalMediaServer(VIDEO_BYTES) as video, LocalMediaServer(AUDIO_BYTES) as audio:
            item = make_item(video.url, audio.url)
            first, _ = pipeline.download_one(
                "https://www.facebook.com/reel/9/", out_dir=self.out,
                transport=FakeTransport(run_responses(item)),
            )
            second, code = pipeline.download_one(
                "https://www.facebook.com/reel/9/", out_dir=self.out,
                transport=FakeTransport(run_responses(item)),
            )

        self.assertEqual(code, 0)
        self.assertEqual(first["outputDir"], second["outputDir"])
        self.assertTrue(second["assets"][0]["skipped"])

    def test_missing_transcript_still_downloads_media(self):
        with LocalMediaServer(VIDEO_BYTES) as video, LocalMediaServer(AUDIO_BYTES) as audio:
            transport = FakeTransport(run_responses(make_item(video.url, audio.url, text=None)))
            report, code = pipeline.download_one(
                "https://www.facebook.com/reel/9/", out_dir=self.out, transport=transport
            )

        self.assertEqual(code, 0)
        self.assertEqual(report["status"], "no_transcript")
        self.assertEqual(report["segments"], 0)
        folder = Path(report["outputDir"])
        self.assertTrue((folder / "video.mp4").is_file())
        self.assertEqual((folder / "transcript.txt").read_text(encoding="utf-8"), "")

    def test_actor_error_writes_metadata_but_no_media(self):
        with LocalMediaServer(VIDEO_BYTES) as video, LocalMediaServer(AUDIO_BYTES) as audio:
            item = make_item(video.url, audio.url, text=None, err="no audio url found")
            transport = FakeTransport(run_responses(item))
            report, code = pipeline.download_one(
                "https://www.facebook.com/reel/9/", out_dir=self.out, transport=transport
            )

        self.assertEqual(code, 4)
        self.assertEqual(report["status"], "actor_error")
        folder = Path(report["outputDir"])
        self.assertTrue((folder / "metadata.json").is_file())
        self.assertFalse((folder / "video.mp4").exists())
        self.assertEqual(transport.post_count, 1)

    def test_missing_media_url_reports_download_failure(self):
        item = make_item("", "")
        transport = FakeTransport(run_responses(item))
        report, code = pipeline.download_one(
            "https://www.facebook.com/reel/9/", out_dir=self.out, transport=transport
        )
        self.assertEqual(code, pipeline.DOWNLOAD_FAILED_EXIT)
        self.assertIn("no media URL", report["error"])

    def test_unreachable_media_url_reports_download_failure(self):
        item = make_item("http://127.0.0.1:9/nothing.mp4", "http://127.0.0.1:9/nothing.mp4")
        transport = FakeTransport(run_responses(item))
        report, code = pipeline.download_one(
            "https://www.facebook.com/reel/9/", out_dir=self.out, transport=transport
        )
        self.assertEqual(code, pipeline.DOWNLOAD_FAILED_EXIT)
        self.assertIn("download failed", report["error"])
        # The transcript is still written; only the media step failed.
        folder = Path(report["outputDir"])
        self.assertTrue((folder / "transcript.txt").is_file())

    def test_force_rederives_the_converted_container(self):
        calls: list[str] = []

        def fake_transcode(source, target, audio_format):
            calls.append(f"{Path(source).name}->{Path(target).name}")
            Path(target).write_bytes(b"\x00\x00\x00\x18ftypM4A " + b"m" * 100)
            return Path(target)

        with LocalMediaServer(VIDEO_BYTES) as video, LocalMediaServer(AUDIO_BYTES) as audio:
            item = make_item(video.url, audio.url)
            with mock.patch.object(pipeline, "transcode_audio", side_effect=fake_transcode):
                first, _ = pipeline.download_one(
                    "https://www.facebook.com/reel/9/", out_dir=self.out,
                    want_video=False, want_audio=True, audio_format="m4a",
                    transport=FakeTransport(run_responses(item)),
                )
                self.assertEqual(calls, ["audio.mp4->audio.m4a"])

                # A plain rerun keeps the existing container and still reports it.
                second, _ = pipeline.download_one(
                    "https://www.facebook.com/reel/9/", out_dir=self.out,
                    want_video=False, want_audio=True, audio_format="m4a",
                    transport=FakeTransport(run_responses(item)),
                )
                self.assertEqual(len(calls), 1)
                self.assertEqual(
                    [asset["kind"] for asset in second["assets"]], ["audio", "audio-converted"]
                )
                # The container was reused, so the report must say so.
                self.assertTrue(second["assets"][1]["skipped"])

                # --force re-downloads audio.mp4, so audio.m4a must be re-derived from it.
                third, code = pipeline.download_one(
                    "https://www.facebook.com/reel/9/", out_dir=self.out,
                    want_video=False, want_audio=True, audio_format="m4a", force=True,
                    transport=FakeTransport(run_responses(item)),
                )

        self.assertEqual(code, 0, third.get("error"))
        self.assertEqual(len(calls), 2)
        self.assertFalse(third["assets"][0]["skipped"])
        self.assertFalse(third["assets"][1]["skipped"], "a forced rebuild is not a skip")
        self.assertEqual(
            [asset["kind"] for asset in third["assets"]], ["audio", "audio-converted"]
        )

    def test_bad_url_never_creates_a_run(self):
        transport = FakeTransport([])
        report, code = pipeline.download_one("https://example.com/nope", out_dir=self.out, transport=transport)
        self.assertEqual(code, 2)
        self.assertEqual(report["status"], "config_error")
        self.assertEqual(transport.post_count, 0)


if __name__ == "__main__":
    unittest.main()
