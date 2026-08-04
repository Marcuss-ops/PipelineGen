#!/usr/bin/env python3
"""Verify only components impacted by the current Git changes.

The component registry remains the single source of truth for path ownership,
commands, dependencies, and timeouts.  This script only discovers changed
paths and delegates execution to ``verify-component.py``.

Examples:
    python3 scripts/ci/verify-changed-components.py --dry-run
    python3 scripts/ci/verify-changed-components.py --base HEAD~1
    python3 scripts/ci/verify-changed-components.py --race --report /tmp/changed.json
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, Iterable, Mapping, Sequence


DEFAULT_REGISTRY = Path("config/verify-components.json")
DEFAULT_REPORT = Path("artifacts/verify/changed-components.json")
DEFAULT_LATEST_REPORT = Path("artifacts/verify/latest.json")
DEFAULT_BASE_CANDIDATES = ("origin/main", "main", "HEAD~1")
EXIT_CONFIG_ERROR = 2
EXIT_FAILURE = 1
EXIT_TIMEOUT = 124

# Changes to the verification machinery or global build inputs can invalidate
# every component, even when the changed file is outside a component path.
ALL_COMPONENT_EXACT_FILES = frozenset(
    {
        "Makefile",
        "go.mod",
        "go.sum",
        "config.example.yaml",
        "config.production.example.yaml",
        "config/multilingual.yaml",
        "config/verify-components.json",
        "config/verify-pipelines.json",
        "scripts/ci/verify-component.py",
        "scripts/ci/verify-all-components.py",
        "scripts/ci/verify-pipeline.py",
        "scripts/ci/verify-changed-components.py",
    }
)
ALL_COMPONENT_PREFIXES = (
    "make/",
    "scripts/hooks/",
    "scripts/ci/",
    "internal/",
    "cmd/",
    "pkg/",
    "tests/",        "migrations/",
        "architecture/",
)
IGNORED_CHANGED_PREFIXES = ("artifacts/", "tmp/", "data/")


class ChangedComponentsError(RuntimeError):
    """The changed-file set or component verification cannot be trusted."""


@dataclass(frozen=True)
class GitChanges:
    files: tuple[str, ...]
    base_ref: str | None
    base_available: bool
    base_fallback: bool


@dataclass(frozen=True)
class ComponentRunner:
    module: Any
    path: Path


@dataclass(frozen=True)
class Execution:
    report: dict[str, Any]
    exit_code: int


def _now_utc() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _git_sha(repo_root: Path) -> str | None:
    import subprocess

    completed = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=str(repo_root),
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        check=False,
    )
    value = completed.stdout.strip()
    return value if completed.returncode == 0 and value else None


def _load_component_runner(path: Path) -> ComponentRunner:
    """Load the existing runner without duplicating its registry logic."""
    if not path.is_file():
        raise ChangedComponentsError(f"component runner does not exist: {path}")
    spec = importlib.util.spec_from_file_location("verify_component_shared", path)
    if spec is None or spec.loader is None:
        raise ChangedComponentsError(f"cannot load component runner: {path}")
    module = importlib.util.module_from_spec(spec)
    # dataclasses and other decorators may inspect the module during import;
    # register it before execution just like normal import machinery does.
    sys.modules[spec.name] = module
    try:
        spec.loader.exec_module(module)
    except Exception as exc:  # pragma: no cover - exercised through CLI errors
        raise ChangedComponentsError(f"cannot load component runner {path}: {exc}") from exc
    return ComponentRunner(module=module, path=path)


def _git_command(repo_root: Path, args: Sequence[str]) -> list[str]:
    """Run a Git name-list command and return NUL-separated paths."""
    import subprocess

    completed = subprocess.run(
        ["git", *args],
        cwd=str(repo_root),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if completed.returncode != 0:
        stderr = completed.stderr.decode("utf-8", errors="replace").strip()
        raise ChangedComponentsError(
            f"git {' '.join(args)} failed with exit={completed.returncode}"
            + (f": {stderr}" if stderr else "")
        )
    output = completed.stdout.decode("utf-8", errors="surrogateescape")
    return [item for item in output.split("\0") if item]


def _git_ref_exists(repo_root: Path, ref: str) -> bool:
    import subprocess

    completed = subprocess.run(
        ["git", "rev-parse", "--verify", f"{ref}^{{commit}}"],
        cwd=str(repo_root),
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    return completed.returncode == 0


def _changed_against_base(repo_root: Path, base_ref: str) -> list[str]:
    """Return committed changes, tolerating a shallow/unrelated base shape."""
    import subprocess

    for revision in (f"{base_ref}...HEAD", base_ref):
        completed = subprocess.run(
            ["git", "diff", "--name-only", "-z", revision],
            cwd=str(repo_root),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
        if completed.returncode == 0:
            output = completed.stdout.decode("utf-8", errors="surrogateescape")
            return [item for item in output.split("\0") if item]
    stderr = completed.stderr.decode("utf-8", errors="replace").strip()
    raise ChangedComponentsError(
        f"cannot compare HEAD with base={base_ref}"
        + (f": {stderr}" if stderr else "")
    )


def select_base_ref(
    repo_root: Path,
    requested: str | None,
    candidates: Sequence[str] = DEFAULT_BASE_CANDIDATES,
) -> tuple[str | None, bool]:
    """Choose an explicit base or a safe default; return (ref, fallback_used)."""
    if requested:
        if not _git_ref_exists(repo_root, requested):
            raise ChangedComponentsError(f"requested base ref does not exist: {requested}")
        return requested, False
    for candidate in candidates:
        if _git_ref_exists(repo_root, candidate):
            return candidate, candidate != candidates[0]
    return None, True


def collect_changed_files(repo_root: Path, base_ref: str | None) -> GitChanges:
    """Collect committed, staged, unstaged, and untracked non-ignored paths."""
    paths: set[str] = set()
    if base_ref is not None:
        paths.update(_changed_against_base(repo_root, base_ref))
    paths.update(_git_command(repo_root, ["diff", "--name-only", "-z"]))
    paths.update(_git_command(repo_root, ["diff", "--cached", "--name-only", "-z"]))
    paths.update(_git_command(repo_root, ["ls-files", "--others", "--exclude-standard", "-z"]))
    normalized = tuple(
        sorted(
            path
            for path in (normalize_path(item) for item in paths)
            if path and not path.startswith(IGNORED_CHANGED_PREFIXES)
        )
    )
    return GitChanges(
        files=normalized,
        base_ref=base_ref,
        base_available=base_ref is not None,
        base_fallback=base_ref is None,
    )


def normalize_path(path: str) -> str:
    """Normalize Git's repository-relative POSIX paths for prefix matching."""
    value = path.replace("\\", "/").strip()
    while value.startswith("./"):
        value = value[2:]
    return value.strip("/")


