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

Implementation lives in the sibling ``verify_component_*.py`` modules (each
physical file stays under 400 lines); this entry keeps the CLI contract:
``python3 scripts/ci/verify-component.py [components] [--all] [--race] ...``.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Sequence

from verify_component_cache import format_cache_summary
from verify_component_core import (
    DEFAULT_REGISTRY,
    DEFAULT_REPORT,
    EXIT_CONFIG_ERROR,
    EXIT_TIMEOUT,
    RegistryError,
)
from verify_component_registry import load_registry, resolve_components
from verify_component_runner import _git_sha, _now_utc, _run_subprocess, run_components, write_report


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
    cache = report.get("cache_summary")
    if cache:
        print(format_cache_summary(cache))
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
