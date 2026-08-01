#!/usr/bin/env python3
"""Focused tests for scripts/ci/verify-component.py."""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import time
import unittest
from pathlib import Path
from typing import Sequence


SCRIPT = Path(__file__).with_name("verify-component.py")
SPEC = importlib.util.spec_from_file_location("verify_component", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
verify_component = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = verify_component
SPEC.loader.exec_module(verify_component)


class VerifyComponentTests(unittest.TestCase):
    def setUp(self) -> None:
        self.calls: list[tuple[tuple[str, ...], float, Path]] = []

    def fake_runner(self, argv: Sequence[str], timeout: float, cwd: Path):
        self.calls.append((tuple(argv), timeout, cwd))
        return verify_component.Execution(
            verify_component.CommandResult("PASS", 0, 3)
        )

    @staticmethod
    def registry() -> dict[str, dict]:
        return {
            "base": {
                "go_packages": ["./internal/base/..."],
                "node_tests": [],
                "python_tests": [],
                "dependencies": [],
                "timeout_seconds": 30,
                "race_enabled": True,
                "live_tests": [],
            },
            "child": {
                "go_packages": ["./internal/base/...", "./internal/child/..."],
                "node_tests": [["python3", "-c", "pass"]],
                "python_tests": [],
                "dependencies": ["base"],
                "timeout_seconds": 30,
                "race_enabled": True,
                "live_tests": [],
            },
        }

    def test_all_flag_executes_every_registry_component(self) -> None:
        registry = self.registry()
        with tempfile.TemporaryDirectory() as directory:
            registry_path = Path(directory) / "registry.json"
            report_path = Path(directory) / "report.json"
            registry_path.write_text(json.dumps(registry), encoding="utf-8")
            code = verify_component.main([
                "--all",
                "--dry-run",
                "--registry",
                str(registry_path),
                "--report",
                str(report_path),
            ])
            report = json.loads(report_path.read_text(encoding="utf-8"))
        self.assertEqual(code, 0)
        self.assertEqual(report["resolved_components"], ["base", "child"])
        self.assertEqual(report["final"], "PASS")

    def test_all_flag_is_supported_without_positional_components(self) -> None:
        args = verify_component.parse_args(["--all", "--dry-run"])
        self.assertTrue(args.all)
        self.assertEqual(args.components, [])

    def test_resolves_dependencies_once_in_stable_order(self) -> None:
        registry = self.registry()
        self.assertEqual(
            verify_component.resolve_components(registry, ["child", "base", "child"]),
            ["base", "child"],
        )

    def test_rejects_dependency_cycles(self) -> None:
        registry = {"a": {"dependencies": ["b"]}, "b": {"dependencies": ["a"]}}
        with self.assertRaisesRegex(verify_component.RegistryError, "dependency cycle: a -> b -> a"):
            verify_component.resolve_components(registry, ["a"])

    def test_rejects_unbalanced_command_quotes_as_config_error(self) -> None:
        registry = self.registry()
        registry["base"]["python_tests"] = ["python3 -c 'print(unfinished)"]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "registry.json"
            path.write_text(json.dumps(registry), encoding="utf-8")
            with self.assertRaisesRegex(verify_component.RegistryError, "invalid shell quoting"):
                verify_component.load_registry(path)

    def test_deduplicates_shared_commands_across_components(self) -> None:
        registry = self.registry()
        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_component.run_components(
                registry,
                ["child"],
                repo_root=Path(directory),
                runner=self.fake_runner,
            )
        self.assertEqual(code, 0)
        self.assertEqual(len(self.calls), 3)
        self.assertEqual(
            report["components"]["child"]["command_results"][0]["reused"],
            True,
        )
        self.assertEqual(report["components"]["base"]["status"], "PASS")
        self.assertEqual(report["final"], "PASS")

    def test_race_mode_adds_race_only_to_opted_in_go_commands(self) -> None:
        registry = self.registry()
        registry["no-race"] = {
            "go_packages": ["./internal/no-race/..."],
            "node_tests": [],
            "python_tests": [],
            "dependencies": [],
            "timeout_seconds": 30,
            "race_enabled": False,
            "live_tests": [],
        }
        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_component.run_components(
                registry,
                ["child", "no-race"],
                mode="race",
                repo_root=Path(directory),
                runner=self.fake_runner,
                dry_run=True,
            )
        self.assertEqual(code, 0)
        commands = report["components"]["child"]["commands"]
        self.assertIn("go test -race ./internal/base/...", commands)
        self.assertTrue(report["components"]["no-race"]["race_skipped"])
        self.assertIn("go test ./internal/no-race/...", report["components"]["no-race"]["commands"])

    def test_reused_command_still_respects_dependent_timeout_budget(self) -> None:
        registry = self.registry()
        registry["child"]["timeout_seconds"] = 0.01

        def slow_child_runner(argv, timeout, cwd):
            self.calls.append((tuple(argv), timeout, cwd))
            if argv[-1] == "./internal/child/...":
                time.sleep(0.02)
            return verify_component.Execution(
                verify_component.CommandResult("PASS", 0, 20)
            )

        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_component.run_components(
                registry,
                ["child"],
                repo_root=Path(directory),
                runner=slow_child_runner,
            )
        child_results = report["components"]["child"]["command_results"]
        self.assertEqual(code, verify_component.EXIT_TIMEOUT)
        self.assertTrue(child_results[0]["reused"])
        self.assertEqual(child_results[1]["status"], "TIMEOUT")
        self.assertEqual(report["components"]["child"]["status"], "TIMEOUT")

    def test_redact_never_leaks_token_values(self) -> None:
        text = (
            "VELOX_ADMIN_TOKEN=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789 "
            "AKIAIOSFODNN7EXAMPLE ghp_abcdefghijklmnopqrstuvwxyz0123456789"
        )
        redacted = verify_component.redact(text)
        self.assertNotIn("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", redacted)
        self.assertNotIn("AKIAIOSFODNN7EXAMPLE", redacted)
        self.assertNotIn("ghp_abcdefghijklmnopqrstuvwxyz0123456789", redacted)
        self.assertEqual(redacted.count("REDACTED"), 3)

    def test_real_subprocess_timeout_terminates_process_group(self) -> None:
        command = [
            sys.executable,
            "-c",
            "import subprocess, sys, time; subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)']); time.sleep(30)",
        ]
        started = time.monotonic()
        execution = verify_component._run_subprocess(command, 0.1, Path.cwd())
        elapsed = time.monotonic() - started
        self.assertEqual(execution.result.status, "TIMEOUT")
        self.assertTrue(execution.result.timed_out)
        self.assertLess(elapsed, 3.0)

    def test_timeout_is_reported_with_timeout_exit_code(self) -> None:
        def timeout_runner(argv, timeout, cwd):
            return verify_component.Execution(
                verify_component.CommandResult("TIMEOUT", None, 30, timed_out=True),
                stderr="command exceeded limit",
            )

        registry = self.registry()
        with tempfile.TemporaryDirectory() as directory:
            report_path = Path(directory) / "artifacts" / "latest.json"
            report, code = verify_component.run_components(
                registry,
                ["base"],
                repo_root=Path(directory),
                report_path=report_path,
                runner=timeout_runner,
            )
            saved = json.loads(report_path.read_text(encoding="utf-8"))
        self.assertEqual(code, verify_component.EXIT_TIMEOUT)
        self.assertEqual(report["final"], "FAIL")
        self.assertEqual(report["components"]["base"]["status"], "TIMEOUT")
        self.assertEqual(saved["components"]["base"]["command_results"][0]["status"], "TIMEOUT")

    def test_dry_run_writes_pass_report_without_invoking_runner(self) -> None:
        registry = self.registry()

        def unexpected_runner(argv, timeout, cwd):
            raise AssertionError("dry-run must not invoke commands")

        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_component.run_components(
                registry,
                ["child"],
                repo_root=Path(directory),
                runner=unexpected_runner,
                dry_run=True,
            )
        self.assertEqual(code, 0)
        self.assertTrue(report["dry_run"])
        self.assertEqual(report["final"], "PASS")


if __name__ == "__main__":
    unittest.main()
