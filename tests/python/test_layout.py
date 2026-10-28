"""Tests for deterministic output layout and sidecar sanitisation."""
from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "examples" / "python"))

import layout  # noqa: E402


class SlugTests(unittest.TestCase):
    def test_ascii_title_becomes_dashed_slug(self):
        self.assertEqual(layout.slugify("A Day at the Beach!"), "a-day-at-the-beach")

    def test_non_ascii_title_degrades_to_empty(self):
        self.assertEqual(layout.slugify("海边的一天"), "")

    def test_mixed_title_keeps_the_ascii_part(self):
        self.assertEqual(layout.slugify("Trip 2026 — 海边"), "trip-2026")

    def test_non_ascii_separators_never_create_a_dash(self):
        # Digits split by CJK punctuation collapse, matching the Go/Node/Java clients. A dash here
        # would fork the output folder name between clients.
        self.assertEqual(layout.slugify("韓國瑜 稱有人想當美國第51州、日本第5大島、中國第31省"), "51531")
        self.assertEqual(layout.slugify("第51州、日本第5大島、中國第31省"), "51531")

    def test_long_title_is_truncated_without_trailing_dash(self):
        self.assertEqual(layout.slugify("a" * 60, limit=10), "a" * 10)
        self.assertEqual(layout.slugify("ab " * 10, limit=5), "ab-ab")


class IdentityTests(unittest.TestCase):
    def test_identities_are_stable_per_url_form(self):
        cases = {
            ("https://www.facebook.com/watch/?v=123", "watch"): "watch-123",
            ("https://www.facebook.com/video.php?v=123", "video_php"): "video-123",
            ("https://www.facebook.com/PageName/videos/123/", "page_video"): "page-pagename-123",
            ("https://www.facebook.com/videos/123/", "video"): "video-123",
            ("https://www.facebook.com/reel/123/", "reel"): "reel-123",
            ("https://www.facebook.com/share/r/AbC-1/", "share"): "share-r-AbC-1",
            ("https://fb.watch/AbC123/", "short_link"): "short-AbC123",
        }
        for (url, form), expected in cases.items():
            self.assertEqual(layout.video_identity(url, form), expected, url)

    def test_folder_name_appends_title_slug(self):
        self.assertEqual(
            layout.folder_name("https://www.facebook.com/reel/9/", "reel", "Sunset Timelapse"),
            "reel-9-sunset-timelapse",
        )


class OutputDirTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.base = Path(self._tmp.name)

    def tearDown(self):
        self._tmp.cleanup()

    def test_new_folder_is_returned_as_is(self):
        self.assertEqual(layout.choose_output_dir(self.base, "reel-1", "https://x/1"), self.base / "reel-1")

    def test_same_url_reuses_the_folder(self):
        target = self.base / "reel-1"
        target.mkdir()
        (target / "manifest.json").write_text(json.dumps({"canonicalUrl": "https://x/1"}), encoding="utf-8")
        self.assertEqual(layout.choose_output_dir(self.base, "reel-1", "https://x/1"), target)

    def test_different_url_gets_a_suffix_instead_of_overwriting(self):
        target = self.base / "reel-1"
        target.mkdir()
        (target / "manifest.json").write_text(json.dumps({"canonicalUrl": "https://x/other"}), encoding="utf-8")
        self.assertEqual(layout.choose_output_dir(self.base, "reel-1", "https://x/1"), self.base / "reel-1-2")

    def test_unreadable_manifest_is_treated_as_ours(self):
        target = self.base / "reel-1"
        target.mkdir()
        (target / "manifest.json").write_text("{not json", encoding="utf-8")
        self.assertEqual(layout.choose_output_dir(self.base, "reel-1", "https://x/1"), target)


class SanitiseTests(unittest.TestCase):
    ITEM = {
        "url": "https://www.facebook.com/reel/9/",
        "title": "T",
        "videoUrl": "https://scontent.xx.fbcdn.net/v/a.mp4?oh=1&oe=2&sig=3",
        "audioUrl": "https://scontent.xx.fbcdn.net/v/b.mp4?oh=1",
        "avatarUri": "https://scontent.xx.fbcdn.net/v/c.jpg?oh=1",
        "text": "hello",
    }

    def test_signed_queries_are_stripped(self):
        cleaned = layout.sanitize_item(self.ITEM)
        self.assertEqual(cleaned["videoUrl"], "https://scontent.xx.fbcdn.net/v/a.mp4")
        self.assertEqual(cleaned["audioUrl"], "https://scontent.xx.fbcdn.net/v/b.mp4")
        self.assertEqual(cleaned["avatarUri"], "https://scontent.xx.fbcdn.net/v/c.jpg")
        self.assertEqual(cleaned["text"], "hello")

    def test_opt_in_keeps_the_urls(self):
        cleaned = layout.sanitize_item(self.ITEM, keep_media_urls=True)
        self.assertIn("sig=3", cleaned["videoUrl"])

    def test_original_item_is_not_mutated(self):
        layout.sanitize_item(self.ITEM)
        self.assertIn("sig=3", self.ITEM["videoUrl"])


class SegmentTests(unittest.TestCase):
    def test_invalid_segments_are_dropped(self):
        segments = layout.transcript_segments([
            {"start": 0, "end": 1.5, "text": "a"},
            {"start": 2, "end": 1, "text": "reversed"},
            {"start": "0", "end": 1, "text": "string start"},
            {"start": True, "end": 1, "text": "bool"},
            {"start": 1, "end": 2, "text": 5},
            "not a dict",
        ])
        self.assertEqual(segments, [{"start": 0.0, "end": 1.5, "text": "a"}])


if __name__ == "__main__":
    unittest.main()
