#!/usr/bin/env python3
"""Tests for the sanitized YouTube boxer authentication preflight."""

from __future__ import annotations

import importlib.util
import json
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RUNNER_PATH = ROOT / "scripts" / "youtube_boxer_stock_e2e.py"
SPEC = importlib.util.spec_from_file_location("youtube_boxer_stock_e2e", RUNNER_PATH)
runner = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(runner)


class YouTubeAuthPreflightTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.cookie_path = Path(self.tempdir.name) / "youtube.cookies.txt"
        self.cookie_path.touch()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    @staticmethod
    def completed(stdout: str, stderr: str = "", returncode: int = 0) -> subprocess.CompletedProcess[str]:
        return subprocess.CompletedProcess(
            args=["yt-dlp"],
            returncode=returncode,
            stdout=stdout,
            stderr=stderr,
        )

    def test_resolver_prefers_canonical_cookie_variable(self) -> None:
        self.assertEqual(
            runner.resolve_youtube_cookies_path(
                {
                    "VELOX_YOUTUBE_COOKIES_FILE": "/secure/youtube.cookies.txt",
                    "YT_COOKIES_PATH": "/legacy/cookies.txt",
                }
            ),
            "/secure/youtube.cookies.txt",
        )

    def test_success_report_is_sanitized_and_checks_duration(self) -> None:
        calls: list[list[str]] = []

        def fake_run(command: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            calls.append(command)
            return self.completed(json.dumps({"id": "probe", "duration": 180.5}))

        report = runner.run_auth_preflight(cookies_path=str(self.cookie_path), runner=fake_run)

        self.assertEqual(report["youtube_auth"], "PASS")
        self.assertEqual(report["floyd_manifest_probe"], "PASS")
        self.assertEqual(report["sugar_ray_manifest_probe"], "PASS")
        self.assertTrue(report["cookie_file_readable"])
        self.assertEqual(len(calls), 2)
        for command in calls:
            self.assertIn("--cookies", command)
            self.assertIn(str(self.cookie_path), command)
        encoded = json.dumps(report)
        self.assertNotIn(str(self.cookie_path), encoded)
        self.assertNotIn("signed", encoded.casefold())
        self.assertNotIn("authorization", encoded.casefold())

    def test_literal_auth_required_sentinel_is_reported(self) -> None:
        def fake_run(command: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            return self.completed("{\"error\": \"AUTH_REQUIRED\"}", returncode=1)

        report = runner.run_auth_preflight(cookies_path=str(self.cookie_path), runner=fake_run)

        self.assertEqual(report["youtube_auth"], "FAIL")
        self.assertTrue(all(probe["auth_required"] for probe in report["probes"]))
        self.assertTrue(all(probe["error_code"] == "AUTH_REQUIRED" for probe in report["probes"]))

    def test_auth_required_is_reported_without_echoing_yt_dlp_output(self) -> None:
        secret_like_output = "Sign in to confirm you're not a bot; Authorization: REDACTED"

        def fake_run(command: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            return self.completed("", stderr=secret_like_output, returncode=1)

        report = runner.run_auth_preflight(cookies_path=str(self.cookie_path), runner=fake_run)

        self.assertEqual(report["youtube_auth"], "FAIL")
        self.assertTrue(all(probe["auth_required"] for probe in report["probes"]))
        self.assertTrue(all(probe["error_code"] == "AUTH_REQUIRED" for probe in report["probes"]))
        self.assertNotIn(secret_like_output, json.dumps(report))
        self.assertNotIn("authorization", json.dumps(report).casefold())

    def test_unavailable_or_short_video_fails_without_auth_required(self) -> None:
        def fake_run(command: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            return self.completed(json.dumps({"id": "probe", "duration": 12.0}))

        report = runner.run_auth_preflight(cookies_path=str(self.cookie_path), runner=fake_run)

        self.assertEqual(report["youtube_auth"], "FAIL")
        self.assertTrue(all(probe["available"] for probe in report["probes"]))
        self.assertTrue(all(not probe["auth_required"] for probe in report["probes"]))
        self.assertTrue(all(probe["error_code"] == "DURATION_TOO_SHORT" for probe in report["probes"]))
        self.assertTrue(all(probe["duration_check"] == "FAIL" for probe in report["probes"]))

    def test_missing_cookie_file_fails_closed_without_running_yt_dlp(self) -> None:
        calls = 0

        def fake_run(command: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            nonlocal calls
            calls += 1
            return self.completed("{}")

        report = runner.run_auth_preflight(
            cookies_path=str(Path(self.tempdir.name) / "missing.cookies.txt"),
            runner=fake_run,
        )

        self.assertEqual(report["youtube_auth"], "FAIL")
        self.assertFalse(report["cookie_file_readable"])
        self.assertEqual(calls, 0)
        self.assertTrue(all(probe["error_code"] == "COOKIE_FILE_UNAVAILABLE" for probe in report["probes"]))


if __name__ == "__main__":
    unittest.main()
