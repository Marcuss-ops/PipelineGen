#!/usr/bin/env python3
"""Regression tests for reliable boxer report publication."""

from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parent
SPEC = importlib.util.spec_from_file_location(
    "report_publication", ROOT / "report_publication.py"
)
report_publication = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(report_publication)


class ReportPublicationTest(unittest.TestCase):
    def parent(self, status: str) -> dict:
        return {
            "job": {"status": status},
            "result": {"data": {"child_job_ids": ["child-1"]}},
        }

    def child(self, status: str = "SUCCEEDED") -> dict:
        return {"status": status, "result": {"data": {"items": [{"id": "item-1"}]}}}

    def test_stale_running_full_is_replaced_by_terminal_full(self):
        stale = self.parent("RUNNING")
        terminal = self.parent("SUCCEEDED")
        selected = report_publication.select_terminal_parent([stale, terminal])
        self.assertEqual(selected["job"]["status"], "SUCCEEDED")

    def test_running_only_full_response_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "never became terminal"):
            report_publication.select_terminal_parent([self.parent("RUNNING")])

    def test_parent_and_child_results_must_be_terminal_and_present(self):
        errors = report_publication.validate_parent_and_children(
            self.parent("SUCCEEDED"),
            {"child-1": self.child()},
            ["child-1"],
        )
        self.assertEqual(errors, [])

        errors = report_publication.validate_parent_and_children(
            self.parent("SUCCEEDED"),
            {"child-1": self.child("RUNNING")},
            ["child-1"],
        )
        self.assertTrue(any("not terminal" in error for error in errors))

        errors = report_publication.validate_parent_and_children(
            self.parent("SUCCEEDED"), {}, ["child-1"]
        )
        self.assertTrue(any("missing full response" in error for error in errors))

    def test_atomic_copy_and_incomplete_archive_leave_no_partial_artifact(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "source.json"
            source.write_text(json.dumps({"status": "SUCCEEDED"}), encoding="utf-8")
            destination = root / "reports" / "verification_report.json"
            report_publication.atomic_copy(source, destination)
            self.assertEqual(json.loads(destination.read_text(encoding="utf-8"))["status"], "SUCCEEDED")
            self.assertFalse(destination.with_name(".verification_report.json.tmp").exists())

            pending = root / "pending.json"
            pending.write_text("{\"status\":\"RUNNING\"}\n", encoding="utf-8")
            incomplete = root / "reports" / "incomplete"
            archived = report_publication.archive_incomplete(
                [pending], incomplete, "20260731T000000Z"
            )
            self.assertEqual(len(archived), 1)
            self.assertFalse(pending.exists())
            self.assertTrue(archived[0].exists())


if __name__ == "__main__":
    unittest.main()
