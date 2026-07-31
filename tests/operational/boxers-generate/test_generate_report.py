#!/usr/bin/env python3
"""Regression tests for the registry-driven multilang report gates."""

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parent
REGISTRY_PATH = ROOT / "fixtures" / "boxers_stock_registry.json"
REPORT_MODULE_PATH = ROOT / "generate_report.py"
_spec = importlib.util.spec_from_file_location("generate_report", REPORT_MODULE_PATH)
_generate_report = importlib.util.module_from_spec(_spec)
assert _spec.loader is not None
_spec.loader.exec_module(_generate_report)

_registry_spec = importlib.util.spec_from_file_location("stock_registry", ROOT / "stock_registry.py")
_stock_registry = importlib.util.module_from_spec(_registry_spec)
assert _registry_spec.loader is not None
_registry_spec.loader.exec_module(_stock_registry)


class GenerateReportRegistryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        resolved = _stock_registry.load_resolved_stock(REGISTRY_PATH)
        cls.expectations = _stock_registry.scene_expectations(resolved)

    def make_successful_job(self):
        items = []
        for language in _generate_report.LANG_CODES:
            scenes = []
            for expected in self.expectations:
                scenes.append(
                    {
                        "id": f"scene-{language}-{expected['index']}",
                        "bindings": {
                            "stock": {
                                "asset_id": expected["asset_id"],
                                "drive_link": expected["drive_link"],
                                "source": "youtube",
                                "fallback": False,
                            },
                            "voiceover": {
                                "status": "completed",
                                "link": f"https://example.invalid/voice/{language}/{expected['index']}",
                                "voice": f"{language}-voice",
                                "language": language,
                                "folder_id": "test-folder",
                                "duration_seconds": 12.5,
                            },
                        },
                    }
                )
            items.append(
                {
                    "item_id": f"top5-boxers-{language}",
                    "result": {
                        "status": "SUCCEEDED",
                        "artifacts": {
                            "document": {
                                "doc_id": f"doc-{language}",
                                "doc_link": f"https://example.invalid/doc/{language}",
                            }
                        },
                        "output": {"specscene": {"scenes": scenes}},
                    },
                }
            )
        return {"result": {"data": {"items": items}}}

    def generate(self, data):
        with tempfile.TemporaryDirectory() as directory:
            job_path = Path(directory) / "job.json"
            report_path = Path(directory) / "report.json"
            job_path.write_text(json.dumps(data), encoding="utf-8")
            _generate_report.generate_report(
                str(job_path), str(report_path), str(REGISTRY_PATH), "test-folder"
            )
            return json.loads(report_path.read_text(encoding="utf-8"))

    def test_clean_report_passes(self):
        report = self.generate(self.make_successful_job())
        self.assertEqual(report["final"], "PASS")
        self.assertEqual(report["generation_status"], "completed")
        self.assertEqual(report["drive_reconciliation_status"], "passed")
        self.assertEqual(report["overall_status"], "succeeded")
        self.assertEqual(report["drive_verification"]["invalid_links"], 0)

    def test_each_invalid_drive_state_blocks_pass(self):
        for state in ("MISSING", "TRASHED", "INACCESSIBLE"):
            with self.subTest(state=state):
                data = self.make_successful_job()
                scene = data["result"]["data"]["items"][0]["result"]["output"]["specscene"]["scenes"][0]
                scene["bindings"]["stock"]["drive_verification_state"] = state
                report = self.generate(data)
                self.assertEqual(report["final"], "FAIL")
                self.assertEqual(report["generation_status"], "completed")
                self.assertEqual(report["drive_reconciliation_status"], "failed")
                self.assertEqual(report["overall_status"], "failed")
                self.assertNotEqual(report["overall_status"], "succeeded")
                self.assertEqual(report["drive_verification"]["invalid_links"], 1)
                self.assertIn("invalid_drive_links=1", report["_failures"])
                self.assertEqual(report["drive_verification"][state.lower()], 1)

    def test_unrelated_state_field_does_not_block_pass(self):
        data = self.make_successful_job()
        scene = data["result"]["data"]["items"][0]["result"]["output"]["specscene"]["scenes"][0]
        scene["bindings"]["stock"]["state"] = "MISSING"
        report = self.generate(data)
        self.assertEqual(report["final"], "PASS")
        self.assertEqual(report["drive_verification"]["invalid_links"], 0)

    def test_report_rejects_voiceover_folder_or_voice_drift(self):
        data = self.make_successful_job()
        voiceover = data["result"]["data"]["items"][0]["result"]["output"]["specscene"]["scenes"][0]["bindings"]["voiceover"]
        voiceover["voice"] = ""
        voiceover["folder_id"] = "other-folder"
        report = self.generate(data)
        self.assertEqual(report["final"], "FAIL")
        self.assertEqual(report["voiceovers"]["failed"], 1)
        self.assertIn("invalid_voiceovers=1", report["_failures"])
        self.assertTrue(any("voice is empty" in error for error in report["voiceovers"]["invalid"][0]["errors"]))
        self.assertTrue(any("folder_id" in error for error in report["voiceovers"]["invalid"][0]["errors"]))

    def test_report_requires_runtime_folder(self):
        with self.assertRaises(ValueError):
            with tempfile.TemporaryDirectory() as directory:
                job_path = Path(directory) / "job.json"
                job_path.write_text(json.dumps(self.make_successful_job()), encoding="utf-8")
                _generate_report.generate_report(
                    str(job_path), "--stdout", str(REGISTRY_PATH), ""
                )

    def test_report_uses_parent_translate_to_for_voiceover_language(self):
        data = self.make_successful_job()
        item = data["result"]["data"]["items"][0]
        item["item_id"] = "top5-boxers-it"
        for scene in item["result"]["output"]["specscene"]["scenes"]:
            scene["bindings"]["voiceover"]["language"] = "en"
        data["job"] = {
            "payload": {
                "items": [{
                    "id": "top5-boxers-it",
                    "language": "it",
                    "output": {"translate_to": "en"},
                    "docs": {"languages": ["en"]},
                }]
            }
        }
        report = self.generate(data)
        self.assertEqual(report["final"], "PASS")
        self.assertEqual(report["voiceovers"]["failed"], 0)

    def test_single_invalid_link_is_a_false_success_regression(self):
        data = self.make_successful_job()
        scene = data["result"]["data"]["items"][0]["result"]["output"]["specscene"]["scenes"][0]
        scene["bindings"]["stock"]["drive_link"] = "https://drive.google.com/file/d/DELETED_ID/view"
        scene["bindings"]["stock"]["drive_verification_state"] = "MISSING"

        report = self.generate(data)

        self.assertEqual(report["items_completed"], len(_generate_report.LANG_CODES))
        self.assertEqual(report["drive_verification"]["invalid_links"], 1)
        self.assertEqual(report["generation_status"], "completed")
        self.assertEqual(report["drive_reconciliation_status"], "failed")
        self.assertEqual(report["overall_status"], "failed")
        self.assertNotEqual(report["final"], "PASS")
        self.assertNotEqual(report["overall_status"], "SUCCEEDED")
        self.assertTrue(any("invalid_drive_links=1" == failure for failure in report["_failures"]))

    def test_each_admin_audit_detail_state_blocks_pass(self):
        for state in ("MISSING", "TRASHED", "INACCESSIBLE"):
            with self.subTest(state=state):
                data = self.make_successful_job()
                data["details"] = [{"state": state}]
                report = self.generate(data)
                self.assertEqual(report["final"], "FAIL")
                self.assertEqual(report["drive_verification"]["invalid_links"], 1)
                self.assertEqual(report["drive_verification"][state.lower()], 1)


if __name__ == "__main__":
    unittest.main()
