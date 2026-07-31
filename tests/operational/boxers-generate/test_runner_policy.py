#!/usr/bin/env python3
"""Tests for the boxer runner smoke/strict policy contract."""

import importlib.util
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parent
SPEC = importlib.util.spec_from_file_location("runner_policy", ROOT / "runner_policy.py")
runner_policy = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(runner_policy)


class RunnerPolicyTest(unittest.TestCase):
    def test_scenario_07_blocked_result_is_a_non_post_dependency_outcome(self):
        scenario = json.loads((ROOT / "scenarios" / "top5_financial_stories_multilang.json").read_text())
        self.assertEqual(scenario["_runner"]["mode"], "smoke")
        self.assertTrue(all(
            binding["asset_id"].startswith("{{stock.")
            for item in scenario["items"]
            for binding in item["output"]["stock_bindings"]
        ))
        blocked = {"status": "BLOCKED", "missing": [
            {"subject": "Floyd Mayweather", "role": "fight", "reason": "NO_ACTIVE_ASSET"},
            {"subject": "Sugar Ray Robinson", "role": "fight", "reason": "NO_ACTIVE_ASSET"},
        ]}
        self.assertEqual(blocked["status"], "BLOCKED")
        self.assertEqual(len(blocked["missing"]), 2)
        self.assertNotIn("job_id", blocked)
        self.assertNotIn("voiceover_requests", blocked)

    def test_every_scenario_declares_a_valid_runner_mode(self):
        scenarios = sorted((ROOT / "scenarios").glob("*.json"))
        self.assertTrue(scenarios)
        for path in scenarios:
            with self.subTest(scenario=path.name):
                payload = json.loads(path.read_text(encoding="utf-8"))
                mode = payload.get("_runner", {}).get("mode")
                self.assertIn(mode, runner_policy.MODES)

    def test_strict_scenarios_enable_quality_gate_and_smoke_is_explicit(self):
        expected_modes = {
            "01_source_segments.json": "strict",
            "02_translation_voiceover.json": "strict",
            "03_supplied_clips.json": "strict",
            "04_direct_stock_bindings.json": "strict",
            "05_full_pipeline.json": "strict",
            "06_negative_translation_fail.json": "smoke",
            "top5_financial_stories_multilang.json": "smoke",
        }
        for filename, mode in expected_modes.items():
            with self.subTest(filename=filename):
                payload = json.loads((ROOT / "scenarios" / filename).read_text(encoding="utf-8"))
                self.assertEqual(payload["_runner"]["mode"], mode)
                for item in payload["items"]:
                    skip = item["script_params"]["skip_quality_gate"]
                    self.assertEqual(skip, mode == "smoke")

    def test_prepare_payload_strips_metadata_and_enables_strict_quality_gate(self):
        payload = {
            "_runner": {"mode": "strict"},
            "items": [{"script_params": {"skip_quality_gate": True}}],
        }
        prepared = runner_policy.prepare_payload(payload, "strict")
        self.assertNotIn("_runner", prepared)
        self.assertFalse(prepared["items"][0]["script_params"]["skip_quality_gate"])
        self.assertIn("_runner", payload)
        self.assertTrue(payload["items"][0]["script_params"]["skip_quality_gate"])

    def test_prepare_payload_strips_metadata_but_preserves_smoke_quality_setting(self):
        payload = {
            "_runner": {"mode": "smoke"},
            "items": [{"script_params": {"skip_quality_gate": True}}],
        }
        prepared = runner_policy.prepare_payload(payload, "smoke")
        self.assertNotIn("_runner", prepared)
        self.assertTrue(prepared["items"][0]["script_params"]["skip_quality_gate"])

    def test_smoke_accepts_only_explicitly_allowed_warning(self):
        response = {
            "status": "SUCCEEDED_WITH_WARNINGS",
            "warnings": ["quality gate warning"],
        }
        self.assertEqual(
            runner_policy.validate_response(response, "smoke", allowed_warning_regex="quality"),
            [],
        )
        errors = runner_policy.validate_response(
            response, "smoke", allowed_warning_regex="translation failed"
        )
        self.assertTrue(any("not allowed" in error for error in errors))

    def test_smoke_rejects_warning_status_without_explicit_warning(self):
        errors = runner_policy.validate_response(
            {"status": "SUCCEEDED_WITH_WARNINGS"}, "smoke", allowed_warning_regex="quality"
        )
        self.assertTrue(any("no explicit warning" in error for error in errors))

    def test_strict_rejects_warning_status_warnings_and_fallback(self):
        response = {
            "status": "SUCCEEDED_WITH_WARNINGS",
            "warnings": ["quality gate warning"],
            "result": {"data": {"output": {"specscene": {"scenes": [
                {"bindings": {"stock": {
                    "asset_id": "asset-1",
                    "drive_link": "https://drive/1",
                    "source": "youtube",
                    "fallback": True,
                }}}
            ]}}}},
        }
        errors = runner_policy.validate_response(response, "strict")
        self.assertTrue(any("SUCCEEDED_WITH_WARNINGS" in error for error in errors))
        self.assertTrue(any("warning is forbidden" in error for error in errors))
        self.assertTrue(any("fallback binding" in error for error in errors))

    def test_strict_requires_active_lifecycle_state(self):
        response = {"status": "SUCCEEDED", "result": {"data": {"output": {
            "specscene": {"scenes": [{"bindings": {"stock": {
                "asset_id": "asset-deleted",
                "drive_link": "https://drive/deleted",
                "source": "youtube",
            }}}]}
        }}}}
        with tempfile.TemporaryDirectory() as directory:
            db_path = Path(directory) / "assets.sqlite"
            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE media_assets (id TEXT PRIMARY KEY, lifecycle_state TEXT)"
                )
                connection.execute(
                    "INSERT INTO media_assets VALUES ('asset-deleted', 'DELETED')"
                )
                connection.commit()
            errors = runner_policy.validate_response(response, "strict", str(db_path))
        self.assertTrue(any("lifecycle_state" in error for error in errors))
        self.assertFalse(any("lifecycle_status" in error for error in errors))

    def test_strict_checks_supplied_clip_lifecycle_state(self):
        response = {"status": "SUCCEEDED", "output": {"specscene": {
            "scenes": [{"bindings": {"clip": {
                "clip_id": "clip-deleted",
                "drive_link": "https://drive/clip",
                "source": "youtube",
            }}}]
        }}}
        with tempfile.TemporaryDirectory() as directory:
            db_path = Path(directory) / "assets.sqlite"
            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE media_assets (id TEXT PRIMARY KEY, lifecycle_state TEXT)"
                )
                connection.execute(
                    "INSERT INTO media_assets VALUES ('clip-deleted', 'DELETED')"
                )
                connection.commit()
            errors = runner_policy.validate_response(response, "strict", str(db_path))
        self.assertTrue(any("clip-deleted" in error and "lifecycle_state" in error for error in errors))

    def test_strict_rejects_missing_asset_without_binding_metadata(self):
        response = {"status": "SUCCEEDED", "output": {
            "stock_bindings": [{"asset_id": "asset-missing"}]
        }}
        with tempfile.TemporaryDirectory() as directory:
            db_path = Path(directory) / "assets.sqlite"
            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE media_assets (id TEXT PRIMARY KEY, lifecycle_state TEXT)"
                )
                connection.commit()
            errors = runner_policy.validate_response(response, "strict", str(db_path))
        self.assertTrue(any("missing from SQLite" in error for error in errors))

    def test_voiceover_preflight_rejects_empty_voice(self):
        with tempfile.TemporaryDirectory() as directory:
            db_path = Path(directory) / "voiceovers.sqlite"
            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE voiceovers (id TEXT, language TEXT, voice TEXT, "
                    "folder_id TEXT, status TEXT)"
                )
                connection.execute(
                    "INSERT INTO voiceovers VALUES "
                    "('vo-empty', 'it', '', 'canonical-folder', 'generated')"
                )
                connection.commit()
            errors = runner_policy.voiceover_db_preflight(
                str(db_path), "canonical-folder"
            )
        self.assertTrue(any("empty voice" in error for error in errors))

    def test_voiceover_preflight_rejects_folder_drift_and_incoherence(self):
        with tempfile.TemporaryDirectory() as directory:
            db_path = Path(directory) / "voiceovers.sqlite"
            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE voiceovers (id TEXT, language TEXT, voice TEXT, "
                    "folder_id TEXT, status TEXT)"
                )
                connection.executemany(
                    "INSERT INTO voiceovers VALUES (?, ?, ?, ?, ?)",
                    [
                        ("vo-good", "it", "voice-it", "canonical-folder", "completed"),
                        ("vo-drift", "en", "voice-en", "other-folder", "generated"),
                    ],
                )
                connection.commit()
            errors = runner_policy.voiceover_db_preflight(
                str(db_path), "canonical-folder"
            )
        self.assertTrue(any("does not match BOXERS_VOICEOVER_FOLDER_ID" in error for error in errors))
        self.assertTrue(any("incoherent folder_id" in error for error in errors))

    def test_runner_scenarios_do_not_store_real_voiceover_folder_ids(self):
        source = (ROOT / "run.sh").read_text(encoding="utf-8")
        scenario_files = sorted((ROOT / "scenarios").glob("*.json"))
        self.assertNotIn("1unQMyEH_ZqtXHT5D-68dxvcV9KgKA6d4", source)
        for path in scenario_files:
            with self.subTest(scenario=path.name):
                self.assertNotIn(
                    "1unQMyEH_ZqtXHT5D-68dxvcV9KgKA6d4",
                    path.read_text(encoding="utf-8"),
                )


if __name__ == "__main__":
    unittest.main()
