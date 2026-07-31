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

    def test_profiles_define_canary_and_full_contracts(self) -> None:
        canary = runner.PROFILES["canary"]
        full = runner.PROFILES["full"]

        self.assertEqual((canary.videos, canary.clips_per_video, canary.total_clips), (1, 3, 3))
        self.assertEqual((full.videos, full.clips_per_video, full.total_clips), (20, 15, 300))
        self.assertEqual(canary.per_source_min_ms, 11_400)
        self.assertEqual(canary.per_source_max_ms, 12_600)
        self.assertEqual(full.total_min_ms, 1_140_000)
        self.assertEqual(full.total_max_ms, 1_260_000)

    def test_canary_manifest_selection_uses_one_fight_source(self) -> None:
        described: list[tuple[str, str]] = []
        original_describe = runner.describe

        def fake_describe(video_id: str, category: str) -> dict[str, object]:
            described.append((video_id, category))
            return {
                "video_id": video_id,
                "url": f"https://www.youtube.com/watch?v={video_id}",
                "title": "fixture",
                "channel": "fixture",
                "duration": 180.0,
                "category": category,
            }

        runner.describe = fake_describe
        try:
            selected = runner.select(
                "http://unused",
                "unused",
                "Floyd Mayweather Jr.",
                runner.PROFILES["canary"],
            )
        finally:
            runner.describe = original_describe

        self.assertEqual(len(selected), 1)
        self.assertEqual(selected[0]["category"], "fight")
        self.assertEqual(described, [(runner.FLOYD_MAYWEATHER_MANIFEST[0][1][0], "fight")])

    def test_full_manifest_selection_keeps_20_unique_sources(self) -> None:
        described: list[tuple[str, str]] = []
        original_describe = runner.describe

        def fake_describe(video_id: str, category: str) -> dict[str, object]:
            described.append((video_id, category))
            return {
                "video_id": video_id,
                "url": f"https://www.youtube.com/watch?v={video_id}",
                "title": "fixture",
                "channel": "fixture",
                "duration": 180.0,
                "category": category,
            }

        runner.describe = fake_describe
        try:
            selected = runner.select(
                "http://unused",
                "unused",
                "Floyd Mayweather Jr.",
                runner.PROFILES["full"],
            )
        finally:
            runner.describe = original_describe

        expected = [
            (video_id, category)
            for category, ids in runner.FLOYD_MAYWEATHER_MANIFEST
            for video_id in ids
        ]
        self.assertEqual(len(expected), 20)
        self.assertEqual(len({video_id for video_id, _ in expected}), 20)
        self.assertEqual(described, expected)
        self.assertEqual([item["video_id"] for item in selected], [video_id for video_id, _ in expected])

    def test_sugar_ray_full_manifest_selection_keeps_20_unique_sources(self) -> None:
        original_describe = runner.describe

        def fake_describe(video_id: str, category: str) -> dict[str, object]:
            return {
                "video_id": video_id,
                "url": f"https://www.youtube.com/watch?v={video_id}",
                "title": "fixture",
                "channel": "fixture",
                "duration": 180.0,
                "category": category,
            }

        runner.describe = fake_describe
        try:
            selected = runner.select(
                "http://unused",
                "unused",
                "Sugar Ray Robinson",
                runner.PROFILES["full"],
            )
        finally:
            runner.describe = original_describe

        expected_ids = [
            video_id
            for _, ids in runner.SUGAR_RAY_ROBINSON_MANIFEST
            for video_id in ids
        ]
        self.assertEqual(len(expected_ids), 20)
        self.assertEqual(len(set(expected_ids)), 20)
        self.assertEqual([item["video_id"] for item in selected], expected_ids)

    def test_segments_honor_profile_clip_count(self) -> None:
        video = {
            "video_id": "fixture",
            "duration": 180.0,
            "title": "fixture",
            "channel": "fixture",
            "category": "fight",
        }
        self.assertEqual(len(runner.segments(video, runner.PROFILES["canary"])), 3)
        self.assertEqual(len(runner.segments(video, runner.PROFILES["full"])), 15)

    def test_concurrency_is_never_above_safe_cap(self) -> None:
        self.assertEqual(runner.bounded_concurrency(1), 1)
        self.assertEqual(runner.bounded_concurrency(99), runner.MAX_CONCURRENCY)
        self.assertEqual(runner.bounded_concurrency(0), 1)
        self.assertEqual(runner.bounded_concurrency(-10), 1)

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
