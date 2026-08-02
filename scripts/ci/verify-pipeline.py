#!/usr/bin/env python3
"""Run registered end-to-end verification pipelines.

Pipeline definitions stay declarative in ``config/verify-pipelines.json``.
This runner resolves all component names once, delegates component execution to
``verify-component.py``, then runs each configured operational test
sequentially.  It is intentionally standard-library-only and fail-closed.

Examples:
    python3 scripts/ci/verify-pipeline.py stock-only --dry-run
    python3 scripts/ci/verify-pipeline.py clip-only
    python3 scripts/ci/verify-pipeline.py stock-only clip-only --report /tmp/pipeline.json
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import signal
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, Iterable, Mapping, Sequence


DEFAULT_REGISTRY = Path("config/verify-pipelines.json")
DEFAULT_COMPONENT_REGISTRY = Path("config/verify-components.json")
DEFAULT_COMPONENT_RUNNER = Path("scripts/ci/verify-component.py")
DEFAULT_REPORT = Path("artifacts/verify/latest.json")
EXIT_CONFIG_ERROR = 2
EXIT_FAILURE = 1
EXIT_TIMEOUT = 124


class PipelineConfigError(ValueError):
    """The pipeline registry cannot be safely executed."""


@dataclass(frozen=True)
class ProcessResult:
    status: str
    exit_code: int | None
    duration_ms: int
    stdout: str = ""
    stderr: str = ""
    timed_out: bool = False


@dataclass(frozen=True)
class OperationalTest:
    name: str
    argv: tuple[str, ...]
    timeout_seconds: float
    dry_run_supported: bool

    @property
    def display(self) -> str:
        return shlex.join(self.argv)


@dataclass(frozen=True)
class PipelineDefinition:
    name: str
    components: tuple[str, ...]
    operational_tests: tuple[OperationalTest, ...]
    timeout_seconds: float


@dataclass(frozen=True)
class ExecuteContext:
    repo_root: Path
    runner: Callable[[Sequence[str], float, Path], ProcessResult]


def _now_utc() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _git_sha(root: Path) -> str | None:
    completed = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=str(root),
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        check=False,
    )
    value = completed.stdout.strip()
    return value if completed.returncode == 0 and value else None


def _positive_number(value: Any, field: str, context: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)) or value <= 0:
        raise PipelineConfigError(f"{context}: {field} must be positive")
    return float(value)


def _argv(value: Any, field: str, context: str) -> tuple[str, ...]:
    if isinstance(value, str):
        try:
            result = tuple(shlex.split(value))
        except ValueError as exc:
            raise PipelineConfigError(f"{context}: {field} has invalid shell quoting: {exc}") from exc
    elif isinstance(value, list):
        if any(not isinstance(item, str) or not item for item in value):
            raise PipelineConfigError(f"{context}: {field} must contain non-empty strings")
        result = tuple(value)
    else:
        raise PipelineConfigError(f"{context}: {field} must be a command string or argv array")
    if not result:
        raise PipelineConfigError(f"{context}: {field} must not be empty")
    return result


def load_pipeline_registry(path: Path) -> dict[str, PipelineDefinition]:
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise PipelineConfigError(f"cannot read pipeline registry {path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise PipelineConfigError(f"invalid JSON in pipeline registry {path}: {exc}") from exc

    if not isinstance(raw, dict) or not raw:
        raise PipelineConfigError("pipeline registry must be a non-empty JSON object")

    result: dict[str, PipelineDefinition] = {}
    for name, value in raw.items():
        context = f"pipeline={name}"
        if not isinstance(name, str) or not name.strip():
            raise PipelineConfigError("pipeline names must be non-empty strings")
        if not isinstance(value, dict):
            raise PipelineConfigError(f"{context}: definition must be an object")

        components = value.get("components")
        if not isinstance(components, list) or not components or any(
            not isinstance(component, str) or not component.strip() for component in components
        ):
            raise PipelineConfigError(f"{context}: components must be a non-empty string array")
        if len(set(components)) != len(components):
            raise PipelineConfigError(f"{context}: duplicate components are not allowed")

        raw_tests = value.get(
            "operational_tests",
            value.get("operational_test", value.get("tests", [])),
        )
        # Accept the compact registry form from the operational contract:
        # "operational_test": ["python3", "tests/..."].
        if isinstance(raw_tests, list) and raw_tests and all(
            isinstance(item, str) for item in raw_tests
        ):
            raw_tests = [{"command": raw_tests, "dry_run_supported": True}]
        if not isinstance(raw_tests, list):
            raise PipelineConfigError(f"{context}: operational_tests must be an array")
        tests: list[OperationalTest] = []
        seen_test_names: set[str] = set()
        for index, raw_test in enumerate(raw_tests):
            test_context = f"{context} operational_tests[{index}]"
            if not isinstance(raw_test, dict):
                raise PipelineConfigError(f"{test_context}: definition must be an object")
            test_name = raw_test.get("name", f"test-{index + 1}")
            if not isinstance(test_name, str) or not test_name.strip():
                raise PipelineConfigError(f"{test_context}: name must be a non-empty string")
            if test_name in seen_test_names:
                raise PipelineConfigError(f"{context}: duplicate operational test name={test_name}")
            seen_test_names.add(test_name)
            tests.append(
                OperationalTest(
                    name=test_name,
                    argv=_argv(raw_test.get("command"), "command", test_context),
                    timeout_seconds=_positive_number(
                        raw_test.get("timeout_seconds", value.get("timeout_seconds", 300)),
                        "timeout_seconds",
                        test_context,
                    ),
                    dry_run_supported=raw_test.get("dry_run_supported", False),
                )
            )
            if not isinstance(tests[-1].dry_run_supported, bool):
                raise PipelineConfigError(f"{test_context}: dry_run_supported must be boolean")

        result[name] = PipelineDefinition(
            name=name,
            components=tuple(components),
            operational_tests=tuple(tests),
            timeout_seconds=_positive_number(
                value.get("timeout_seconds", 300), "timeout_seconds", context
            ),
        )
    return result


def load_component_names(path: Path) -> set[str]:
    return set(load_component_registry(path))


def load_component_registry(path: Path) -> dict[str, dict[str, Any]]:
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PipelineConfigError(f"cannot load component registry {path}: {exc}") from exc
    if not isinstance(raw, dict) or not raw:
        raise PipelineConfigError("component registry must be a non-empty JSON object")
    for name, definition in raw.items():
        if not isinstance(name, str) or not name.strip() or not isinstance(definition, dict):
            raise PipelineConfigError("component registry contains an invalid definition")
        dependencies = definition.get("dependencies", [])
        if not isinstance(dependencies, list) or any(
            not isinstance(dependency, str) or not dependency.strip()
            for dependency in dependencies
        ):
            raise PipelineConfigError(f"component={name}: dependencies must be a string array")
    return raw


def resolve_pipeline_names(
    registry: Mapping[str, PipelineDefinition], requested: Iterable[str]
) -> list[str]:
    names = list(dict.fromkeys(requested))
    if not names:
        raise PipelineConfigError("at least one pipeline is required")
    for name in names:
        if name not in registry:
            raise PipelineConfigError(f"unknown pipeline={name}")
    return names


def resolve_components(
    registry: Mapping[str, PipelineDefinition],
    pipeline_names: Sequence[str],
    available: set[str] | Mapping[str, Mapping[str, Any]],
) -> list[str]:
    result: list[str] = []
    seen: set[str] = set()
    visiting: set[str] = set()
    available_names = set(available)
    dependencies = available if isinstance(available, Mapping) else {}

    def visit(component: str, pipeline_name: str) -> None:
        if component not in available_names:
            raise PipelineConfigError(
                f"pipeline={pipeline_name}: unknown component={component}"
            )
        if component in seen:
            return
        if component in visiting:
            raise PipelineConfigError(f"component dependency cycle at {component}")
        visiting.add(component)
        for dependency in dependencies.get(component, {}).get("dependencies", []):
            visit(dependency, pipeline_name)
        visiting.remove(component)
        seen.add(component)
        result.append(component)

    for pipeline_name in pipeline_names:
        for component in registry[pipeline_name].components:
            visit(component, pipeline_name)
    return result


def _text(value: str | bytes | None) -> str:
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="replace")
    return value


def run_process(argv: Sequence[str], timeout_seconds: float, cwd: Path) -> ProcessResult:
    """Run one command in an isolated process group with bounded cleanup."""
    started = time.monotonic()
    try:
        process = subprocess.Popen(
            list(argv),
            cwd=str(cwd),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            start_new_session=True,
        )
        stdout, stderr = process.communicate(timeout=max(0.001, timeout_seconds))
    except OSError as exc:
        return ProcessResult(
            status="FAIL",
            exit_code=127,
            duration_ms=int((time.monotonic() - started) * 1000),
            stderr=str(exc),
        )
    except subprocess.TimeoutExpired as exc:
        try:
            os.killpg(process.pid, signal.SIGTERM)
            process.communicate(timeout=1)
        except (ProcessLookupError, subprocess.TimeoutExpired):
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.communicate()
        return ProcessResult(
            status="TIMEOUT",
            exit_code=None,
            duration_ms=int((time.monotonic() - started) * 1000),
            stdout=_text(exc.stdout),
            stderr=_text(exc.stderr),
            timed_out=True,
        )
    return ProcessResult(
        status="PASS" if process.returncode == 0 else "FAIL",
        exit_code=process.returncode,
        duration_ms=int((time.monotonic() - started) * 1000),
        stdout=stdout,
        stderr=stderr,
    )


def _result_dict(result: ProcessResult, command: Sequence[str]) -> dict[str, Any]:
    return {
        "command": shlex.join(command),
        "status": result.status,
        "exit_code": result.exit_code,
        "duration_ms": result.duration_ms,
        "timed_out": result.timed_out,
    }


def _write_report(path: Path, report: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(report, handle, indent=2, ensure_ascii=False)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def _read_json_object(path: Path) -> dict[str, Any] | None:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


def _append_dry_run(argv: Sequence[str]) -> tuple[str, ...]:
    if "--dry-run" in argv:
        return tuple(argv)
    return tuple(argv) + ("--dry-run",)


def run_pipeline(
    pipeline_registry: Mapping[str, PipelineDefinition],
    component_names: set[str],
    requested: Sequence[str],
    *,
    component_runner: Path,
    repo_root: Path,
    report_path: Path | None = None,
    dry_run: bool = False,
    runner: Callable[[Sequence[str], float, Path], ProcessResult] = run_process,
) -> tuple[dict[str, Any], int]:
    """Run selected pipelines and return their JSON report plus exit code."""
    pipeline_names = resolve_pipeline_names(pipeline_registry, requested)
    components = resolve_components(pipeline_registry, pipeline_names, component_names)
    started = time.monotonic()
    started_at = _now_utc()
    component_report: dict[str, Any] | None = None
    component_result: ProcessResult | None = None
    pipeline_reports: dict[str, dict[str, Any]] = {}
    overall_code = 0
    global_deadline = started + min(
        pipeline_registry[name].timeout_seconds for name in pipeline_names
    )

    with tempfile.TemporaryDirectory(prefix="verify-pipeline-") as temporary:
        component_report_path = Path(temporary) / "components.json"
        component_command = [
            sys.executable,
            str(component_runner),
            *components,
            "--report",
            str(component_report_path),
        ]
        if dry_run:
            component_command.append("--dry-run")

        component_result = runner(
            component_command,
            max(0.001, global_deadline - time.monotonic()),
            repo_root,
        )
        component_report = _read_json_object(component_report_path)
        if component_result.status == "PASS" and time.monotonic() > global_deadline:
            component_result = ProcessResult(
                status="TIMEOUT",
                exit_code=None,
                duration_ms=component_result.duration_ms,
                stdout=component_result.stdout,
                stderr=component_result.stderr,
                timed_out=True,
            )
        report_components = (
            component_report.get("resolved_components")
            if isinstance(component_report, dict)
            else None
        )
        report_component_statuses = (
            component_report.get("components", {})
            if isinstance(component_report, dict)
            else {}
        )
        component_report_valid = (
            isinstance(component_report, dict)
            and component_report.get("final") == "PASS"
            and isinstance(report_components, list)
            and len(report_components) == len(components)
            and set(report_components) == set(components)
            and isinstance(report_component_statuses, dict)
            and all(
                isinstance(report_component_statuses.get(name), dict)
                and report_component_statuses[name].get("status") == "PASS"
                for name in components
            )
        )
        if component_result.status == "PASS" and not component_report_valid:
            components_status = "FAIL"
            overall_code = EXIT_FAILURE
        else:
            components_status = "PASS" if component_result.status == "PASS" else component_result.status
        if components_status == "TIMEOUT":
            overall_code = EXIT_TIMEOUT
        elif components_status != "PASS":
            overall_code = EXIT_FAILURE
        resolved_components = (
            component_report.get("resolved_components", components)
            if isinstance(component_report, dict)
            else components
        )

        for pipeline_name in pipeline_names:
            definition = pipeline_registry[pipeline_name]
            pipeline_started = time.monotonic()
            pipeline_deadline = pipeline_started + definition.timeout_seconds
            test_results: list[dict[str, Any]] = []
            status = components_status
            if status == "PASS":
                for test in definition.operational_tests:
                    command = test.argv
                    if dry_run:
                        planned_command = _append_dry_run(command) if test.dry_run_supported else tuple(command)
                        test_results.append(
                            {
                                **_result_dict(ProcessResult("PLANNED", 0, 0), planned_command),
                                "dry_run_supported": test.dry_run_supported,
                            }
                        )
                        continue

                    remaining = min(
                        test.timeout_seconds,
                        pipeline_deadline - time.monotonic(),
                        global_deadline - time.monotonic(),
                    )
                    if remaining <= 0:
                        test_result = ProcessResult("TIMEOUT", None, 0, timed_out=True)
                    else:
                        test_result = runner(command, remaining, repo_root)
                    test_results.append(
                        {
                            **_result_dict(test_result, command),
                            "dry_run_supported": test.dry_run_supported,
                        }
                    )
                    if test_result.status != "PASS":
                        status = test_result.status
                        break

            if status == "PASS" and any(item["status"] == "TIMEOUT" for item in test_results):
                status = "TIMEOUT"
            if status == "PASS" and any(item["status"] == "FAIL" for item in test_results):
                status = "FAIL"
            if status == "TIMEOUT":
                overall_code = EXIT_TIMEOUT
            elif status != "PASS" and overall_code == 0:
                overall_code = EXIT_FAILURE
            pipeline_reports[pipeline_name] = {
                "status": status,
                "duration_ms": int((time.monotonic() - pipeline_started) * 1000),
                "components": list(definition.components),
                "operational_tests": test_results,
            }

    final = "PASS" if overall_code == 0 else "FAIL"
    report: dict[str, Any] = {
        "schema_version": 1,
        "requested": pipeline_names,
        "resolved_components": resolved_components,
        "started_at": started_at,
        "finished_at": _now_utc(),
        "git_sha": _git_sha(repo_root),
        "duration_ms": int((time.monotonic() - started) * 1000),
        "dry_run": dry_run,
        "final": final,
        "components": {
            "status": components_status,
            "command": shlex.join(component_command),
            "exit_code": component_result.exit_code if component_result else None,
            "duration_ms": component_result.duration_ms if component_result else 0,
            "timed_out": component_result.timed_out if component_result else False,
            "report": component_report,
        },
        "pipelines": pipeline_reports,
    }
    if report_path is not None:
        _write_report(report_path, report)
    return report, overall_code


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("pipelines", nargs="+", help="registered pipeline names")
    parser.add_argument("--registry", type=Path, default=DEFAULT_REGISTRY)
    parser.add_argument("--component-registry", type=Path, default=DEFAULT_COMPONENT_REGISTRY)
    parser.add_argument("--component-runner", type=Path, default=DEFAULT_COMPONENT_RUNNER)
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT)
    parser.add_argument("--repo-root", type=Path, default=None)
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    root = (args.repo_root or Path.cwd()).resolve()
    registry_path = args.registry if args.registry.is_absolute() else root / args.registry
    component_registry_path = (
        args.component_registry
        if args.component_registry.is_absolute()
        else root / args.component_registry
    )
    component_runner = args.component_runner if args.component_runner.is_absolute() else root / args.component_runner
    report_path = args.report if args.report.is_absolute() else root / args.report
    try:
        pipelines = load_pipeline_registry(registry_path)
        components = load_component_registry(component_registry_path)
        report, code = run_pipeline(
            pipelines,
            components,
            args.pipelines,
            component_runner=component_runner,
            repo_root=root,
            report_path=report_path,
            dry_run=args.dry_run,
        )
    except PipelineConfigError as exc:
        print(f"VERIFY_PIPELINE_CONFIG_ERROR {exc}", file=sys.stderr)
        return EXIT_CONFIG_ERROR
    except OSError as exc:
        print(f"VERIFY_PIPELINE_REPORT_ERROR {exc}", file=sys.stderr)
        return EXIT_FAILURE

    print(
        f"verify-pipeline pipelines={','.join(report['requested'])} "
        f"components={','.join(report['resolved_components'])} "
        f"final={report['final']} duration_ms={report['duration_ms']} report={report_path}"
    )
    return code


if __name__ == "__main__":
    raise SystemExit(main())
