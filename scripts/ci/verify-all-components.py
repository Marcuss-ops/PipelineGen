#!/usr/bin/env python3
"""Run every component through the canonical component runner.

This is a deliberately small compatibility entry point.  Component execution,
dependency resolution, timeout handling, and report generation remain owned by
``verify-component.py``.
"""

from __future__ import annotations

import argparse
import importlib.util
import sys
from pathlib import Path
from typing import Sequence


RUNNER = Path(__file__).with_name("verify-component.py")


def _load_runner():
    spec = importlib.util.spec_from_file_location("verify_component_all", RUNNER)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load component runner: {RUNNER}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("fast", "race"), default="fast")
    parser.add_argument("--race", action="store_true", help="shortcut for --mode race")
    parser.add_argument("--registry", type=Path, default=Path("config/verify-components.json"))
    parser.add_argument("--report", type=Path, default=Path("artifacts/verify/latest.json"))
    parser.add_argument("--repo-root", type=Path, default=None)
    parser.add_argument("--include-live", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    runner = _load_runner()
    mode = "race" if args.race else args.mode
    root = (args.repo_root or Path.cwd()).resolve()
    registry_path = args.registry if args.registry.is_absolute() else root / args.registry
    report_path = args.report if args.report.is_absolute() else root / args.report
    try:
        registry = runner.load_registry(registry_path)
        report, code = runner.run_components(
            registry,
            list(registry),
            mode=mode,
            repo_root=root,
            report_path=report_path,
            include_live=args.include_live,
            dry_run=args.dry_run,
        )
    except runner.RegistryError as exc:
        print(f"VERIFY_ALL_COMPONENTS_CONFIG_ERROR {exc}", file=sys.stderr)
        return runner.EXIT_CONFIG_ERROR

    print(
        f"verify-all-components mode={report['mode']} "
        f"components={len(report['resolved_components'])} "
        f"final={report['final']} duration_ms={report['duration_ms']} "
        f"report={report_path}"
    )
    return code


if __name__ == "__main__":
    raise SystemExit(main())