def _path_matches(path: str, registered_path: str) -> bool:
    path = normalize_path(path)
    registered = normalize_path(registered_path)
    if not path or not registered:
        return False
    if registered.endswith("/"):
        return path.startswith(registered)
    return path == registered or path.startswith(registered + "/")


def _requires_all_components(path: str) -> bool:
    normalized = normalize_path(path)
    return normalized in ALL_COMPONENT_EXACT_FILES or any(
        normalized.startswith(prefix) for prefix in ALL_COMPONENT_PREFIXES
    )


def map_changed_files(
    changed_files: Iterable[str],
    registry: Mapping[str, Mapping[str, Any]],
    *,
    run_all_when_unmapped: bool = False,
) -> tuple[dict[str, list[str]], list[str], list[str]]:
    """Map files to registered components, preserving deterministic order.

    Returns ``(file_to_components, impacted_components, unmapped_files)``.
    Dependencies are intentionally not resolved here; the shared component
    runner resolves them exactly once when it executes the impacted set.
    """
    mapping: dict[str, list[str]] = {}
    impacted: list[str] = []
    unmapped: list[str] = []
    component_items = list(registry.items())

    for raw_path in sorted({normalize_path(item) for item in changed_files if normalize_path(item)}):
        matches: list[str] = []
        for name, definition in component_items:
            paths = definition.get("paths", [])
            if any(_path_matches(raw_path, registered) for registered in paths):
                matches.append(name)
        # Global inputs and known repository source domains fall back to a
        # complete run only when no narrower registry owner exists. A truly
        # unknown path remains unmapped and fails closed.
        if not matches and _requires_all_components(raw_path):
            matches = [name for name, _ in component_items]
        if not matches:
            unmapped.append(raw_path)
            if run_all_when_unmapped:
                matches = [name for name, _ in component_items]
        mapping[raw_path] = matches
        for name in matches:
            if name not in impacted:
                impacted.append(name)
    return mapping, impacted, unmapped


