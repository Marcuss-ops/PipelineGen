#!/usr/bin/env python3
"""Fail-closed coverage check for the verification component registry.

The changed-file resolver is intentionally conservative: an unmapped file
causes the impacted set to be untrusted.  This command checks the stronger,
repository-wide invariant so that a new production or verification file
cannot silently bypass the registry.
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Iterable, Mapping


EXIT_CONFIG_ERROR = 2
EXIT_FAILURE = 1

# These are coverage domains, not executable registry components.  They keep
# support code auditable without introducing umbrella components that would
# make verify-main run unrelated tests.
COVERAGE_FALLBACKS: tuple[tuple[str, str], ...] = (
    ("Makefile", "verification"),
    ("make/", "verification"),
    ("scripts/ci/", "verification"),
    ("scripts/hooks/", "verification"),
    ("config/", "verification"),
    ("internal/", "core-internal"),
    ("cmd/", "commands"),
    ("pkg/", "packages"),
    ("tests/", "integration-tests"),
    ("migrations/", "database"),
)

# Shared application adapters are intentionally owned by more than one
# component.  Every other overlap is a registry design error.
ALLOWED_OVERLAP_PREFIXES: tuple[tuple[str, frozenset[str]], ...] = (
    ("internal/application/scripts/", frozenset({"script", "research", "translation"})),
    ("internal/application/assets/providers/artlist/", frozenset({"stock", "artlist"})),
    ("internal/infrastructure/artlist/", frozenset({"stock", "artlist"})),
    ("internal/api/assets/artlist/", frozenset({"stock", "artlist"})),
    ("internal/platform/drive/", frozenset({"drive", "storage"})),
    ("internal/api/assets/storage/", frozenset({"timeline", "storage"})),
    ("internal/api/assets/clips/indexing/", frozenset({"clips", "indexing"})),
    ("internal/platform/sqlite/jobs/", frozenset({"database", "jobs"})),
    ("internal/platform/sqlite/scripts/", frozenset({"database", "research"})),
)


def normalize(path: str) -> str:
    value = path.replace("\\", "/").strip()
    while value.startswith("./"):
        value = value[2:]
    return value.strip("/")


def matches(path: str, registered: str) -> bool:
    path = normalize(path)
    registered = normalize(registered)
    if not path or not registered:
        return False
    if registered.endswith("/"):
        return path.startswith(registered)
    return path == registered or path.startswith(registered + "/")


def owners(path: str, registry: Mapping[str, Mapping[str, Any]]) -> list[str]:
    return sorted(
        name
        for name, definition in registry.items()
        if any(matches(path, registered) for registered in definition.get("paths", []))
    )


def fallback_owner(path: str) -> tuple[str, str] | None:
    normalized = normalize(path)
    for prefix, domain in COVERAGE_FALLBACKS:
        if normalized == prefix.rstrip("/") or normalized.startswith(prefix):
            return domain, f"coverage fallback domain {domain} ({prefix})"
    return None


def overlap_is_allowed(path: str, path_owners: list[str]) -> bool:
    owner_set = frozenset(path_owners)
    for prefix, allowed in ALLOWED_OVERLAP_PREFIXES:
        if path.startswith(prefix) and owner_set <= allowed:
            return True
    return False


def tracked_files(root: Path) -> list[str]:
    completed = subprocess.run(
        ["git", "ls-files", "-co", "--exclude-standard", "-z"],
        cwd=root,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", errors="replace").strip()
        raise RuntimeError(f"git file discovery failed: {detail}")
    return sorted(
        normalize(item)
        for item in completed.stdout.decode("utf-8", errors="surrogateescape").split("\0")
        if normalize(item)
    )


def in_scope(path: str) -> bool:
    """Return whether *path* must have a registry owner.

    Reports, caches, generated artifacts, documentation, and examples are
    intentionally outside this gate.  Production Go, test sources, Make
    recipes, migrations, configuration, and verification scripts are
    machine-executed surfaces.
    """

    path = normalize(path)
    if path.startswith((".git/", "artifacts/", "tmp/", "data/", "vendor/", "node_modules/")):
        return False
    if path == "Makefile" or path.startswith("make/"):
        return path == "Makefile" or path.endswith(".mk")
    if path.startswith(("internal/", "cmd/", "pkg/")):
        return path.endswith(".go")
    if path.startswith("tests/"):
        return not any(
            marker in path
            for marker in ("/reports/", "/raw/", "/incomplete/")
        )
    if path.startswith(("scripts/ci/", "scripts/hooks/")):
        return path.endswith((".py", ".sh"))
    if path.startswith(("config/", "migrations/")):
        return True
    return False


def command_entries(registry: Mapping[str, Mapping[str, Any]]) -> Iterable[tuple[str, str, Any]]:
    for component, definition in registry.items():
        for field in ("node_tests", "python_tests", "live_tests"):
            for entry in definition.get(field, []):
                yield component, field, entry


def validate_commands(root: Path, registry: Mapping[str, Mapping[str, Any]]) -> list[str]:
    """Validate command syntax and referenced files without executing tests."""
    errors: list[str] = []
    for component, field, entry in command_entries(registry):
        try:
            argv = shlex.split(entry) if isinstance(entry, str) else list(entry)
        except (TypeError, ValueError) as exc:
            errors.append(f"component={component}: {field}: invalid command: {exc}")
            continue
        if not argv or any(not isinstance(arg, str) or not arg for arg in argv):
            errors.append(f"component={component}: {field}: empty command argument")
            continue
        command_text = " ".join(argv).lower()
        if any(marker in command_text for marker in ("todo", "not implemented", "placeholder")):
            errors.append(f"component={component}: {field}: placeholder command")
        if argv[0] in {"python", "python3"} and len(argv) > 1:
            script_args = [arg for arg in argv[1:] if arg.endswith((".py", ".sh"))]
            for script in script_args:
                if not (root / script).is_file():
                    errors.append(f"component={component}: {field}: missing file {script}")
        if argv[0] in {"node", "npm", "npx"}:
            if "--prefix" in argv:
                index = argv.index("--prefix")
                if index + 1 >= len(argv) or not (root / argv[index + 1]).is_dir():
                    errors.append(f"component={component}: {field}: missing Node prefix")
            for arg in argv[1:]:
                if arg.endswith((".js", ".mjs", ".cjs")) and not (root / arg).is_file():
                    errors.append(f"component={component}: {field}: missing file {arg}")
    return errors


def validate_registry(root: Path, registry: Mapping[str, Mapping[str, Any]]) -> list[str]:
    errors: list[str] = []
    if not registry:
        return ["registry is empty"]
    for name, definition in registry.items():
        if not isinstance(definition, dict):
            errors.append(f"component={name}: definition is not an object")
            continue
        paths = definition.get("paths", [])
        if not isinstance(paths, list) or not paths:
            errors.append(f"component={name}: paths must be a non-empty array")
            continue
        for registered in paths:
            if not isinstance(registered, str) or not registered.strip():
                errors.append(f"component={name}: path must be a non-empty string")
                continue
            if not (root / registered.rstrip("/")).exists():
                errors.append(f"component={name}: missing registry path {registered}")
        if not definition.get("utility", False) and not definition.get("go_packages"):
            errors.append(f"component={name}: no Go verification package")
        for package in definition.get("go_packages", []):
            if not isinstance(package, str) or not package.startswith("./"):
                errors.append(f"component={name}: invalid Go package {package!r}")
                continue
            package_path = package[2:].removesuffix("/...")
            if package_path and not (root / package_path).exists():
                errors.append(f"component={name}: missing Go package path {package}")
    return errors


def build_report(root: Path, registry_path: Path) -> dict[str, Any]:
    try:
        registry = json.loads(registry_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"cannot load registry {registry_path}: {exc}") from exc
    if not isinstance(registry, dict):
        raise RuntimeError("registry must be a JSON object")

    errors = validate_registry(root, registry)
    command_errors = validate_commands(root, registry)
    unmapped: list[str] = []
    mapping: dict[str, dict[str, Any]] = {}
    unexpected_overlaps: list[dict[str, Any]] = []
    for path in tracked_files(root):
        if not in_scope(path):
            continue
        path_owners = owners(path, registry)
        reason = "registry paths"
        effective_owners = path_owners
        if not effective_owners:
            fallback = fallback_owner(path)
            if fallback:
                effective_owners = [fallback[0]]
                reason = fallback[1]
        mapping[path] = {"owners": effective_owners, "reason": reason}
        if not effective_owners:
            unmapped.append(path)
        elif len(path_owners) > 1 and not overlap_is_allowed(path, path_owners):
            unexpected_overlaps.append({"file": path, "owners": path_owners})

    stale_registry_paths = sorted(
        f"{component}: {registered}"
        for component, definition in registry.items()
        for registered in definition.get("paths", [])
        if not (root / registered.rstrip("/")).exists()
    )
    checked = len(mapping)
    mapped = checked - len(unmapped)
    all_errors = errors + command_errors

    return {
        "schema_version": 1,
        "registry": str(registry_path.relative_to(root)),
        "tracked_files_checked": checked,
        "scanned_files": checked,
        "mapped_files": mapped,
        "coverage_percent": round((mapped / checked) * 100, 2) if checked else 100,
        "unmapped_files": unmapped,
        "stale_registry_paths": stale_registry_paths,
        "unexpected_overlaps": unexpected_overlaps,
        "mapping": mapping,
        "registry_errors": errors,
        "command_errors": command_errors,
        "coverage_domains": sorted({domain for _, domain in COVERAGE_FALLBACKS}),
        "final": "PASS" if not all_errors and not unmapped and not stale_registry_paths and not unexpected_overlaps else "FAIL",
    }


def write_report(path: Path, report: Mapping[str, Any]) -> None:
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


def parse_args(argv: Iterable[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--registry", type=Path, default=Path("config/verify-components.json"))
    parser.add_argument("--repo-root", type=Path, default=Path.cwd())
    parser.add_argument("--report", type=Path, default=None)
    return parser.parse_args(list(argv) if argv is not None else None)


def main(argv: Iterable[str] | None = None) -> int:
    args = parse_args(argv)
    root = args.repo_root.resolve()
    registry = args.registry if args.registry.is_absolute() else root / args.registry
    try:
        report = build_report(root, registry)
    except (OSError, RuntimeError) as exc:
        print(f"VERIFY_COMPONENT_COVERAGE_CONFIG_ERROR {exc}", file=sys.stderr)
        return EXIT_CONFIG_ERROR

    if args.report:
        report_path = args.report if args.report.is_absolute() else root / args.report
        report_path.parent.mkdir(parents=True, exist_ok=True)
        write_report(report_path, report)
    print(
        f"verify-component-coverage scanned={report['scanned_files']} "
        f"unmapped={len(report['unmapped_files'])} "
        f"registry_errors={len(report['registry_errors']) + len(report['command_errors'])} "
        f"final={report['final']}"
    )
    if report["unmapped_files"]:
        print("unmapped_files=" + ",".join(report["unmapped_files"]), file=sys.stderr)
    for error in report["registry_errors"]:
        print(error, file=sys.stderr)
    for error in report["command_errors"]:
        print(error, file=sys.stderr)
    return 0 if report["final"] == "PASS" else EXIT_FAILURE


if __name__ == "__main__":
    raise SystemExit(main())
