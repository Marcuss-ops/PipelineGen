#!/usr/bin/env python3
"""Focused tests for scripts/ci/verify-pipeline.py."""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import time
import unittest
from pathlib import Path
from typing import Sequence


SCRIPT = Path(__file__).with_name("verify-pipeline.py")
SPEC = importlib.util.spec_from_file_location("verify_pipeline", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
verify_pipeline = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = verify_pipeline
SPEC.loader.exec_module(verify_pipeline)


class VerifyPipelineTests(unittest.TestCase):
    @staticmethod
    def registry() -> dict[str, verify_pipeline.PipelineDefinition]:
        return {
            "stock-only": verify_pipeline.PipelineDefinition(
                name="stock-only",
                components=("script", "stock", "drive"),
                operational_tests=(
                    verify_pipeline.OperationalTest(
                        name="stock-e2e",
                        argv=("python3", "stock.py", "--scenes", "1"),
                        timeout_seconds=10,
                        dry_run_supported=True,
                    ),
                ),
                timeout_seconds=20,
            ),
            "clip-only": verify_pipeline.PipelineDefinition(
                name="clip-only",
                components=("script", "clips", "drive"),
                operational_tests=(
                    verify_pipeline.OperationalTest(
                        name="clip-e2e",
                        argv=("python3", "clip.py", "--clips", "1"),
                        timeout_seconds=10,
                        dry_run_supported=True,
                    ),
                ),
                timeout_seconds=20,
            ),
        }

    @staticmethod
    def write_component_report(command: Sequence[str], status: str = "PASS") -> None:
        report_index = command.index("--report") + 1
        report_path = Path(command[report_index])
        resolved = []
        for value in command:
            if value in {"script", "stock", "clips", "drive"}:
                resolved.append(value)
        report_path.write_text(
            json.dumps(
                {
                    "final": status,
                    "resolved_components": resolved,
                    "components": {name: {"status": status} for name in resolved},
                }
            ),
            encoding="utf-8",
        )

    def test_resolves_components_once_across_two_pipelines(self) -> None:
        registry = self.registry()
        available = {"script", "stock", "clips", "drive"}
        self.assertEqual(
            verify_pipeline.resolve_components(registry, ["stock-only", "clip-only"], available),
            ["script", "stock", "drive", "clips"],
        )

    def test_registry_rejects_duplicate_component_and_test_names(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "pipelines.json"
            path.write_text(
                json.dumps(
                    {
                        "broken": {
                            "components": ["script", "script"],
                            "operational_tests": [],
                        }
                    }
                ),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(verify_pipeline.PipelineConfigError, "duplicate components"):
                verify_pipeline.load_pipeline_registry(path)

    def test_dry_run_plans_operational_tests_without_executing_them(self) -> None:
        calls: list[tuple[tuple[str, ...], float, Path]] = []

        def fake_runner(argv: Sequence[str], timeout: float, cwd: Path):
            calls.append((tuple(argv), timeout, cwd))
            if "verify-component.py" in argv[1]:
                self.write_component_report(argv)
            return verify_pipeline.ProcessResult("PASS", 0, 2)

        with tempfile.TemporaryDirectory() as directory:
            report_path = Path(directory) / "report.json"
            report, code = verify_pipeline.run_pipeline(
                self.registry(),
                {"script", "stock", "clips", "drive"},
                ["stock-only", "clip-only"],
                component_runner=Path("scripts/ci/verify-component.py"),
                repo_root=Path(directory),
                report_path=report_path,
                dry_run=True,
                runner=fake_runner,
            )
        self.assertEqual(code, 0)
        self.assertEqual(report["final"], "PASS")
        self.assertEqual(report["resolved_components"], ["script", "stock", "drive", "clips"])
        self.assertEqual(len(calls), 1)
        self.assertTrue(all(
            result["status"] == "PLANNED"
            for pipeline in report["pipelines"].values()
            for result in pipeline["operational_tests"]
        ))

    def test_missing_component_report_fails_closed(self) -> None:
        def runner_without_report(argv: Sequence[str], timeout: float, cwd: Path):
            return verify_pipeline.ProcessResult("PASS", 0, 1)

        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_pipeline.run_pipeline(
                self.registry(),
                {"script", "stock", "drive"},
                ["stock-only"],
                component_runner=Path("scripts/ci/verify-component.py"),
                repo_root=Path(directory),
                runner=runner_without_report,
            )
        self.assertEqual(code, verify_pipeline.EXIT_FAILURE)
        self.assertEqual(report["components"]["status"], "FAIL")
        self.assertEqual(report["final"], "FAIL")
        self.assertEqual(report["pipelines"]["stock-only"]["operational_tests"], [])

    def test_real_subprocess_timeout_terminates_process_group(self) -> None:
        command = [
            sys.executable,
            "-c",
            "import subprocess, sys, time; subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)']); time.sleep(30)",
        ]
        started = time.monotonic()
        result = verify_pipeline.run_process(command, 0.1, Path.cwd())
        elapsed = time.monotonic() - started
        self.assertEqual(result.status, "TIMEOUT")
        self.assertTrue(result.timed_out)
        self.assertLess(elapsed, 3.0)

    def test_component_report_mismatch_blocks_operational_tests(self) -> None:
        def incomplete_report_runner(argv: Sequence[str], timeout: float, cwd: Path):
            report_index = argv.index("--report") + 1
            Path(argv[report_index]).write_text(
                json.dumps({
                    "final": "PASS",
                    "resolved_components": ["script"],
                    "components": {"script": {"status": "PASS"}},
                }),
                encoding="utf-8",
            )
            return verify_pipeline.ProcessResult("PASS", 0, 1)

        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_pipeline.run_pipeline(
                self.registry(),
                {"script", "stock", "drive"},
                ["stock-only"],
                component_runner=Path("scripts/ci/verify-component.py"),
                repo_root=Path(directory),
                runner=incomplete_report_runner,
            )
        self.assertEqual(code, verify_pipeline.EXIT_FAILURE)
        self.assertEqual(report["components"]["status"], "FAIL")
        self.assertEqual(report["pipelines"]["stock-only"]["operational_tests"], [])

    def test_component_failure_blocks_operational_tests(self) -> None:
        calls: list[tuple[str, ...]] = []

        def failing_runner(argv: Sequence[str], timeout: float, cwd: Path):
            calls.append(tuple(argv))
            if "verify-component.py" in argv[1]:
                self.write_component_report(argv, "FAIL")
                return verify_pipeline.ProcessResult("FAIL", 1, 4)
            raise AssertionError("operational test must not run after component failure")

        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_pipeline.run_pipeline(
                self.registry(),
                {"script", "stock", "clips", "drive"},
                ["stock-only"],
                component_runner=Path("scripts/ci/verify-component.py"),
                repo_root=Path(directory),
                runner=failing_runner,
            )
        self.assertEqual(code, verify_pipeline.EXIT_FAILURE)
        self.assertEqual(report["final"], "FAIL")
        self.assertEqual(report["pipelines"]["stock-only"]["status"], "FAIL")
        self.assertEqual(len(calls), 1)
        self.assertEqual(report["pipelines"]["stock-only"]["operational_tests"], [])

    def test_operational_timeout_returns_timeout_exit_code(self) -> None:
        calls = 0

        def timeout_runner(argv: Sequence[str], timeout: float, cwd: Path):
            nonlocal calls
            calls += 1
            if "verify-component.py" in argv[1]:
                self.write_component_report(argv)
                return verify_pipeline.ProcessResult("PASS", 0, 1)
            time.sleep(0.02)
            return verify_pipeline.ProcessResult("TIMEOUT", None, 20, timed_out=True)

        definition = self.registry()["stock-only"]
        short_registry = {"stock-only": verify_pipeline.PipelineDefinition(
            name=definition.name,
            components=definition.components,
            operational_tests=(verify_pipeline.OperationalTest(
                name="stock-e2e",
                argv=definition.operational_tests[0].argv,
                timeout_seconds=0.01,
                dry_run_supported=True,
            ),),
            timeout_seconds=1,
        )}
        with tempfile.TemporaryDirectory() as directory:
            report, code = verify_pipeline.run_pipeline(
                short_registry,
                {"script", "stock", "drive"},
                ["stock-only"],
                component_runner=Path("scripts/ci/verify-component.py"),
                repo_root=Path(directory),
                runner=timeout_runner,
            )
        self.assertEqual(code, verify_pipeline.EXIT_TIMEOUT)
        self.assertEqual(report["final"], "FAIL")
        self.assertEqual(report["pipelines"]["stock-only"]["status"], "TIMEOUT")
        self.assertEqual(calls, 2)

    def test_process_start_failure_preserves_fail_closed_result_shape(self) -> None:
        result = verify_pipeline.run_process(
            ["/definitely/missing/verify-pipeline-command"],
            1,
            Path.cwd(),
        )
        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.exit_code, 127)
        self.assertGreaterEqual(result.duration_ms, 0)
        self.assertFalse(result.timed_out)

    def test_real_registry_loads_initial_pipelines(self) -> None:
        path = Path(__file__).parents[2] / "config" / "verify-pipelines.json"
        registry = verify_pipeline.load_pipeline_registry(path)
        self.assertEqual(
            set(registry),
            {"stock-only", "clip-only", "research", "document", "voiceover", "script", "youtube-stock", "vidrush"},
        )
        self.assertEqual(
            registry["stock-only"].components,
            ("script", "stock", "drive", "docs", "database", "jobs", "api"),
        )
        self.assertEqual(
            registry["clip-only"].components,
            ("script", "clips", "drive", "docs", "database", "jobs", "api"),
        )


if __name__ == "__main__":
    unittest.main()
