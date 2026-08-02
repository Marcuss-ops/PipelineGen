#!/usr/bin/env python3
"""Tests for the fail-closed component coverage gate."""

from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[2]
SCRIPT = ROOT / "scripts" / "ci" / "verify-component-coverage.py"
SPEC = importlib.util.spec_from_file_location("verify_component_coverage", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ComponentCoverageTests(unittest.TestCase):
    def test_registered_path_must_exist(self) -> None:
        errors = MODULE.validate_registry(
            ROOT,
            {"test": {"paths": ["missing/"], "go_packages": ["./pkg/"]}},
        )
        self.assertIn("component=test: missing registry path missing/", errors)

    def test_unmapped_production_file_is_reported(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / ".git").mkdir()
            self.assertEqual(MODULE.owners("internal/new/file.go", {}), [])

    def test_reports_current_repository_as_covered(self) -> None:
        report = MODULE.build_report(ROOT, ROOT / "config/verify-components.json")
        self.assertEqual(report["final"], "PASS", report)
        self.assertEqual(report["unmapped_files"], [])
        self.assertEqual(report["registry_errors"], [])
        self.assertEqual(report["command_errors"], [])
        self.assertEqual(report["stale_registry_paths"], [])
        self.assertEqual(report["unexpected_overlaps"], [])
        self.assertEqual(report["coverage_percent"], 100.0)
        self.assertIn("reason", report["mapping"]["Makefile"])

    def test_support_domains_are_explicit_fallback_owners(self) -> None:
        self.assertEqual(MODULE.fallback_owner("cmd/admin/main.go")[0], "commands")
        self.assertEqual(MODULE.fallback_owner("scripts/ci/test_verify.py")[0], "verification")
        self.assertEqual(MODULE.fallback_owner("internal/app/main.go")[0], "core-internal")

    def test_unexpected_overlap_is_not_hidden(self) -> None:
        registry = {
            "one": {"paths": ["internal/shared/"]},
            "two": {"paths": ["internal/shared/"]},
        }
        self.assertFalse(MODULE.overlap_is_allowed("internal/shared/file.go", ["one", "two"]))
        self.assertTrue(
            MODULE.overlap_is_allowed(
                "internal/application/scripts/usecase/file.go",
                ["script", "research"],
            )
        )


if __name__ == "__main__":
    unittest.main()
