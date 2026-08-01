#!/usr/bin/env python3
"""Focused tests for scripts/ci/verify-changed-components.py."""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from typing import Any


SCRIPT = Path(__file__).with_name("verify-changed-components.py")
SPEC = importlib.util.spec_from_file_location("verify_changed_components", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
verify_changed = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = verify_changed
SPEC.loader.exec_module(verify_changed)


class VerifyChangedComponentsTests(unittest.TestCase):
    @staticmethod
    def registry() -> dict[str, dict[str, Any]]:
        return {
            "script": {"paths": ["internal/domain/script/"], "dependencies": []},
            "stock": {"paths": ["internal/application/assets/providers/stock/"], "dependencies": ["script"]},
            "clips": {"paths": ["internal/domain/clips/"], "dependencies": ["script"]},
            "drive": {"paths": ["internal/infrastructure/drive/"], "dependencies": []},
        }

    def test_maps_files_to_components_and_keeps_unmapped_files(self) -> None:
        mapping, impacted, unmapped = verify_changed.map_changed_files(
            [
                "internal/application/assets/providers/stock/resolver.go",
                "internal/infrastructure/drive/client.go",
                "README.md",
            ],
            self.registry(),
        )
        self.assertEqual(
            mapping["internal/application/assets/providers/stock/resolver.go"],
            ["stock"],
        )
        self.assertEqual(mapping["internal/infrastructure/drive/client.go"], ["drive"])
        self.assertEqual(impacted, ["stock", "drive"])
        self.assertEqual(unmapped, ["README.md"])

    def test_global_files_impact_every_component(self) -> None:
        mapping, impacted, unmapped = verify_changed.map_changed_files(
            ["Makefile", "config/verify-components.json"], self.registry()
        )
        self.assertEqual(impacted, ["script", "stock", "clips", "drive"])
        self.assertEqual(mapping["Makefile"], impacted)
        self.assertEqual(unmapped, [])

    def test_collects_committed_staged_unstaged_and_untracked_files(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.git(root, "init", "-q")
            self.git(root, "config", "user.email", "test@example.invalid")
            self.git(root, "config", "user.name", "Test")
            tracked = root / "internal" / "domain" / "script"
            tracked.mkdir(parents=True)
            (tracked / "tracked.go").write_text("one\n", encoding="utf-8")
            self.git(root, "add", ".")
            self.git(root, "commit", "-qm", "initial")

            (tracked / "tracked.go").write_text("two\n", encoding="utf-8")
            staged = root / "internal" / "infrastructure" / "drive"
            staged.mkdir(parents=True)
            (staged / "staged.go").write_text("staged\n", encoding="utf-8")
            self.git(root, "add", str(staged / "staged.go"))
            untracked = root / "internal" / "domain" / "clips"
            untracked.mkdir(parents=True)
            (untracked / "new.go").write_text("new\n", encoding="utf-8")

            changes = verify_changed.collect_changed_files(root, "HEAD")
            self.assertEqual(
                changes.files,
                (
                    "internal/domain/clips/new.go",
                    "internal/domain/script/tracked.go",
                    "internal/infrastructure/drive/staged.go",
                ),
            )

    def test_run_changed_components_invokes_shared_runner_once(self) -> None:
        calls: list[tuple[str, ...]] = []

        def fake_run_components(registry, requested, **kwargs):
            calls.append(tuple(requested))
            return (
                {
                    "resolved_components": ["script", "stock", "drive"],
                    "components": {
                        name: {"status": "PASS"}
                        for name in ("script", "stock", "drive")
                    },
                    "duration_ms": 7,
                    "final": "PASS",
                },
                0,
            )

        fake_runner = SimpleNamespace(
            run_components=fake_run_components,
            resolve_components=lambda registry, requested: ["script", "stock", "drive"],
            _run_subprocess=lambda *args, **kwargs: None,
        )
        changes = verify_changed.GitChanges(
            files=(
                "internal/application/assets/providers/stock/resolver.go",
                "internal/infrastructure/drive/client.go",
            ),
            base_ref="origin/main",
            base_available=True,
            base_fallback=False,
        )
        with tempfile.TemporaryDirectory() as directory:
            execution = verify_changed.run_changed_components(
                changes,
                self.registry(),
                runner_module=fake_runner,
                repo_root=Path(directory),
                dry_run=True,
            )
        self.assertEqual(calls, [("stock", "drive")])
        self.assertEqual(execution.exit_code, 0)
        self.assertEqual(execution.report["resolved_components"], ["script", "stock", "drive"])
        self.assertEqual(execution.report["skipped"], ["clips"])

    def test_no_changes_skips_all_components(self) -> None:
        fake_runner = SimpleNamespace()
        changes = verify_changed.GitChanges((), "origin/main", True, False)
        execution = verify_changed.run_changed_components(
            changes,
            self.registry(),
            runner_module=fake_runner,
            repo_root=Path.cwd(),
        )
        self.assertEqual(execution.exit_code, 0)
        self.assertEqual(execution.report["reason"], "no_changes")
        self.assertEqual(execution.report["skipped"], list(self.registry()))

    def test_no_base_falls_back_to_all_components_for_unmapped_changes(self) -> None:
        calls: list[tuple[str, ...]] = []

        def fake_run_components(registry, requested, **kwargs):
            calls.append(tuple(requested))
            return (
                {
                    "resolved_components": list(requested),
                    "components": {name: {"status": "PASS"} for name in requested},
                    "final": "PASS",
                    "duration_ms": 0,
                },
                0,
            )

        changes = verify_changed.GitChanges(("README.md",), None, False, True)
        with tempfile.TemporaryDirectory() as directory:
            execution = verify_changed.run_changed_components(
                changes,
                self.registry(),
                runner_module=SimpleNamespace(
                    run_components=fake_run_components,
                    resolve_components=lambda registry, requested: list(requested),
                    _run_subprocess=lambda *args, **kwargs: None,
                ),
                repo_root=Path(directory),
            )
        self.assertEqual(calls, [("script", "stock", "clips", "drive")])
        self.assertEqual(execution.report["final"], "PASS")
        self.assertEqual(execution.report["file_components"]["README.md"], list(self.registry()))

    def test_unmapped_files_fail_closed_by_default(self) -> None:
        changes = verify_changed.GitChanges(("README.md",), "origin/main", True, False)
        with tempfile.TemporaryDirectory() as directory:
            execution = verify_changed.run_changed_components(
                changes,
                self.registry(),
                runner_module=SimpleNamespace(),
                repo_root=Path(directory),
            )
        self.assertEqual(execution.exit_code, verify_changed.EXIT_FAILURE)
        self.assertEqual(execution.report["final"], "FAIL")
        self.assertEqual(execution.report["reason"], "unmapped_files")
        self.assertEqual(execution.report["unmapped_files"], ["README.md"])

    def test_run_all_when_unmapped_executes_every_component(self) -> None:
        calls: list[tuple[str, ...]] = []

        def fake_run_components(registry, requested, **kwargs):
            calls.append(tuple(requested))
            return (
                {
                    "resolved_components": ["script", "stock", "clips", "drive"],
                    "components": {
                        name: {"status": "PASS"}
                        for name in ("script", "stock", "clips", "drive")
                    },
                    "final": "PASS",
                    "duration_ms": 1,
                },
                0,
            )

        changes = verify_changed.GitChanges(
            ("internal/infrastructure/drive/client.go", "README.md"),
            "origin/main",
            True,
            False,
        )
        with tempfile.TemporaryDirectory() as directory:
            execution = verify_changed.run_changed_components(
                changes,
                self.registry(),
                runner_module=SimpleNamespace(
                    run_components=fake_run_components,
                    resolve_components=lambda registry, requested: [
                        "script", "stock", "clips", "drive"
                    ],
                    _run_subprocess=lambda *args, **kwargs: None,
                ),
                repo_root=Path(directory),
                run_all_when_unmapped=True,
            )
        self.assertEqual(calls, [("script", "stock", "clips", "drive")])
        self.assertEqual(execution.exit_code, 0)
        self.assertEqual(execution.report["final"], "PASS")
        self.assertEqual(execution.report["unmapped_files"], ["README.md"])

    def test_report_with_wrong_resolved_order_fails_closed(self) -> None:
        def fake_run_components(registry, requested, **kwargs):
            return (
                {
                    "resolved_components": ["stock", "script", "drive"],
                    "components": {
                        "stock": {"status": "PASS"},
                        "script": {"status": "PASS"},
                        "drive": {"status": "PASS"},
                    },
                    "final": "PASS",
                    "duration_ms": 1,
                },
                0,
            )

        changes = verify_changed.GitChanges(
            ("internal/application/assets/providers/stock/resolver.go",),
            "origin/main",
            True,
            False,
        )
        with tempfile.TemporaryDirectory() as directory:
            execution = verify_changed.run_changed_components(
                changes,
                self.registry(),
                runner_module=SimpleNamespace(
                    run_components=fake_run_components,
                    resolve_components=lambda registry, requested: ["script", "stock"],
                    _run_subprocess=lambda *args, **kwargs: None,
                ),
                repo_root=Path(directory),
            )
        self.assertEqual(execution.exit_code, verify_changed.EXIT_FAILURE)
        self.assertEqual(execution.report["final"], "FAIL")

    def test_mapped_and_unmapped_files_fail_closed_together(self) -> None:
        changes = verify_changed.GitChanges(
            (
                "internal/infrastructure/drive/client.go",
                "README.md",
            ),
            "origin/main",
            True,
            False,
        )
        with tempfile.TemporaryDirectory() as directory:
            execution = verify_changed.run_changed_components(
                changes,
                self.registry(),
                runner_module=SimpleNamespace(),
                repo_root=Path(directory),
            )
        self.assertEqual(execution.exit_code, verify_changed.EXIT_FAILURE)
        self.assertEqual(execution.report["final"], "FAIL")
        self.assertEqual(execution.report["reason"], "unmapped_files")
        self.assertEqual(execution.report["impacted_components"], ["drive"])
        self.assertEqual(execution.report["unmapped_files"], ["README.md"])

    @staticmethod
    def git(root: Path, *args: str) -> None:
        completed = subprocess.run(["git", *args], cwd=root, check=False, capture_output=True, text=True)
        if completed.returncode:
            raise AssertionError(f"git command failed: {args}: {completed.stderr}")


if __name__ == "__main__":
    unittest.main()
