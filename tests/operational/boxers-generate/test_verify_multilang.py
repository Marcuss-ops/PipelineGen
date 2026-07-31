#!/usr/bin/env python3
"""Regression tests for multilang language and voiceover verification."""

from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path


ROOT = Path(__file__).parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))
SPEC = importlib.util.spec_from_file_location(
    "verify_multilang", ROOT / "verify_multilang.py"
)
verify = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(verify)


class VerifyMultilangMetadataTest(unittest.TestCase):
    def test_effective_language_precedence(self):
        self.assertEqual(
            verify.effective_language(
                {
                    "language": "it",
                    "docs": {"languages": ["fr"]},
                    "output": {"translate_to": "en"},
                }
            ),
            "en",
        )
        self.assertEqual(
            verify.effective_language(
                {"language": "it", "docs": {"languages": ["fr"]}}
            ),
            "fr",
        )
        self.assertEqual(verify.effective_language({"language": "it"}), "it")

    def test_effective_language_reads_nested_result_envelope(self):
        item = {
            "item_id": "top5-boxers-en",
            "result": {
                "data": {
                    "data": {
                        "output": {"translate_to": "en"},
                        "docs": {"languages": ["fr"]},
                        "language": "it",
                    }
                }
            },
        }
        self.assertEqual(verify.effective_language(item), "en")

    def test_extract_items_attaches_parent_request_metadata(self):
        response = {
            "job": {
                "payload": {"items": [{
                    "id": "top5-boxers-en",
                    "language": "it",
                    "output": {
                        "translate_to": "en",
                        "voiceover_folder_id": "canonical-folder",
                    },
                    "docs": {"languages": ["en"], "folder_id": "canonical-folder"},
                }]},
            },
            "result": {"data": {"items": [{
                "item_id": "top5-boxers-en",
                "result": {"output": {"text": "translated"}},
            }]}},
        }
        item = verify.extract_items(response)[0]
        self.assertEqual(verify.effective_language(item), "en")
        self.assertEqual(verify.canonical_folder_id(item), "canonical-folder")

    def test_canonical_folder_id_reads_output_and_docs_metadata(self):
        self.assertEqual(
            verify.canonical_folder_id(
                {
                    "output": {"voiceover_folder_id": "output-folder"},
                    "docs": {"folder_id": "docs-folder"},
                }
            ),
            "output-folder",
        )
        self.assertEqual(
            verify.canonical_folder_id({"docs": {"folder_id": "docs-folder"}}),
            "docs-folder",
        )

    def test_voiceover_accepts_complete_positive_binding(self):
        voiceover = {
            "status": "SUCCEEDED",
            "drive_link": "https://drive.example/voice.mp3",
            "voice": "en-US-GuyNeural",
            "language": "en",
            "folder_id": "canonical-folder",
            "duration_seconds": 12.5,
        }
        self.assertEqual(
            verify.validate_voiceover(voiceover, "en", "canonical-folder"), []
        )

    def test_voiceover_rejects_each_required_field_violation(self):
        valid = {
            "status": "completed",
            "drive_link": "https://drive.example/voice.mp3",
            "voice": "en-US-GuyNeural",
            "language": "en",
            "folder_id": "canonical-folder",
            "duration_seconds": 12,
        }
        cases = {
            "status": {"status": "RUNNING"},
            "drive_link": {"drive_link": ""},
            "voice": {"voice": ""},
            "language": {"language": "it"},
            "folder": {"folder_id": "other-folder"},
            "duration": {"duration_seconds": 0},
        }
        for label, override in cases.items():
            with self.subTest(field=label):
                candidate = {**valid, **override}
                errors = verify.validate_voiceover(
                    candidate, "en", "canonical-folder"
                )
                self.assertTrue(errors, label)

    def test_voiceover_rejects_nan_duration(self):
        errors = verify.validate_voiceover(
            {
                "status": "completed",
                "drive_link": "https://drive.example/voice.mp3",
                "voice": "en-US-GuyNeural",
                "language": "en",
                "folder_id": "canonical-folder",
                "duration_seconds": "nan",
            },
            "en",
            "canonical-folder",
        )
        self.assertTrue(any("duration_seconds" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
