"""Nearest-.env discovery shared by both Python CLIs."""
from __future__ import annotations

import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "examples" / "python"))

import env  # noqa: E402


class FindDotEnvTests(unittest.TestCase):
    def test_finds_the_file_in_a_parent_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            nested = root / "examples" / "python"
            nested.mkdir(parents=True)
            target = root / ".env"
            target.write_text("APIFY_TOKEN=x\n", encoding="utf-8")
            self.assertEqual(env.find_dotenv(nested), target.resolve())

    def test_prefers_the_nearest_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            nested = root / "examples"
            nested.mkdir()
            (root / ".env").write_text("APIFY_TOKEN=outer\n", encoding="utf-8")
            (nested / ".env").write_text("APIFY_TOKEN=inner\n", encoding="utf-8")
            self.assertEqual(env.find_dotenv(nested), (nested / ".env").resolve())

    def test_returns_none_when_there_is_no_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertIsNone(env.find_dotenv(Path(tmp)))


class LoadEnvFileTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        self.path = self.root / ".env"
        self.path.write_text(
            "# comment\nAPIFY_TOKEN=from-file\nAPIFY_KEEP=ignored\nNOT_APIFY=skipped\n",
            encoding="utf-8",
        )

    def tearDown(self):
        self._tmp.cleanup()

    def test_loads_apify_keys_without_overriding_the_environment(self):
        with mock.patch.dict(os.environ, {"APIFY_KEEP": "keep"}, clear=False):
            # The suite runs with APIFY_SKIP_DOTENV=1 so real tokens never leak in; this test
            # exercises the loader, so the switch has to be off for its duration.
            os.environ.pop("APIFY_SKIP_DOTENV", None)
            os.environ.pop("APIFY_TOKEN", None)
            env.load_env_file(self.path)
            self.assertEqual(os.environ["APIFY_TOKEN"], "from-file")
            self.assertEqual(os.environ["APIFY_KEEP"], "keep")
            self.assertNotIn("NOT_APIFY", os.environ)

    def test_skip_switch_disables_the_file(self):
        with mock.patch.dict(os.environ, {"APIFY_SKIP_DOTENV": "1"}, clear=False):
            os.environ["APIFY_SKIP_DOTENV"] = "1"
            os.environ.pop("APIFY_TOKEN", None)
            self.assertIsNone(env.load_env_file(self.path))
            self.assertNotIn("APIFY_TOKEN", os.environ)

    def test_a_missing_file_is_not_an_error(self):
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("APIFY_SKIP_DOTENV", None)
            self.assertIsNone(env.load_env_file(self.root / "absent.env"))


if __name__ == "__main__":
    unittest.main()
