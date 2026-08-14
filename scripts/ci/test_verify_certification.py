#!/usr/bin/env python3
"""Black-box certification tests for the verification framework."""

from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from typing import Sequence

ROOT = Path(__file__).parents[2]
SCRIPT = ROOT / "scripts/ci/verify-component.py"
SPEC = importlib.util.spec_from_file_location("verify_component_cert", SCRIPT)
assert SPEC and SPEC.loader
runner = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = runner
SPEC.loader.exec_module(runner)


def definition(*, dependencies=None, commands=None, timeout=30):
    return {
        "paths": ["internal/test/"], "go_packages": [], "node_tests": [],
        "python_tests": commands or [], "live_tests": [],
        "dependencies": dependencies or [], "timeout_seconds": timeout,
        "race_timeout_seconds": timeout, "race_enabled": True,
    }


class VerifyFrameworkCertificationTests(unittest.TestCase):
    def test_invalid_registry_mutations_fail_before_any_command(self):
        mutations = {
            "broken-json": None,
            "zero-timeout": {"timeout_seconds": 0},
            "boolean-timeout": {"timeout_seconds": True},
            "non-boolean-race": {"race_enabled": "yes"},
            "unknown-dependency": {"dependencies": ["missing"]},
            "self-dependency": {"dependencies": ["base"]},
            "empty-command": {"python_tests": [[]]},
            "empty-package": {"go_packages": [""]},
            "empty-component-name": {"name": ""},
        }
        for mutation_name, mutation in mutations.items():
            with self.subTest(mutation_name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                marker = root / "executed"
                registry = {"base": definition(commands=[[sys.executable, "-c", f"Path({str(marker)!r}).touch()"]])}
                path = root / "registry.json"
                if mutation_name == "broken-json":
                    path.write_text("{broken", encoding="utf-8")
                elif mutation_name == "empty-component-name":
                    path.write_text(json.dumps({"": definition()}), encoding="utf-8")
                else:
                    registry["base"].update(mutation)
                    path.write_text(json.dumps(registry), encoding="utf-8")
                report_path = root / "report.json"
                result = subprocess.run(
                    [sys.executable, str(SCRIPT), "base", "--registry", str(path), "--report", str(report_path)],
                    cwd=ROOT, text=True, capture_output=True, check=False,
                )
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertFalse(marker.exists())
                report = json.loads(report_path.read_text(encoding="utf-8"))
                self.assertEqual(report["final"], "CONFIG_ERROR")
                self.assertEqual(report["components"], {})

    def test_cycle_and_diamond_are_handled_deterministically(self):
        cycle = {"a": definition(dependencies=["b"]), "b": definition(dependencies=["a"])}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "cycle.json"
            path.write_text(json.dumps(cycle), encoding="utf-8")
            with self.assertRaisesRegex(runner.RegistryError, "dependency cycle"):
                runner.load_registry(path)

        registry = {
            "database": definition(commands=[["shared", "test"]]),
            "qdrant": definition(dependencies=["database"]),
            "indexing": definition(dependencies=["database", "qdrant"]),
            "jobs": definition(dependencies=["database"]),
            "api": definition(dependencies=["jobs"]),
        }
        requested = ["qdrant", "indexing", "jobs", "api"]
        expected = ["database", "qdrant", "indexing", "jobs", "api"]
        self.assertEqual(runner.resolve_components(registry, requested), expected)
        calls = []

        def fake(argv: Sequence[str], timeout: float, cwd: Path):
            calls.append(tuple(argv))
            return runner.Execution(runner.CommandResult("PASS", 0, 1))

        with tempfile.TemporaryDirectory() as directory:
            report, code = runner.run_components(registry, requested, repo_root=Path(directory), runner=fake)
        self.assertEqual(code, 0)
        self.assertEqual(calls, [("shared", "test")])
        self.assertEqual(report["resolved_components"], expected)

    def test_failure_propagation_blocks_dependents_and_keeps_independent_work(self):
        registry = {
            "database": definition(commands=[["database", "fails"]]),
            "jobs": definition(dependencies=["database"], commands=[["jobs", "no"]]),
            "api": definition(dependencies=["jobs"], commands=[["api", "no"]]),
            "script": definition(commands=[["script", "passes"]]),
        }
        calls = []

        def fake(argv: Sequence[str], timeout: float, cwd: Path):
            calls.append(tuple(argv))
            failed = tuple(argv) == ("database", "fails")
            return runner.Execution(runner.CommandResult("FAIL" if failed else "PASS", 1 if failed else 0, 1))

        with tempfile.TemporaryDirectory() as directory:
            report, code = runner.run_components(registry, ["api", "script"], repo_root=Path(directory), runner=fake)
        self.assertEqual(code, 1)
        # Independent components may run concurrently; only the multiset of
        # executions is deterministic.
        self.assertEqual(sorted(calls), sorted([("database", "fails"), ("script", "passes")]))
        self.assertEqual(report["components"]["jobs"]["status"], "BLOCKED")
        self.assertEqual(report["components"]["jobs"]["blocked_by"], ["database"])
        self.assertEqual(report["components"]["api"]["status"], "BLOCKED")
        self.assertEqual(report["components"]["api"]["blocked_by"], ["jobs"])

    def test_live_is_skipped_unless_explicitly_enabled(self):
        registry = {"live": definition()}
        registry["live"]["live_tests"] = [[sys.executable, "-c", "raise SystemExit(9)"]]
        calls = []

        def fake(argv: Sequence[str], timeout: float, cwd: Path):
            calls.append(tuple(argv))
            return runner.Execution(runner.CommandResult("PASS", 0, 1))

        with tempfile.TemporaryDirectory() as directory:
            report, code = runner.run_components(registry, ["live"], repo_root=Path(directory), runner=fake)
            self.assertEqual((code, calls), (0, []))
            self.assertEqual(len(report["components"]["live"]["skipped_live"]), 1)
            runner.run_components(registry, ["live"], repo_root=Path(directory), runner=fake, include_live=True)
        self.assertEqual(len(calls), 1)

    def test_timeout_kills_process_group_and_reports_124(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            pid_file = root / "child.pid"
            registry = {"slow": definition(timeout=1, commands=[[sys.executable, "-c", "import subprocess,time; p=subprocess.Popen(['sleep','30']); open(%r,'w').write(str(p.pid)); time.sleep(30)" % str(pid_file)]])}
            path = root / "registry.json"
            path.write_text(json.dumps(registry), encoding="utf-8")
            report_path = root / "report.json"
            result = subprocess.run([sys.executable, str(SCRIPT), "slow", "--registry", str(path), "--report", str(report_path)], cwd=ROOT, text=True, capture_output=True, check=False)
            self.assertEqual(result.returncode, 124, result.stderr)
            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertEqual(report["components"]["slow"]["status"], "TIMEOUT")
            self.assertTrue(report["components"]["slow"]["command_results"][0]["timed_out"])
            child_pid = int(pid_file.read_text(encoding="utf-8"))
            for _ in range(20):
                try:
                    os.kill(child_pid, 0)
                except ProcessLookupError:
                    break
                time.sleep(0.05)
            else:
                self.fail("timeout left child process alive")

    def test_report_schema_excludes_stdout_and_stderr_secrets(self):
        def fake(argv, timeout, cwd):
            return runner.Execution(runner.CommandResult("FAIL", 1, 1), stdout="Bearer super-secret-token", stderr="VELOX_ADMIN_TOKEN=my-secret ghp_FAKESECRET12345678901234567890")

        with tempfile.TemporaryDirectory() as directory:
            report_path = Path(directory) / "report.json"
            report, code = runner.run_components({"secret": definition(commands=[["secret"]])}, ["secret"], repo_root=Path(directory), report_path=report_path, runner=fake)
            serialized = report_path.read_text(encoding="utf-8")
        self.assertEqual(code, 1)
        self.assertEqual(report["schema_version"], 1)
        for field in ("started_at", "finished_at", "duration_ms", "mode", "requested_components", "resolved_components", "git_sha", "final"):
            self.assertIn(field, report)
        self.assertNotIn("super-secret-token", serialized)
        self.assertNotIn("my-secret", serialized)
        self.assertNotIn("ghp_FAKESECRET", serialized)


if __name__ == "__main__":
    unittest.main()
