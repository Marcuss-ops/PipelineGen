"""Component execution engine: bounded processes, cache replay, report."""
from __future__ import annotations

import os
import re
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any, Callable, Mapping, Sequence

from verify_component_cache import (
    cache_hit_entry,
    cache_root,
    cache_summary,
    missing_required_artifacts,
    read_cache_entry,
    store_cache_pass,
    write_cache_entry,
)
from verify_component_core import (
    Command,
    CommandResult,
    ComponentRun,
    Execution,
    EXIT_FAILURE,
    EXIT_TIMEOUT,
    FINGERPRINT_SCHEMA_VERSION,
    RegistryError,
)
from verify_component_fingerprint import component_fingerprint, detect_toolchain
from verify_component_registry import build_commands, resolve_components
from verify_runtime import (
    git_sha as _runtime_git_sha,
    now_utc as _runtime_now_utc,
    run_bounded_process,
    text_output as _runtime_text_output,
    write_json_report,
)

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
    toolchain: Mapping[str, str] | None = None,
) -> tuple[dict[str, Any], int]:
    """Execute resolved components and return ``(report, exit_code)``."""
    if mode not in {"fast", "race"}:
        raise RegistryError(f"unsupported mode={mode}")
    root = (repo_root or Path.cwd()).resolve()
    ordered = resolve_components(registry, requested)
    toolchain_versions = (
        dict(toolchain)
        if toolchain is not None
        else (
            detect_toolchain(root)
            if any(registry[name].get("cacheable") for name in ordered)
            else {}
        )
    )
    fingerprint_memo: dict[tuple[str, str, bool], str] = {}
    fingerprints = {
        name: component_fingerprint(
            registry, name, mode, include_live, root, toolchain_versions, fingerprint_memo
        )
        if registry[name].get("cacheable")
        else None
        for name in ordered
    }
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
            fingerprint=fingerprints[name],
        )
        component_runs[name] = component

        failed_dependencies = [
            dependency
            for dependency in component.dependencies
            if component_runs[dependency].status not in {"PASS", "CACHED_PASS"}
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

        # Content-addressed cache hit: a previous run already passed these exact
        # inputs (same fingerprint, PASS record, compatible schema), so skip
        # re-executing the component's commands.  CACHED_PASS aggregates as PASS.
        # A gate whose required output artifact is missing is a miss: the cache
        # must not hide a missing artifact that later steps depend on.
        cached_entry = None
        if not dry_run and definition.get("cacheable") and fingerprints.get(name):
            missing = missing_required_artifacts(root, definition)
            if missing:
                diagnostics.append(
                    f"component={name} cache_miss missing_artifact={','.join(missing)}"
                )
            else:
                cached_entry = cache_hit_entry(root, name, fingerprints[name])
        if cached_entry is not None:
            component.cache_hit = True
            component.status = "CACHED_PASS"
            component.original_duration_ms = int(cached_entry.get("duration_ms") or 0)
            component.command_results = []
            diagnostics.append(
                f"component={name} status=CACHED_PASS "
                f"original_duration_ms={component.original_duration_ms}"
            )
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
        if (
            component.status == "PASS"
            and not dry_run
            and definition.get("cacheable")
            and fingerprints.get(name)
        ):
            store_cache_pass(
                root, name, fingerprints[name], component.duration_ms, mode, toolchain_versions
            )
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

    final_status = "PASS" if all(
        component_runs[name].status in {"PASS", "CACHED_PASS"} for name in ordered
    ) else "FAIL"
    finished_at = _now_utc()
    components = {name: component_runs[name].as_dict() for name in ordered}
    report: dict[str, Any] = {
        "schema_version": 1,
        "cache_schema_version": FINGERPRINT_SCHEMA_VERSION,
        "mode": mode,
        "requested": list(dict.fromkeys(requested)),
        "requested_components": list(dict.fromkeys(requested)),
        "resolved_components": ordered,
        "started_at": started_at,
        "finished_at": finished_at,
        "git_sha": _git_sha(root),
        "duration_ms": int((time.monotonic() - started) * 1000),
        "components": components,
        "cache_summary": cache_summary(components),
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


