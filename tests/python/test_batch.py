"""Tests for CSV validation, cost estimation, and result counting."""
from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "examples" / "python"))

import batch_download  # noqa: E402
from client import ConfigurationError  # noqa: E402


class CsvTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self._tmp.name)

    def tearDown(self):
        self._tmp.cleanup()

    def write(self, text: str, name: str = "videos.csv") -> Path:
        path = self.dir / name
        path.write_text(text, encoding="utf-8")
        return path

    def test_parses_rows_and_keeps_line_numbers(self):
        path = self.write(
            "videoUrl,durationSeconds\n"
            "https://www.facebook.com/reel/1/,10\n"
            "\n"
            "https://www.facebook.com/watch/?v=2,90\n"
        )
        rows = batch_download.parse_rows(path)
        self.assertEqual([row.row_id for row in rows], ["2", "4"])
        self.assertEqual([row.url_form for row in rows], ["reel", "watch"])

    def test_accepts_url_column_case_insensitively(self):
        path = self.write("URL\nhttps://www.facebook.com/reel/1/\n")
        self.assertEqual(len(batch_download.parse_rows(path)), 1)

    def test_missing_url_column_is_rejected(self):
        path = self.write("name\nfoo\n")
        with self.assertRaises(ConfigurationError):
            batch_download.parse_rows(path)

    def test_invalid_url_reports_the_line(self):
        path = self.write("videoUrl\nhttps://example.com/reel/1/\n")
        with self.assertRaises(ConfigurationError) as caught:
            batch_download.parse_rows(path)
        self.assertIn("line 2", str(caught.exception))

    def test_negative_duration_is_rejected(self):
        path = self.write("videoUrl,durationSeconds\nhttps://www.facebook.com/reel/1/,-3\n")
        with self.assertRaises(ConfigurationError):
            batch_download.parse_rows(path)

    def test_empty_file_is_rejected(self):
        path = self.write("videoUrl\n\n")
        with self.assertRaises(ConfigurationError):
            batch_download.parse_rows(path)


class EstimateTests(unittest.TestCase):
    def rows(self, *durations):
        return [
            batch_download.CsvRow(str(index + 2), "https://www.facebook.com/reel/1/",
                                  "https://www.facebook.com/reel/1/", "reel", duration)
            for index, duration in enumerate(durations)
        ]

    def test_under_one_minute_is_billed_as_one_minute(self):
        estimate = batch_download.estimate_rows(self.rows(42.261), "FREE")
        self.assertEqual(estimate["billedMinutes"], 1)
        self.assertAlmostEqual(estimate["totalUsd"], 0.0055, places=6)

    def test_unknown_duration_is_a_lower_bound_at_one_minute(self):
        estimate = batch_download.estimate_rows(self.rows(None), "FREE")
        self.assertTrue(estimate["lowerBound"])
        self.assertAlmostEqual(estimate["totalUsd"], 0.0055, places=6)

    def test_invalid_tier_is_rejected(self):
        with self.assertRaises(ConfigurationError):
            batch_download.estimate_rows(self.rows(10), "TITANIUM")


class CountTests(unittest.TestCase):
    def test_counts_include_download_failures(self):
        counts = batch_download.count_results([
            {"status": "success"},
            {"status": "no_transcript"},
            {"status": "download_failed"},
            {"status": "something-new"},
        ])
        self.assertEqual(counts["success"], 1)
        self.assertEqual(counts["no_transcript"], 1)
        self.assertEqual(counts["download_failed"], 1)
        self.assertEqual(counts["unknown"], 1)
        self.assertEqual(counts["processed"], 4)
        # A silent video still downloaded successfully, so it is not a failure.
        self.assertEqual(counts["failed"], 2)


if __name__ == "__main__":
    unittest.main()
