"""Tests for the media download layer: resumption, verification, retries, and redaction."""
from __future__ import annotations

import hashlib
import sys
import tempfile
import unittest
import urllib.error
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "examples" / "python"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import media  # noqa: E402
from offline_support import FakeResponse, LocalMediaServer  # noqa: E402

PAYLOAD = bytes(range(256)) * 400  # 102 400 bytes


def http_error(code: int, *, content_range: str | None = None) -> urllib.error.HTTPError:
    import email.message

    headers = email.message.Message()
    if content_range:
        headers["Content-Range"] = content_range
    return urllib.error.HTTPError("https://example.invalid/x", code, "error", headers, None)


class DownloadTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self._tmp.name)
        self.destination = self.dir / "video.mp4"

    def tearDown(self):
        self._tmp.cleanup()

    def test_full_download_verifies_bytes_and_hash(self):
        asset = media.download_media(
            "https://cdn.example/video.mp4",
            self.destination,
            opener=lambda *_a, **_k: FakeResponse(PAYLOAD),
        )
        self.assertEqual(asset.bytes, len(PAYLOAD))
        self.assertEqual(asset.sha256, hashlib.sha256(PAYLOAD).hexdigest())
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertFalse(asset.skipped)
        self.assertFalse(self.destination.with_name("video.mp4.part").exists())

    def test_resume_appends_only_the_missing_range(self):
        part = self.destination.with_name("video.mp4.part")
        part.write_bytes(PAYLOAD[:40000])
        seen = {}

        def opener(request, timeout=None):
            seen["range"] = request.get_header("Range")
            start = int(seen["range"].split("=")[1].rstrip("-"))
            return FakeResponse(
                PAYLOAD[start:],
                status=206,
                content_range=f"bytes {start}-{len(PAYLOAD) - 1}/{len(PAYLOAD)}",
            )

        asset = media.download_media("https://cdn.example/video.mp4", self.destination, opener=opener)
        self.assertEqual(seen["range"], "bytes=40000-")
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertEqual(asset.bytes, len(PAYLOAD))

    def test_server_ignoring_range_restarts_cleanly(self):
        part = self.destination.with_name("video.mp4.part")
        part.write_bytes(PAYLOAD[:40000])

        asset = media.download_media(
            "https://cdn.example/video.mp4",
            self.destination,
            opener=lambda *_a, **_k: FakeResponse(PAYLOAD, status=200),
        )
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertEqual(asset.bytes, len(PAYLOAD))

    def test_completed_part_is_promoted_on_416(self):
        part = self.destination.with_name("video.mp4.part")
        part.write_bytes(PAYLOAD)

        def opener(*_args, **_kwargs):
            raise http_error(416, content_range=f"bytes */{len(PAYLOAD)}")

        asset = media.download_media("https://cdn.example/video.mp4", self.destination, opener=opener)
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertEqual(asset.bytes, len(PAYLOAD))

    def test_truncated_body_is_retried_then_reported(self):
        calls = []

        def opener(*_args, **_kwargs):
            calls.append(1)
            return FakeResponse(PAYLOAD[:100], declared_length=len(PAYLOAD))

        with self.assertRaises(media.MediaError) as caught:
            media.download_media(
                "https://cdn.example/video.mp4",
                self.destination,
                opener=opener,
                max_attempts=2,
                sleep=lambda _s: None,
            )
        self.assertIn("truncated", str(caught.exception))
        self.assertEqual(len(calls), 2)
        self.assertFalse(self.destination.exists())

    def test_forbidden_is_not_retried(self):
        calls = []

        def opener(*_args, **_kwargs):
            calls.append(1)
            raise http_error(403)

        with self.assertRaises(media.MediaError) as caught:
            media.download_media(
                "https://cdn.example/video.mp4",
                self.destination,
                opener=opener,
                max_attempts=3,
                sleep=lambda _s: None,
            )
        self.assertEqual(caught.exception.status, 403)
        self.assertEqual(len(calls), 1)

    def test_transient_failure_is_retried(self):
        calls = []

        def opener(*_args, **_kwargs):
            calls.append(1)
            if len(calls) == 1:
                raise http_error(503)
            return FakeResponse(PAYLOAD)

        asset = media.download_media(
            "https://cdn.example/video.mp4",
            self.destination,
            opener=opener,
            sleep=lambda _s: None,
        )
        self.assertEqual(len(calls), 2)
        self.assertEqual(asset.bytes, len(PAYLOAD))

    def test_existing_verified_file_is_skipped(self):
        self.destination.write_bytes(PAYLOAD)

        def opener(*_args, **_kwargs):
            raise AssertionError("network must not be used for an existing file")

        asset = media.download_media("https://cdn.example/video.mp4", self.destination, opener=opener)
        self.assertTrue(asset.skipped)
        self.assertEqual(asset.sha256, hashlib.sha256(PAYLOAD).hexdigest())

    def test_force_replaces_existing_file(self):
        self.destination.write_bytes(b"stale")
        asset = media.download_media(
            "https://cdn.example/video.mp4",
            self.destination,
            opener=lambda *_a, **_k: FakeResponse(PAYLOAD),
            force=True,
        )
        self.assertFalse(asset.skipped)
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)

    def test_real_http_range_resume(self):
        with LocalMediaServer(PAYLOAD) as server:
            part = self.destination.with_name("video.mp4.part")
            part.write_bytes(PAYLOAD[:5000])
            asset = media.download_media(server.url, self.destination)
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertEqual(asset.bytes, len(PAYLOAD))
        self.assertEqual(asset.content_type, "video/mp4")

    def test_real_http_without_range_support(self):
        with LocalMediaServer(PAYLOAD, support_range=False) as server:
            part = self.destination.with_name("video.mp4.part")
            part.write_bytes(PAYLOAD[:5000])
            media.download_media(server.url, self.destination)
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)

    def test_rejects_non_http_url(self):
        with self.assertRaises(media.MediaError):
            media.download_media("ftp://example.invalid/x", self.destination)


