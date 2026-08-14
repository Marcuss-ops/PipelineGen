#!/usr/bin/env python3
"""Certify the content-addressed verification cache end-to-end.

This is the VERIFY CACHE LIVE CERTIFICATION harness.  It exercises the exact
runner invoked by ``make verify-main`` (``scripts/ci/verify-component.py``)
through a hermetic, self-contained sandbox: a synthetic registry plus fixture
files in a temporary directory, executed with real subprocesses.

Nothing here reimplements registry, fingerprint, cache, or command logic — the
existing component runner owns all of it.  This script only drives the
certification steps and prints the final report, so a green run means the
cache behaves exactly as ``make verify-main`` relies on.

The sandbox keeps every mutation (source/test/untracked/staged files, go.mod,
corrupted cache records) away from the repository's tracked files and its
``.cache/pipelinegen/verify`` directory.

Exit codes:
    0  CERTIFIED (every check passed)
    1  certification failed (a check failed or failed closed)
    2  configuration error (runner/registry unusable)
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Callable

REPO_ROOT = Path(__file__).resolve().parents[2]
RUNNER_PATH = REPO_ROOT / "scripts/ci/verify-component.py"

# Fixed toolchain fixtures make the toolchain-change step deterministic
# regardless of which Go/Node versions happen to be installed on the host.
TOOLCHAIN_A = {"go": "go version go1.21.0 linux/amd64", "node": "v20.0.0", "python": "3.11.0"}
TOOLCHAIN_B = {"go": "go version go1.22.0 linux/amd64", "node": "v20.0.0", "python": "3.11.0"}


class CertificationError(RuntimeError):
    """The certification harness itself cannot proceed safely."""


def _load_runner(path: Path = RUNNER_PATH) -> Any:
    """Load the existing component runner without duplicating its logic."""
    runner_path = Path(path)
    if not runner_path.is_file():
        raise CertificationError(f"component runner does not exist: {runner_path}")
    spec = importlib.util.spec_from_file_location("verify_component_certification_runner", runner_path)
    if spec is None or spec.loader is None:
        raise CertificationError(f"cannot load component runner: {runner_path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    try:
        spec.loader.exec_module(module)
    except Exception as exc:  # noqa: BLE001 - report as config error
        raise CertificationError(f"cannot load component runner {runner_path}: {exc}") from exc
    return module


def _probe(log: Path) -> list[str]:
    """A PASS command that appends one marker per execution to ``log``."""
    return [
        sys.executable,
        "-c",
        "import pathlib,sys\n"
        "p=pathlib.Path(sys.argv[1])\n"
        "p.parent.mkdir(parents=True,exist_ok=True)\n"
        "f=open(str(p),'a');f.write('x');f.close()",
        str(log),
    ]


def _fail() -> list[str]:
    """A command that always fails, to exercise the never-cache rule."""
    return [sys.executable, "-c", "import sys; sys.exit(3)"]


def _source_gate(source: Path, log: Path) -> list[str]:
    """A command whose outcome depends on a tracked source file's content.

    Exits non-zero when the source contains the literal ``FAIL`` marker, and
    otherwise records one execution marker.  This couples the gate's outcome
    to the fingerprint input so the never-cache and fix-then-run rules are
    exercised end-to-end.
    """
    return [
        sys.executable,
        "-c",
        "import pathlib,sys\n"
        "src=pathlib.Path(sys.argv[1])\n"
        "if 'FAIL' in src.read_text(): sys.exit(3)\n"
        "log=pathlib.Path(sys.argv[2])\n"
        "log.parent.mkdir(parents=True,exist_ok=True)\n"
        "f=open(str(log),'a');f.write('x');f.close()",
        str(source),
        str(log),
    ]


class Certification:
    """Self-contained sandbox that drives the certification steps."""

    def __init__(self, runner: Any, keep_sandbox: bool = False) -> None:
        self.runner = runner
        self.keep_sandbox = keep_sandbox
        self.root = Path(tempfile.mkdtemp(prefix="verify-cache-cert-"))
        self.registry_path = self.root / "registry.json"
        self.logs: dict[str, Path] = {}
        self.registry: dict[str, dict[str, Any]] = {}
        self.git_available = False
        self.results: list[tuple[str, bool, str]] = []
        self.blocks: list[list[str]] = []

    def __enter__(self) -> "Certification":
        self._setup()
        return self

    def __exit__(self, *exc: object) -> None:
        if self.keep_sandbox:
            print(f"VERIFY_CACHE_CERTIFICATION_SANDBOX {self.root}", file=sys.stderr)
            return
        shutil.rmtree(self.root, ignore_errors=True)

    # ------------------------------------------------------------------ setup

    def _def(
        self,
        paths: list[str],
        *,
        deps: tuple[str, ...] = (),
        python_tests: tuple[list[str], ...] = (),
        live_tests: tuple[list[str], ...] = (),
        go_packages: tuple[str, ...] = (),
        cacheable: bool = True,
        race_enabled: bool = False,
    ) -> dict[str, Any]:
        return {
            "paths": list(paths),
            "go_packages": list(go_packages),
            "node_tests": [],
            "python_tests": [list(command) for command in python_tests],
            "live_tests": [list(command) for command in live_tests],
            "dependencies": list(deps),
            "timeout_seconds": 120,
            "race_timeout_seconds": 120,
            "race_enabled": race_enabled,
            "utility": False,
            "cacheable": cacheable,
            "cache_scope": "content" if cacheable else "live",
            "required_artifacts": [],
        }

    def _write_fixture(self, rel: str, content: str) -> Path:
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    def _log(self, name: str) -> Path:
        log = self.root / "logs" / f"{name}.log"
        self.logs[name] = log
        return log

    def _write_main_registry(self) -> None:
        self.registry_path.write_text(json.dumps(self.registry, indent=2), encoding="utf-8")

    def _write_registry_file(self, path: Path, definitions: dict[str, dict[str, Any]]) -> None:
        path.write_text(json.dumps(definitions, indent=2), encoding="utf-8")

    def _setup(self) -> None:
        for name in ("kernel", "audio", "ollama", "stock", "toolchain_probe", "cmd_probe", "gomod_probe"):
            self._write_fixture(f"src/{name}/{name}.go", f"package {name}\n\nvar Probe = 1\n")
        self._write_fixture("src/audio/audio_test.go", "package audio\n\nimport \"testing\"\n\nfunc TestProbe(t *testing.T) {}\n")

        self.registry = {
            "kernel": self._def(
                ["src/kernel/"], python_tests=(_probe(self._log("kernel")),), race_enabled=True
            ),
            "audio": self._def(
                ["src/audio/"],
                deps=("kernel",),
                python_tests=(_source_gate(self.root / "src/audio/audio.go", self._log("audio")),),
            ),
            "ollama": self._def(
                ["src/ollama/"], python_tests=(_probe(self._log("ollama")),)
            ),
            "stock": self._def(
                ["src/stock/"],
                live_tests=(_probe(self._log("stock")),),
                cacheable=False,
            ),
        }
        self._write_main_registry()
        # Validate the registry immediately so a broken sandbox fails fast.
        self.runner.load_registry(self.registry_path)

        try:
            subprocess.run(["git", "init", "-q"], cwd=self.root, check=True, capture_output=True)
            self.git_available = True
        except (OSError, subprocess.CalledProcessError):
            self.git_available = False

    # ----------------------------------------------------------------- helpers

    def _clear_cache(self) -> None:
        shutil.rmtree(self.runner.cache_root(self.root), ignore_errors=True)

    def _reset_logs(self) -> None:
        for log in self.logs.values():
            log.parent.mkdir(parents=True, exist_ok=True)
            log.write_text("", encoding="utf-8")

    def _count(self, name: str) -> int:
        log = self.logs[name]
        try:
            return log.read_text(encoding="utf-8").count("x")
        except OSError:
            return 0

    def _run(
        self,
        components: list[str],
        *,
        mode: str = "fast",
        include_live: bool = False,
        toolchain: dict[str, str] | None = None,
        registry_path: Path | None = None,
    ) -> tuple[dict[str, Any], int]:
        registry = self.runner.load_registry(registry_path or self.registry_path)
        return self.runner.run_components(
            registry,
            components,
            mode=mode,
            repo_root=self.root,
            include_live=include_live,
            toolchain=toolchain,
        )

    def _record(self, name: str, fn: Callable[[], tuple[bool, str]]) -> None:
        try:
            passed, detail = fn()
        except Exception as exc:  # noqa: BLE001 - certification fails closed
            passed, detail = False, f"{type(exc).__name__}: {exc}"
        self.results.append((name, passed, detail))

    def _cache_dir(self, component: str) -> Path:
        return self.runner.cache_root(self.root) / component

    def _cache_entries(self, component: str) -> list[Path]:
        directory = self._cache_dir(component)
        if not directory.is_dir():
            return []
        return sorted(directory.glob("*.json"))

    @staticmethod
    def _fmt_ms(ms: int) -> str:
        return f"{ms / 1000.0:.2f}s"

    def _block(self, title: str, summary: dict[str, Any], *, saved: bool = False) -> None:
        lines = [
            f"{title}:",
            f"duration={self._fmt_ms(int(summary.get('duration_ms') or 0))}",
            f"executed={summary.get('executed', 0)}",
            f"hits={summary.get('hits', 0)}",
        ]
        if saved:
            lines.append(f"saved={self._fmt_ms(int(summary.get('saved_ms') or 0))}")
        self.blocks.append(lines)

    # ---------------------------------------------------------------- checks

    def _check_cold(self) -> tuple[bool, str]:
        self._clear_cache()
        self._reset_logs()
        report, code = self._run(["kernel", "audio", "ollama"])
        summary = report["cache_summary"]
        self._block("Cold", {"duration_ms": report["duration_ms"], **summary})
        executed = summary["executed"]
        ok = (
            code == 0
            and summary["hits"] == 0
            and executed == 3
            and all(self._count(name) == 1 for name in ("kernel", "audio", "ollama"))
        )
        return ok, f"code={code} hits={summary['hits']} executed={executed}"

    def _check_warm(self) -> tuple[bool, str]:
        self._reset_logs()
        report, code = self._run(["kernel", "audio", "ollama"])
        summary = report["cache_summary"]
        self._block("Warm unchanged", {"duration_ms": report["duration_ms"], **summary}, saved=True)
        ok = (
            code == 0
            and summary["hits"] == 3
            and summary["executed"] == 0
            and all(self._count(name) == 0 for name in ("kernel", "audio", "ollama"))
            and all(
                report["components"][name]["status"] == "CACHED_PASS"
                for name in ("kernel", "audio", "ollama")
            )
        )
        return ok, f"code={code} hits={summary['hits']} executed={summary['executed']}"

    def _check_working_tree(self) -> tuple[bool, str]:
        source = self.root / "src/audio/audio.go"
        original = source.read_text(encoding="utf-8")
        self._reset_logs()
        source.write_text(original + "// working tree change\n", encoding="utf-8")
        try:
            report, code = self._run(["kernel", "audio", "ollama"])
        finally:
            source.write_text(original, encoding="utf-8")
        summary = report["cache_summary"]
        self._block("Audio change", {"duration_ms": report["duration_ms"], **summary})
        audio = report["components"]["audio"]
        ok = (
            code == 0
            and audio["cache_hit"] is False
            and self._count("audio") == 1
            and report["components"]["kernel"]["cache_hit"] is True
            and report["components"]["ollama"]["cache_hit"] is True
            and self._count("kernel") == 0
            and self._count("ollama") == 0
        )
        return ok, f"code={code} hits={summary['hits']} executed={summary['executed']}"

    def _check_staged(self) -> tuple[bool, str]:
        if not self.git_available:
            return False, "git unavailable; cannot exercise a staged change"
        source = self.root / "src/audio/audio.go"
        original = source.read_text(encoding="utf-8")
        self._reset_logs()
        source.write_text(original + "// staged change\n", encoding="utf-8")
        try:
            subprocess.run(["git", "add", "src/audio/audio.go"], cwd=self.root, check=True)
            report, code = self._run(["audio"])
        finally:
            source.write_text(original, encoding="utf-8")
        audio = report["components"]["audio"]
        ok = code == 0 and audio["cache_hit"] is False and self._count("audio") == 1
        return ok, f"code={code} cache_hit={audio['cache_hit']}"

    def _check_untracked(self) -> tuple[bool, str]:
        untracked = self.root / "src/audio/untracked_test.go"
        self._reset_logs()
        untracked.write_text("package audio\n", encoding="utf-8")
        try:
            report, code = self._run(["audio"])
        finally:
            untracked.unlink(missing_ok=True)
        audio = report["components"]["audio"]
        ok = code == 0 and audio["cache_hit"] is False and self._count("audio") == 1
        return ok, f"code={code} cache_hit={audio['cache_hit']}"

    def _check_dependency(self) -> tuple[bool, str]:
        source = self.root / "src/kernel/kernel.go"
        original = source.read_text(encoding="utf-8")
        self._reset_logs()
        source.write_text(original + "// dependency change\n", encoding="utf-8")
        try:
            report, code = self._run(["kernel", "audio", "ollama"])
        finally:
            source.write_text(original, encoding="utf-8")
        kernel = report["components"]["kernel"]
        audio = report["components"]["audio"]
        ok = (
            code == 0
            and kernel["cache_hit"] is False
            and audio["cache_hit"] is False  # transitive invalidation
            and self._count("kernel") == 1
            and self._count("audio") == 1
            and report["components"]["ollama"]["cache_hit"] is True
        )
        return ok, f"code={code} kernel_hit={kernel['cache_hit']} audio_hit={audio['cache_hit']}"

    def _check_test(self) -> tuple[bool, str]:
        source = self.root / "src/audio/audio_test.go"
        original = source.read_text(encoding="utf-8")
        self._reset_logs()
        source.write_text(original + "\nfunc TestExtra(t *testing.T) {}\n", encoding="utf-8")
        try:
            report, code = self._run(["audio"])
        finally:
            source.write_text(original, encoding="utf-8")
        audio = report["components"]["audio"]
        ok = code == 0 and audio["cache_hit"] is False and self._count("audio") == 1
        return ok, f"code={code} cache_hit={audio['cache_hit']}"

    def _check_go_manifests(self) -> tuple[bool, str]:
        registry = {
            "gomod_probe": self._def(["src/gomod_probe/"], go_packages=("./src/gomod_probe/...",))
        }
        go_mod = self.root / "go.mod"
        go_sum = self.root / "go.sum"
        go_mod.write_text("module example.com/gomodprobe\n\ngo 1.21\n", encoding="utf-8")
        go_sum.write_text("example.com/foo v1.0.0 h1:AAAA\n", encoding="utf-8")

        def fingerprint() -> str:
            return self.runner.component_fingerprint(
                registry, "gomod_probe", "fast", False, self.root, toolchain=TOOLCHAIN_A
            )

        try:
            base = fingerprint()
            go_mod.write_text("module example.com/gomodprobe\n\ngo 1.22\n", encoding="utf-8")
            gomod_invalidates = fingerprint() != base
            go_mod.write_text("module example.com/gomodprobe\n\ngo 1.21\n", encoding="utf-8")
            go_sum.write_text("example.com/foo v1.0.0 h1:BBBB\n", encoding="utf-8")
            gosum_invalidates = fingerprint() != base
        finally:
            go_mod.unlink(missing_ok=True)
            go_sum.unlink(missing_ok=True)
        ok = gomod_invalidates and gosum_invalidates
        return ok, f"go.mod_invalidates={gomod_invalidates} go.sum_invalidates={gosum_invalidates}"

    def _check_toolchain(self) -> tuple[bool, str]:
        self._reset_logs()
        registry_path = self.root / "registry_toolchain.json"
        self._write_registry_file(
            registry_path,
            {"toolchain_probe": self._def(["src/toolchain_probe/"], python_tests=(_probe(self._log("toolchain_probe")),))},
        )
        report_a, code_a = self._run(["toolchain_probe"], toolchain=TOOLCHAIN_A, registry_path=registry_path)
        report_b, code_b = self._run(["toolchain_probe"], toolchain=TOOLCHAIN_B, registry_path=registry_path)
        report_a2, code_a2 = self._run(["toolchain_probe"], toolchain=TOOLCHAIN_A, registry_path=registry_path)
        ran = self._count("toolchain_probe")
        ok = (
            code_a == 0
            and code_b == 0
            and code_a2 == 0
            and report_a["components"]["toolchain_probe"]["cache_hit"] is False
            and report_b["components"]["toolchain_probe"]["cache_hit"] is False  # B is a MISS
            and report_a2["components"]["toolchain_probe"]["cache_hit"] is True  # A is cached
            and ran == 2
        )
        return ok, f"ran={ran} A_hit_again={report_a2['components']['toolchain_probe']['cache_hit']}"

    def _check_command(self) -> tuple[bool, str]:
        self._reset_logs()
        log_a = self._log("cmd_probe_a")
        log_b = self._log("cmd_probe_b")
        registry_path = self.root / "registry_cmd.json"

        def definition(command: list[str]) -> dict[str, Any]:
            return self._def(["src/cmd_probe/"], python_tests=(command,))

        self._write_registry_file(registry_path, {"cmd_probe": definition(_probe(log_a))})
        report_a, code_a = self._run(["cmd_probe"], toolchain=TOOLCHAIN_A, registry_path=registry_path)
        self._write_registry_file(registry_path, {"cmd_probe": definition(_probe(log_b))})
        report_b, code_b = self._run(["cmd_probe"], toolchain=TOOLCHAIN_A, registry_path=registry_path)
        ok = (
            code_a == 0
            and code_b == 0
            and report_a["components"]["cmd_probe"]["cache_hit"] is False
            and report_b["components"]["cmd_probe"]["cache_hit"] is False  # command change is a MISS
            and self._count("cmd_probe_a") == 1
            and self._count("cmd_probe_b") == 1
        )
        return ok, f"command_a_ran={self._count('cmd_probe_a')} command_b_ran={self._count('cmd_probe_b')}"

    def _check_touch(self) -> tuple[bool, str]:
        source = self.root / "src/audio/audio.go"
        self._reset_logs()
        before = self.runner.component_fingerprint(
            self.runner.load_registry(self.registry_path), "audio", "fast", False, self.root
        )
        os.utime(source, (1_000_000_000, 1_000_000_000))
        after = self.runner.component_fingerprint(
            self.runner.load_registry(self.registry_path), "audio", "fast", False, self.root
        )
        report, code = self._run(["audio"])
        audio = report["components"]["audio"]
        ok = (
            before == after
            and code == 0
            and audio["cache_hit"] is True
            and self._count("audio") == 0
        )
        return ok, f"fingerprint_unchanged={before == after} cache_hit={audio['cache_hit']}"

    def _check_failure(self) -> tuple[bool, str]:
        source = self.root / "src/audio/audio.go"
        original = source.read_text(encoding="utf-8")
        try:
            # Introduce a failing gate: the FAIL marker makes the command exit
            # non-zero, and the changed source gives a distinct fingerprint.
            self._reset_logs()
            source.write_text(original + "// FAIL\n", encoding="utf-8")
            report, code = self._run(["audio"])
            audio = report["components"]["audio"]
            failing_fingerprint = audio.get("fingerprint")
            never_cached = not (
                failing_fingerprint
                and self.runner.cache_entry_path(self.root, "audio", failing_fingerprint).is_file()
            )

            # Fix the gate with new content (not the pre-failure content), so
            # the fingerprint is novel and the gate must actually re-run.
            self._reset_logs()
            source.write_text(original + "// fixed\n", encoding="utf-8")
            report_fixed, code_fixed = self._run(["audio"])
            audio_fixed = report_fixed["components"]["audio"]
        finally:
            source.write_text(original, encoding="utf-8")
        ok = (
            code != 0
            and audio["status"] == "FAIL"
            and never_cached
            and code_fixed == 0
            and audio_fixed["cache_hit"] is False
            and self._count("audio") == 1
            and bool(self._cache_entries("audio"))
        )
        return ok, f"fail_code={code} fixed_code={code_fixed} failed_never_cached={never_cached}"

    def _check_corrupt(self) -> tuple[bool, str]:
        entries = self._cache_entries("kernel")
        if not entries:
            return False, "no kernel cache entry to corrupt"
        for entry in entries:
            entry.write_text("{corrupt", encoding="utf-8")
        self._reset_logs()
        report, code = self._run(["kernel"])
        kernel = report["components"]["kernel"]
        ok = (
            code == 0
            and kernel["cache_hit"] is False  # fail-closed: corrupt entry re-runs
            and self._count("kernel") == 1
        )
        return ok, f"code={code} cache_hit={kernel['cache_hit']} ran={self._count('kernel')}"

    def _check_race(self) -> tuple[bool, str]:
        self._reset_logs()
        report, code = self._run(["kernel"], mode="race")
        kernel = report["components"]["kernel"]
        ok = code == 0 and kernel["cache_hit"] is False and self._count("kernel") == 1
        return ok, f"code={code} cache_hit={kernel['cache_hit']} ran={self._count('kernel')}"

    def _check_live(self) -> tuple[bool, str]:
        self._reset_logs()
        report_a, code_a = self._run(["stock"], include_live=True)
        report_b, code_b = self._run(["stock"], include_live=True)
        stock_a = report_a["components"]["stock"]
        stock_b = report_b["components"]["stock"]
        ok = (
            code_a == 0
            and code_b == 0
            and stock_a["cache_hit"] is False
            and stock_b["cache_hit"] is False
            and self._count("stock") == 2  # RUN RUN, never RUN CACHED
            and not self._cache_entries("stock")
        )
        return ok, f"ran={self._count('stock')} cached_entries={len(self._cache_entries('stock'))}"

    # ------------------------------------------------------------------- run

    def run(self) -> None:
        checks: list[tuple[str, Callable[[], tuple[bool, str]]]] = [
            ("cold run executes every gate", self._check_cold),
            ("warm run reuses every gate", self._check_warm),
            ("working-tree invalidation", self._check_working_tree),
            ("staged invalidation", self._check_staged),
            ("untracked invalidation", self._check_untracked),
            ("dependency invalidation", self._check_dependency),
            ("test invalidation", self._check_test),
            ("go.mod invalidation", self._check_go_manifests),
            ("toolchain invalidation", self._check_toolchain),
            ("command invalidation", self._check_command),
            ("touch does not invalidate (content-addressed)", self._check_touch),
            ("failure never cached", self._check_failure),
            ("corrupt cache fail-closed", self._check_corrupt),
            ("race cache isolated", self._check_race),
            ("live gates uncached", self._check_live),
        ]
        for name, fn in checks:
            self._record(name, fn)


def _format_report(cert: Certification) -> str:
    lines = ["VERIFY-MAIN CACHE CERTIFICATION", ""]
    for block in cert.blocks:
        lines.extend(block)
        lines.append("")
    for name, passed, detail in cert.results:
        prefix = "PASS" if passed else "FAIL"
        lines.append(f"{prefix} {name}" + ("" if passed else f": {detail}"))
    lines.append("")
    lines.append("CERTIFIED" if all(passed for _, passed, _ in cert.results) else "FAILED")
    return "\n".join(lines)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--component-runner",
        type=Path,
        default=RUNNER_PATH,
        help="component runner to exercise (default scripts/ci/verify-component.py)",
    )
    parser.add_argument(
        "--report",
        type=Path,
        default=None,
        help="optional JSON destination for the machine-readable result",
    )
    parser.add_argument(
        "--keep-sandbox",
        action="store_true",
        help="retain the temporary sandbox directory for inspection",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        runner = _load_runner(args.component_runner)
        with Certification(runner, keep_sandbox=args.keep_sandbox) as cert:
            cert.run()
    except CertificationError as exc:
        print(f"VERIFY_CACHE_CERTIFICATION_CONFIG_ERROR {exc}", file=sys.stderr)
        return 2

    report_text = _format_report(cert)
    print(report_text)

    all_passed = all(passed for _, passed, _ in cert.results)
    machine_report = {
        "schema_version": 1,
        "final": "CERTIFIED" if all_passed else "FAILED",
        "blocks": cert.blocks,
        "checks": [
            {"name": name, "status": "PASS" if passed else "FAIL", "detail": detail}
            for name, passed, detail in cert.results
        ],
    }
    if args.report is not None:
        runner.write_json_report(args.report, machine_report)
    return 0 if all_passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
