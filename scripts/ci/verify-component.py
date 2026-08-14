#!/usr/bin/env python3
"""Run one or more registered component verification suites.

The registry is deliberately declarative.  This runner owns the mechanics:
loading and validating the registry, resolving the dependency DAG, deduplicating
commands, enforcing component timeouts, and publishing one machine-readable
report.

Examples:
    python3 scripts/ci/verify-component.py stock
    python3 scripts/ci/verify-component.py clips --race
    python3 scripts/ci/verify-component.py stock clips --dry-run
    python3 scripts/ci/verify-component.py --all --dry-run

The default mode is ``fast``.  ``--race`` adds ``-race`` to Go tests only when
the component opts in with ``race_enabled``.  Live tests are never executed by
default; use ``--include-live`` explicitly for those future registry entries.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from verify_runtime import (
    git_sha as _runtime_git_sha,
    now_utc as _runtime_now_utc,
    run_bounded_process,
    text_output as _runtime_text_output,
    write_json_report,
)
from typing import Any, Callable, Iterable, Mapping, Sequence


DEFAULT_TIMEOUT_SECONDS = 300
DEFAULT_REGISTRY = Path("config/verify-components.json")
DEFAULT_REPORT = Path("artifacts/verify/latest.json")
EXIT_CONFIG_ERROR = 2
EXIT_FAILURE = 1
EXIT_TIMEOUT = 124


class RegistryError(ValueError):
    """The registry cannot be safely executed."""


class VerificationError(RuntimeError):
    """A verification command failed or timed out."""


@dataclass(frozen=True)
class Command:
    argv: tuple[str, ...]
    kind: str
    source: str

    @property
    def key(self) -> tuple[str, ...]:
        return self.argv

    @property
    def display(self) -> str:
        return shlex.join(self.argv)


@dataclass(frozen=True)
class CommandResult:
    status: str
    exit_code: int | None
    duration_ms: int
    timed_out: bool = False

    def as_dict(self) -> dict[str, Any]:
        return {
            "status": self.status,
            "exit_code": self.exit_code,
            "duration_ms": self.duration_ms,
            "timed_out": self.timed_out,
        }


@dataclass(frozen=True)
class Execution:
    result: CommandResult
    stdout: str = ""
    stderr: str = ""


@dataclass
class ComponentRun:
    name: str
    dependencies: list[str]
    commands: list[Command]
    timeout_seconds: float
    blocked_by: list[str]
    status: str = "PENDING"
    duration_ms: int = 0
    command_results: list[dict[str, Any]] | None = None
    skipped_live: list[str] | None = None
    race_skipped: bool = False

    def as_dict(self) -> dict[str, Any]:
        command_results = self.command_results or []
        return {
            "status": self.status,
            "duration_ms": self.duration_ms,
            "timeout_seconds": self.timeout_seconds,
            "commands": [command.display for command in self.commands],
            "packages": sum(command.kind == "go" for command in self.commands),
            "command_results": command_results,
            "dependencies": self.dependencies,
            "blocked_by": self.blocked_by,
            "skipped_live": self.skipped_live or [],
            "race_skipped": self.race_skipped,
        }


# Do not put command output in the JSON artifact: tool output can contain
# credentials, cookies, or other sensitive data.  This redaction is only for
# concise diagnostics printed to the terminal after a failure.
_SECRET_PATTERNS = (
    re.compile(r"(?i)(VELOX_(?:ADMIN|WORKER)_TOKEN\s*(?:=|:)\s*)([^\s,;]+)"),
    re.compile(r"(?i)(Bearer\s+)([^\s]+)"),
    re.compile(r"(?i)AKIA[0-9A-Z]{16}"),
    re.compile(r"(?i)ghp_[A-Za-z0-9]{20,}"),
)


def redact(text: str) -> str:
    """Redact common credential-shaped values from terminal diagnostics."""
    result = text
    for pattern in _SECRET_PATTERNS:
        result = pattern.sub(lambda match: f"{match.group(1)}REDACTED" if match.lastindex else "REDACTED", result)
    return result


def _require_list(value: Any, field: str, component: str) -> list[Any]:
    if value is None:
        return []
    if not isinstance(value, list):
        raise RegistryError(f"component={component}: {field} must be an array")
    return value


def load_registry(path: Path) -> dict[str, dict[str, Any]]:
    """Load and validate the component registry before executing anything."""
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise RegistryError(f"cannot read registry {path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise RegistryError(f"invalid JSON in registry {path}: {exc}") from exc

    if not isinstance(raw, dict) or not raw:
        raise RegistryError("registry must be a non-empty JSON object")

    registry: dict[str, dict[str, Any]] = {}
    for name, value in raw.items():
        if not isinstance(name, str) or not name.strip():
            raise RegistryError("component names must be non-empty strings")
        if not isinstance(value, dict):
            raise RegistryError(f"component={name}: definition must be an object")

        paths = _require_list(value.get("paths"), "paths", name)
        packages = _require_list(value.get("go_packages"), "go_packages", name)
        if not paths:
            raise RegistryError(f"component={name}: paths must not be empty")
        for field, entries in (("paths", paths), ("go_packages", packages)):
            if any(not isinstance(entry, str) or not entry.strip() for entry in entries):
                raise RegistryError(f"component={name}: {field} entries must be non-empty strings")

        dependencies = _require_list(value.get("dependencies"), "dependencies", name)
        if any(not isinstance(dep, str) or not dep.strip() for dep in dependencies):
            raise RegistryError(f"component={name}: dependencies must contain strings")
        if len(set(dependencies)) != len(dependencies):
            raise RegistryError(f"component={name}: duplicate dependencies are not allowed")

        timeout = value.get("timeout_seconds", DEFAULT_TIMEOUT_SECONDS)
        if isinstance(timeout, bool) or not isinstance(timeout, (int, float)) or timeout <= 0:
            raise RegistryError(f"component={name}: timeout_seconds must be positive")
        race_timeout = value.get("race_timeout_seconds", timeout)
        if isinstance(race_timeout, bool) or not isinstance(race_timeout, (int, float)) or race_timeout <= 0:
            raise RegistryError(f"component={name}: race_timeout_seconds must be positive")

        race_enabled = value.get("race_enabled", False)
        if not isinstance(race_enabled, bool):
            raise RegistryError(f"component={name}: race_enabled must be boolean")
        utility = value.get("utility", False)
        if not isinstance(utility, bool):
            raise RegistryError(f"component={name}: utility must be boolean")

        # Validate command-bearing fields now, rather than after dependencies
        # have already started running.
        for field in ("node_tests", "python_tests", "live_tests"):
            _validate_command_entries(_require_list(value.get(field), field, name), field, name)

        registry[name] = dict(value)
        registry[name]["paths"] = paths
        registry[name]["go_packages"] = packages
        registry[name]["dependencies"] = dependencies
        registry[name]["timeout_seconds"] = float(timeout)
        registry[name]["race_timeout_seconds"] = float(race_timeout)
        registry[name]["race_enabled"] = race_enabled
        registry[name]["utility"] = utility

    for name, definition in registry.items():
        for dependency in definition["dependencies"]:
            if dependency not in registry:
                raise RegistryError(f"component={name}: unknown dependency={dependency}")
            if dependency == name:
                raise RegistryError(f"component={name}: self dependency is not allowed")
    # Validate the complete DAG at registry-load time.  A cycle must not remain
    # latent merely because the current invocation did not request that branch.
    resolve_components(registry, list(registry))
    return registry


def _validate_command_entries(entries: list[Any], field: str, component: str) -> None:
    for index, entry in enumerate(entries):
        if isinstance(entry, str):
            try:
                argv = shlex.split(entry)
            except ValueError as exc:
                raise RegistryError(
                    f"component={component}: {field}[{index}] has invalid shell quoting: {exc}"
                ) from exc
            if not entry.strip() or not argv:
                raise RegistryError(f"component={component}: {field}[{index}] is empty")
        elif isinstance(entry, list):
            if not entry or any(not isinstance(arg, str) or not arg for arg in entry):
                raise RegistryError(f"component={component}: {field}[{index}] must be a non-empty argv array")
        else:
            raise RegistryError(
                f"component={component}: {field}[{index}] must be a command string or argv array"
            )


def resolve_components(registry: Mapping[str, Mapping[str, Any]], requested: Iterable[str]) -> list[str]:
    """Return a stable dependency-first, duplicate-free component order."""
    names = list(dict.fromkeys(requested))
    if not names:
        raise RegistryError("at least one component is required")

    state: dict[str, int] = {}
    ordered: list[str] = []
    stack: list[str] = []

    def visit(name: str) -> None:
        if name not in registry:
            raise RegistryError(f"unknown component={name}")
        current = state.get(name, 0)
        if current == 2:
            return
        if current == 1:
            cycle_start = stack.index(name)
            cycle = stack[cycle_start:] + [name]
            raise RegistryError(f"dependency cycle: {' -> '.join(cycle)}")
        state[name] = 1
        stack.append(name)
        for dependency in registry[name].get("dependencies", []):
            visit(dependency)
        stack.pop()
        state[name] = 2
        ordered.append(name)

    for name in names:
        visit(name)
    return ordered


def _command_argv(entry: Any, field: str, component: str, index: int) -> tuple[str, ...]:
    if isinstance(entry, str):
        try:
            argv = tuple(shlex.split(entry))
        except ValueError as exc:
            raise RegistryError(
                f"component={component}: {field}[{index}] has invalid shell quoting: {exc}"
            ) from exc
    else:
        argv = tuple(entry)
    if not argv:
        raise RegistryError(f"component={component}: {field}[{index}] is empty")
    return argv


def build_commands(
    component: str,
    definition: Mapping[str, Any],
    mode: str,
    include_live: bool,
) -> tuple[list[Command], list[str], bool]:
    """Build a stable, de-duplicated command list for one component."""
    commands: list[Command] = []
    seen: set[tuple[str, ...]] = set()
    race_skipped = mode == "race" and not definition["race_enabled"]

    for package in definition["go_packages"]:
        argv = ["go", "test"]
        if mode == "race" and definition["race_enabled"]:
            argv.append("-race")
        argv.append(package)
        command = Command(tuple(argv), "go", package)
        if command.key not in seen:
            seen.add(command.key)
            commands.append(command)

    for field, kind in (("node_tests", "node"), ("python_tests", "python")):
        for index, entry in enumerate(definition.get(field, [])):
            argv = _command_argv(entry, field, component, index)
            command = Command(argv, kind, f"{field}[{index}]")
            if command.key not in seen:
                seen.add(command.key)
                commands.append(command)

    skipped_live: list[str] = []
    for index, entry in enumerate(definition.get("live_tests", [])):
        argv = _command_argv(entry, "live_tests", component, index)
        command = Command(argv, "live", f"live_tests[{index}]")
        if include_live:
            if command.key not in seen:
                seen.add(command.key)
                commands.append(command)
        else:
            skipped_live.append(command.display)
    return commands, skipped_live, race_skipped


def _text_output(value: str | bytes | None) -> str:
    """Compatibility wrapper for callers that used the old local helper."""
    return _runtime_text_output(value)


def _run_subprocess(argv: Sequence[str], timeout_seconds: float, cwd: Path) -> Execution:
    """Adapt the shared bounded process result to the component contract."""
    result = run_bounded_process(argv, timeout_seconds, cwd)
    return Execution(
        CommandResult(result.status, result.exit_code, result.duration_ms, result.timed_out),
        result.stdout,
        result.stderr,
    )


def _command_result(command: Command, execution: Execution, reused: bool = False) -> dict[str, Any]:
    result = execution.result.as_dict()
    result.update({"command": command.display, "kind": command.kind, "source": command.source})
    if reused:
        result["reused"] = True
    return result


def _now_utc() -> str:
    """Compatibility wrapper for the canonical report timestamp."""
    return _runtime_now_utc()


def _component_jobs(ordered: Sequence[str]) -> int:
    """Concurrent component workers, bounded by the dependency-ordered set."""
    try:
        jobs = int(os.environ.get("VERIFY_COMPONENT_JOBS", "4"))
    except ValueError:
        jobs = 4
    return max(1, min(jobs, len(ordered)))


def write_report(path: Path, report: Mapping[str, Any]) -> None:
    """Write reports through the shared atomic JSON writer."""
    write_json_report(path, report)


def run_components(
    registry: Mapping[str, Mapping[str, Any]],
    requested: Sequence[str],
    mode: str = "fast",
    repo_root: Path | None = None,
    report_path: Path | None = None,
    include_live: bool = False,
    dry_run: bool = False,
    runner: Callable[[Sequence[str], float, Path], Execution] = _run_subprocess,
) -> tuple[dict[str, Any], int]:
    """Execute resolved components and return ``(report, exit_code)``."""
    if mode not in {"fast", "race"}:
        raise RegistryError(f"unsupported mode={mode}")
    root = (repo_root or Path.cwd()).resolve()
    ordered = resolve_components(registry, requested)
    started_at = _now_utc()
    started = time.monotonic()
    executions: dict[tuple[str, ...], Execution] = {}
    execution_running: set[tuple[str, ...]] = set()
    execution_ready: dict[tuple[str, ...], threading.Event] = {}
    component_runs: dict[str, ComponentRun] = {}
    diagnostics: list[str] = []
    completed: set[str] = set()
    next_index = 0
    state_lock = threading.Condition()

    def process_component(name: str) -> None:
        definition = registry[name]
        commands, skipped_live, race_skipped = build_commands(name, definition, mode, include_live)
        component = ComponentRun(
            name=name,
            dependencies=list(definition["dependencies"]),
            commands=commands,
            timeout_seconds=(
                float(definition.get("race_timeout_seconds", definition["timeout_seconds"]))
                if mode == "race"
                else float(definition["timeout_seconds"])
            ),
            blocked_by=[],
            command_results=[],
            skipped_live=skipped_live,
            race_skipped=race_skipped,
        )
        component_runs[name] = component

        failed_dependencies = [
            dependency
            for dependency in component.dependencies
            if component_runs[dependency].status != "PASS"
        ]
        if failed_dependencies:
            component.blocked_by = failed_dependencies
            component.status = "BLOCKED"
            component.command_results = []
            diagnostics.append(f"component={name} blocked_by={','.join(failed_dependencies)}")
            with state_lock:
                completed.add(name)
                state_lock.notify_all()
            return

        component_started = time.monotonic()
        timeout_seconds = (
            float(definition.get("race_timeout_seconds", definition["timeout_seconds"]))
            if mode == "race"
            else float(definition["timeout_seconds"])
        )
        deadline = component_started + timeout_seconds
        for command in commands:
            # A shared command may have been executed by a dependency.  Its
            # result is reusable, but the dependent component still has its
            # own bounded execution budget.  Do not let deduplication turn an
            # expired component into a false PASS.
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                execution = Execution(CommandResult("TIMEOUT", None, 0, timed_out=True))
                component.command_results.append(_command_result(command, execution))
                component.status = "TIMEOUT"
                diagnostics.append(f"component={name} command={command.display} status=TIMEOUT")
                break

            if dry_run:
                with state_lock:
                    if command.key in executions:
                        execution = executions[command.key]
                        reused = True
                    else:
                        execution = Execution(CommandResult("PASS", 0, 0))
                        executions[command.key] = execution
                        reused = False
            else:
                with state_lock:
                    if command.key in executions:
                        execution = executions[command.key]
                        reused = True
                        ready = None
                    elif command.key in execution_running:
                        execution = None
                        reused = True
                        ready = execution_ready[command.key]
                    else:
                        execution_running.add(command.key)
                        execution_ready[command.key] = threading.Event()
                        execution = None
                        reused = False
                        ready = None
                if execution is None:
                    if ready is not None:
                        # Another component is executing the shared command;
                        # wait for its result instead of running it twice.
                        ready.wait()
                        with state_lock:
                            execution = executions[command.key]
                    else:
                        try:
                            execution = runner(command.argv, remaining, root)
                        except Exception as exc:  # noqa: BLE001 - fail closed, never hang peers
                            execution = Execution(CommandResult("FAIL", 127, 0), stderr=str(exc))
                        with state_lock:
                            executions[command.key] = execution
                            execution_running.discard(command.key)
                            execution_ready[command.key].set()

            if execution.result.status == "PASS" and time.monotonic() > deadline:
                execution = Execution(
                    CommandResult(
                        "TIMEOUT",
                        None,
                        execution.result.duration_ms,
                        timed_out=True,
                    ),
                    execution.stdout,
                    execution.stderr,
                )

            with state_lock:
                component.command_results.append(_command_result(command, execution, reused))
                if execution.result.status != "PASS":
                    component.status = execution.result.status
                    diagnostic = f"component={name} command={command.display} status={execution.result.status}"
                    diagnostics.append(diagnostic)
                    if execution.stderr:
                        diagnostics.append(redact(execution.stderr[-2000:]).strip())
                    break

        if component.status == "PENDING":
            component.status = "PASS"
        component.duration_ms = int((time.monotonic() - component_started) * 1000)
        with state_lock:
            completed.add(name)
            state_lock.notify_all()

    def worker() -> None:
        nonlocal next_index
        while True:
            with state_lock:
                while True:
                    index = next_index
                    if index >= len(ordered):
                        return
                    name = ordered[index]
                    if all(
                        dependency in completed
                        for dependency in registry[name].get("dependencies", [])
                    ):
                        next_index = index + 1
                        break
                    state_lock.wait()
            try:
                process_component(name)
            except Exception as exc:  # noqa: BLE001 - fail closed, never hang peers
                with state_lock:
                    component_runs.setdefault(
                        name,
                        ComponentRun(
                            name=name,
                            dependencies=list(registry[name].get("dependencies", [])),
                            commands=[],
                            timeout_seconds=float(
                                registry[name].get("timeout_seconds", DEFAULT_TIMEOUT_SECONDS)
                            ),
                            blocked_by=[],
                        ),
                    )
                    component_runs[name].status = "FAIL"
                    diagnostics.append(f"component={name} crashed: {exc}")
                    completed.add(name)
                    state_lock.notify_all()

    jobs = _component_jobs(ordered)
    if jobs <= 1:
        for name in ordered:
            process_component(name)
    else:
        with ThreadPoolExecutor(max_workers=jobs) as pool:
            futures = [pool.submit(worker) for _ in range(jobs)]
            for future in futures:
                future.result()

    final_status = "PASS" if all(component_runs[name].status == "PASS" for name in ordered) else "FAIL"
    finished_at = _now_utc()
    report: dict[str, Any] = {
        "schema_version": 1,
        "mode": mode,
        "requested": list(dict.fromkeys(requested)),
        "requested_components": list(dict.fromkeys(requested)),
        "resolved_components": ordered,
        "started_at": started_at,
        "finished_at": finished_at,
        "git_sha": _git_sha(root),
        "duration_ms": int((time.monotonic() - started) * 1000),
        "components": {name: component_runs[name].as_dict() for name in ordered},
        "skipped": [
            name for name in registry if name not in ordered
        ],
        "final": final_status,
        "dry_run": dry_run,
        "include_live": include_live,
    }
    if diagnostics:
        report["diagnostics"] = diagnostics

    if report_path is not None:
        write_report(report_path, report)

    if final_status == "PASS":
        return report, 0
    if any(component_runs[name].status == "TIMEOUT" for name in ordered):
        return report, EXIT_TIMEOUT
    return report, EXIT_FAILURE


def _git_sha(root: Path) -> str | None:
    """Compatibility wrapper for the shared Git metadata helper."""
    return _runtime_git_sha(root)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("components", nargs="*", help="registered component names")
    parser.add_argument(
        "--all",
        action="store_true",
        help="verify every component declared in the registry",
    )
    parser.add_argument("--registry", type=Path, default=DEFAULT_REGISTRY, help="component registry JSON")
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT, help="JSON report destination")
    parser.add_argument("--repo-root", type=Path, default=None, help="working tree used for commands")
    parser.add_argument("--mode", choices=("fast", "race"), default="fast")
    parser.add_argument("--race", action="store_true", help="shortcut for --mode race")
    parser.add_argument("--include-live", action="store_true", help="execute registered live tests")
    parser.add_argument("--dry-run", action="store_true", help="plan commands without executing them")
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    mode = "race" if args.race else args.mode
    registry_path = args.registry
    if not registry_path.is_absolute():
        registry_path = Path.cwd() / registry_path
    report_path = args.report
    if not report_path.is_absolute():
        report_path = Path.cwd() / report_path
    repo_root = args.repo_root or Path.cwd()
    try:
        registry = load_registry(registry_path)
        requested = list(registry) if args.all else args.components
        if not requested:
            raise RegistryError("provide component names or use --all")
        report, exit_code = run_components(
            registry,
            requested,
            mode=mode,
            repo_root=repo_root,
            report_path=report_path,
            include_live=args.include_live,
            dry_run=args.dry_run,
        )
    except RegistryError as exc:
        write_report(
            report_path,
            {
                "schema_version": 1,
                "mode": mode,
                "requested": list(args.components),
                "requested_components": list(args.components),
                "resolved_components": [],
                "started_at": _now_utc(),
                "finished_at": _now_utc(),
                "git_sha": _git_sha(repo_root.resolve()),
                "components": {},
                "skipped": [],
                "final": "CONFIG_ERROR",
                "error": str(exc),
            },
        )
        print(f"VERIFY_COMPONENT_CONFIG_ERROR {exc}", file=sys.stderr)
        return EXIT_CONFIG_ERROR

    print(
        f"verify-component mode={report['mode']} components={','.join(report['resolved_components'])} "
        f"final={report['final']} duration_ms={report['duration_ms']} report={report_path}"
    )
    for diagnostic in report.get("diagnostics", []):
        if diagnostic:
            print(diagnostic, file=sys.stderr)
    if exit_code == EXIT_TIMEOUT:
        for name, result in report["components"].items():
            if result["status"] == "TIMEOUT":
                timeout_key = "race_timeout_seconds" if report["mode"] == "race" else "timeout_seconds"
                timeout = registry[name][timeout_key]
                print(
                    f"VERIFY_COMPONENT_TIMEOUT component={name} duration={int(timeout)}s",
                    file=sys.stderr,
                )
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