class RedactionTests(unittest.TestCase):
    def test_signed_query_is_removed(self):
        url = "https://scontent.xx.fbcdn.net/v/t39/123/n.mp4?oh=abc&oe=def&sig=ghi"
        self.assertEqual(media.redact_media_url(url), "https://scontent.xx.fbcdn.net/v/t39/123/n.mp4")

    def test_host_is_preserved(self):
        self.assertEqual(media.media_host("https://scontent.xx.fbcdn.net/v/a.mp4?oh=1"), "scontent.xx.fbcdn.net")

    def test_garbage_is_reported_without_echoing_it(self):
        self.assertEqual(media.redact_media_url("not a url"), "[unparseable media url]")
        self.assertEqual(media.redact_media_url(""), "")


class AudioFormatTests(unittest.TestCase):
    def test_raw_keeps_the_mp4(self):
        path, remux = media.audio_target("out/audio.mp4", "raw")
        self.assertEqual(str(path), "out/audio.mp4")
        self.assertFalse(remux)

    def test_m4a_and_mp3_change_the_extension(self):
        self.assertEqual(str(media.audio_target("out/audio.mp4", "m4a")[0]), "out/audio.m4a")
        self.assertEqual(str(media.audio_target("out/audio.mp4", "mp3")[0]), "out/audio.mp3")

    def test_unknown_format_is_rejected(self):
        with self.assertRaises(media.MediaError):
            media.audio_target("out/audio.mp4", "flac")

    def test_m4a_uses_stream_copy(self):
        args = media.build_ffmpeg_args("a.mp4", "a.m4a", "m4a")
        self.assertIn("copy", args)
        self.assertNotIn("libmp3lame", args)

    def test_mp3_re_encodes(self):
        args = media.build_ffmpeg_args("a.mp4", "a.mp3", "mp3")
        self.assertIn("libmp3lame", args)

    def test_transcode_reports_ffmpeg_failure(self):
        class Result:
            returncode = 1
            stderr = "boom\ninvalid data found\n"

        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "audio.mp4"
            source.write_bytes(b"x")
            with self.assertRaises(media.MediaError) as caught:
                media.transcode_audio(source, Path(tmp) / "audio.m4a", "m4a", runner=lambda *a, **k: Result())
        self.assertIn("invalid data found", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