def _write_report(path: Path, report: Mapping[str, Any]) -> None:
    import os
    import tempfile

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


def run_changed_components(
    changes: GitChanges,
    registry: Mapping[str, Mapping[str, Any]],
    *,
    runner_module: Any,
    repo_root: Path,
    mode: str = "fast",
    dry_run: bool = False,
    report_path: Path | None = None,
    run_all_when_unmapped: bool = False,
    command_runner: Callable[..., Any] | None = None,
) -> Execution:
    """Map and execute impacted components once through the shared runner."""
    if mode not in {"fast", "race"}:
        raise ChangedComponentsError(f"unsupported mode={mode}")
    mapping, impacted, unmapped = map_changed_files(
        changes.files,
        registry,
        run_all_when_unmapped=run_all_when_unmapped,
    )
    all_components = list(registry)
    started = _now_utc()
    git_sha = _git_sha(repo_root)
    if not changes.files:
        report: dict[str, Any] = {
            "schema_version": 1,
            "mode": mode,
            "base_ref": changes.base_ref,
            "base_available": changes.base_available,
            "base_fallback": changes.base_fallback,
            "changed_files": [],
            "file_components": {},
            "impacted_components": [],
            "resolved_components": [],
            "unmapped_files": [],
            "skipped": all_components,
            "components": {},
            "started_at": started,
            "finished_at": _now_utc(),
            "git_sha": git_sha,
            "duration_ms": 0,
            "dry_run": dry_run,
            "final": "PASS",
            "reason": "no_changes",
        }
        if report_path is not None:
            _write_report(report_path, report)
        return Execution(report, 0)

    if not changes.base_available:
        # Without a usable baseline, changed files cannot be safely attributed.
        # Fail closed by verifying every registered component, regardless of
        # whether a path happens to match the current registry.
        impacted = all_components
        for path in changes.files:
            mapping[path] = list(all_components)
        unmapped = []

    if unmapped and run_all_when_unmapped:
        # The caller explicitly chose the conservative fallback: every
        # component is being verified, so these paths are not skipped or
        # unverified. Keep the report fail-closed for the non-opt-in path,
        # while making the opt-in result accurately report PASS/FAIL from the
        # full component run itself.
        impacted = all_components
        for path in unmapped:
            mapping[path] = list(all_components)
        unmapped = []

    if unmapped and not run_all_when_unmapped:
        # A changed path with no registry owner is unsafe to silently skip,
        # even when another changed path mapped successfully. Operators can
        # opt into a conservative full run explicitly with
        # --run-all-when-unmapped.
        report = {
            "schema_version": 1,
            "mode": mode,
            "base_ref": changes.base_ref,
            "base_available": changes.base_available,
            "base_fallback": changes.base_fallback,
            "changed_files": list(changes.files),
            "file_components": mapping,
            "impacted_components": impacted,
            "resolved_components": [],
            "unmapped_files": unmapped,
            "skipped": all_components,
            "components": {},
            "started_at": started,
            "finished_at": _now_utc(),
            "git_sha": git_sha,
            "duration_ms": 0,
            "dry_run": dry_run,
            "final": "FAIL",
            "reason": "unmapped_files",
        }
        if report_path is not None:
            _write_report(report_path, report)
        return Execution(report, EXIT_FAILURE)

    if not impacted:
        report = {
            "schema_version": 1,
            "mode": mode,
            "base_ref": changes.base_ref,
            "base_available": changes.base_available,
            "base_fallback": changes.base_fallback,
            "changed_files": list(changes.files),
            "file_components": mapping,
            "impacted_components": [],
            "resolved_components": [],
            "unmapped_files": unmapped,
            "skipped": all_components,
            "components": {},
            "started_at": started,
            "finished_at": _now_utc(),
            "git_sha": git_sha,
            "duration_ms": 0,
            "dry_run": dry_run,
            "final": "FAIL",
            "reason": "unmapped_files",
        }
        if report_path is not None:
            _write_report(report_path, report)
        return Execution(report, EXIT_FAILURE)

    execution_runner = command_runner or runner_module._run_subprocess
    component_report, code = runner_module.run_components(
        registry,
        impacted,
        mode=mode,
        repo_root=repo_root,
        report_path=None,
        include_live=False,
        dry_run=dry_run,
        runner=execution_runner,
    )
    resolved = component_report.get("resolved_components", [])
    component_statuses = component_report.get("components", {})
    expected_resolved = runner_module.resolve_components(registry, impacted)
    report_valid = (
        component_report.get("final") == "PASS"
        and isinstance(resolved, list)
        and resolved == expected_resolved
        and isinstance(component_statuses, dict)
        and all(
            isinstance(component_statuses.get(name), dict)
            and component_statuses[name].get("status") == "PASS"
            for name in resolved
        )
    )
    if code == 0 and not report_valid:
        code = EXIT_FAILURE
    report = {
        "schema_version": 1,
        "mode": mode,
        "base_ref": changes.base_ref,
        "base_available": changes.base_available,
        "base_fallback": changes.base_fallback,
        "changed_files": list(changes.files),
        "file_components": mapping,
        "impacted_components": impacted,
        "resolved_components": resolved,
        "unmapped_files": unmapped,
        "skipped": [name for name in all_components if name not in resolved],
        "components": component_report.get("components", {}),
        "component_report": component_report,
        "started_at": started,
        "finished_at": _now_utc(),
        "git_sha": git_sha,
        "duration_ms": component_report.get("duration_ms", 0),
        "dry_run": dry_run,
        "final": "PASS" if code == 0 else "FAIL",
    }
    if report_path is not None:
        _write_report(report_path, report)
    return Execution(report, code)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default=None, help="explicit Git base ref; default tries origin/main, main, HEAD~1")
    parser.add_argument("--registry", type=Path, default=DEFAULT_REGISTRY)
    parser.add_argument("--component-runner", type=Path, default=Path("scripts/ci/verify-component.py"))
    parser.add_argument("--repo-root", type=Path, default=None)
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT)
    parser.add_argument("--mode", choices=("fast", "race"), default="fast")
    parser.add_argument("--race", action="store_true", help="shortcut for --mode race")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument(
        "--run-all-when-unmapped",
        action="store_true",
        help="verify every component when a changed path has no registry owner",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    root = (args.repo_root or Path.cwd()).resolve()
    registry_path = args.registry if args.registry.is_absolute() else root / args.registry
    runner_path = args.component_runner if args.component_runner.is_absolute() else root / args.component_runner
    report_path = args.report if args.report.is_absolute() else root / args.report
    mode = "race" if args.race else args.mode
    try:
        runner = _load_component_runner(runner_path)
        registry = runner.module.load_registry(registry_path)
        base_ref, fallback = select_base_ref(root, args.base)
        changes = collect_changed_files(root, base_ref)
        changes = GitChanges(
            files=changes.files,
            base_ref=changes.base_ref,
            base_available=changes.base_available,
            base_fallback=fallback,
        )
        execution = run_changed_components(
            changes,
            registry,
            runner_module=runner.module,
            repo_root=root,
            mode=mode,
            dry_run=args.dry_run,
            report_path=report_path,
            run_all_when_unmapped=args.run_all_when_unmapped,
        )
    except ChangedComponentsError as exc:
        print(f"VERIFY_CHANGED_COMPONENTS_CONFIG_ERROR {exc}", file=sys.stderr)
        return EXIT_CONFIG_ERROR
    except ValueError as exc:
        print(f"VERIFY_CHANGED_COMPONENTS_CONFIG_ERROR {exc}", file=sys.stderr)
        return EXIT_CONFIG_ERROR
    except OSError as exc:
        print(f"VERIFY_CHANGED_COMPONENTS_REPORT_ERROR {exc}", file=sys.stderr)
        return EXIT_FAILURE

    report = execution.report
    # Keep the standard global report path useful for the pre-push gate while
    # retaining the richer changed-components report as a separate artifact.
    _write_report(root / DEFAULT_LATEST_REPORT, report)
    print(
        f"verify-changed-components mode={report['mode']} "
        f"changed_files={len(report['changed_files'])} "
        f"components={','.join(report['resolved_components']) or '-'} "
        f"final={report['final']} report={report_path}"
    )
    if report.get("unmapped_files"):
        print(
            "unmapped_files=" + ",".join(report["unmapped_files"]),
            file=sys.stderr,
        )
    return execution.exit_code


if __name__ == "__main__":
    raise SystemExit(main())
