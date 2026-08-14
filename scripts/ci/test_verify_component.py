#!/usr/bin/env python3
"""Focused tests for scripts/ci/verify-component.py."""

from __future__ import annotations

import importlib.util
import json
import os
import sys
import tempfile
import threading
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
                "paths": ["internal/base/"],
                "go_packages": ["./internal/base/..."],
                "node_tests": [],
                "python_tests": [],
                "dependencies": [],
                "timeout_seconds": 30,
                "race_enabled": True,
                "live_tests": [],
            },
            "child": {
                "paths": ["internal/child/"],
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

    @staticmethod
    def minimal_definition(**overrides: object) -> dict[str, object]:
        definition: dict[str, object] = {
            "paths": ["internal/minimal/"],
            "go_packages": [],
            "node_tests": [],
            "python_tests": [],
            "dependencies": [],
            "timeout_seconds": 30,
            "race_timeout_seconds": 30,
            "race_enabled": False,
            "live_tests": [],
        }
        definition.update(overrides)
        return definition

    @staticmethod
    def load_registry_json(registry: dict[str, object]) -> dict[str, dict[str, object]]:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "registry.json"
            path.write_text(json.dumps(registry), encoding="utf-8")
            return verify_component.load_registry(path)

    def test_cacheable_policy_defaults_fail_closed(self) -> None:
        loaded = self.load_registry_json(
            {
                "plain": self.minimal_definition(),
                "cached": self.minimal_definition(cacheable=True, cache_scope="content"),
            }
        )
        self.assertFalse(loaded["plain"]["cacheable"])
        self.assertEqual(loaded["plain"]["cache_scope"], "content")
        self.assertTrue(loaded["cached"]["cacheable"])

    def test_cacheable_must_be_boolean(self) -> None:
        registry = {"x": self.minimal_definition(cacheable="yes")}
        with self.assertRaisesRegex(verify_component.RegistryError, "cacheable must be boolean"):
            self.load_registry_json(registry)

    def test_cacheable_live_gate_rejected(self) -> None:
        registry = {
            "x": self.minimal_definition(
                cacheable=True,
                live_tests=[["make", "verify-x-live"]],
            )
        }
        with self.assertRaisesRegex(
            verify_component.RegistryError, "cacheable must be false when live_tests is non-empty"
        ):
            self.load_registry_json(registry)

    def test_cache_scope_must_be_non_empty_string(self) -> None:
        registry = {"x": self.minimal_definition(cache_scope="")}
        with self.assertRaisesRegex(verify_component.RegistryError, "cache_scope must be a non-empty string"):
            self.load_registry_json(registry)

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


    @staticmethod
    def write_tree(root: Path, files: dict[str, str]) -> None:
        for rel, content in files.items():
            path = root / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8")

    @staticmethod
    def cacheable_component(paths: Sequence[str], **overrides: object) -> dict[str, object]:
        definition: dict[str, object] = {
            "paths": list(paths),
            "go_packages": ["./internal/audio/..."],
            "node_tests": [],
            "python_tests": [],
            "dependencies": [],
            "timeout_seconds": 30,
            "race_timeout_seconds": 30,
            "race_enabled": True,
            "live_tests": [],
            "cacheable": True,
            "cache_scope": "content",
        }
        definition.update(overrides)
        return definition

    @staticmethod
    def _fingerprint(registry, name, mode, root, toolchain):
        return verify_component.component_fingerprint(registry, name, mode, False, root, toolchain)

    def test_fingerprint_is_stable_for_identical_tree(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "internal/audio/compiler_test.go": "package audio\n",
                "go.mod": "module example\n\ngo 1.23\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0 linux/amd64"}
            first = self._fingerprint(registry, "audio", "fast", root, toolchain)
            second = self._fingerprint(registry, "audio", "fast", root, toolchain)
        self.assertEqual(first, second)
        self.assertEqual(len(first), 64)

    def test_fingerprint_changes_when_source_content_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "audio", "fast", root, toolchain)
            self.write_tree(root, {"internal/audio/compiler.go": "package audio\n\nfunc Render() {}\n"})
            after = self._fingerprint(registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_changes_when_test_content_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "internal/audio/compiler_test.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "audio", "fast", root, toolchain)
            self.write_tree(root, {"internal/audio/compiler_test.go": "package audio\n\nfunc TestRender() {}\n"})
            after = self._fingerprint(registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_changes_when_dependency_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "internal/base/lib.go": "package base\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {
                "base": self.cacheable_component(["internal/base/"], go_packages=["./internal/base/..."]),
                "audio": self.cacheable_component(["internal/audio/"], dependencies=["base"]),
            }
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "audio", "fast", root, toolchain)
            self.write_tree(root, {"internal/base/lib.go": "package base\n\nfunc Helper() {}\n"})
            after = self._fingerprint(registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_changes_when_go_mod_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n\ngo 1.23\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "audio", "fast", root, toolchain)
            self.write_tree(root, {"go.mod": "module example\n\ngo 1.24\n"})
            after = self._fingerprint(registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_changes_when_toolchain_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            before = self._fingerprint(registry, "audio", "fast", root, {"go": "go version go1.23.0"})
            after = self._fingerprint(registry, "audio", "fast", root, {"go": "go version go1.24.0"})
        self.assertNotEqual(before, after)

    def test_fingerprint_changes_when_command_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            before_registry = {"audio": self.cacheable_component(["internal/audio/"], go_packages=["./internal/audio/..."])}
            after_registry = {"audio": self.cacheable_component(["internal/audio/"], go_packages=["./internal/audio/...", "./internal/audio/extra/..."])}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(before_registry, "audio", "fast", root, toolchain)
            after = self._fingerprint(after_registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_differs_between_fast_and_race(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            fast = self._fingerprint(registry, "audio", "fast", root, toolchain)
            race = self._fingerprint(registry, "audio", "race", root, toolchain)
        self.assertNotEqual(fast, race)

    def test_fingerprint_includes_untracked_file(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "audio", "fast", root, toolchain)
            # A brand-new, never-committed file must participate in the hash.
            self.write_tree(root, {"internal/audio/untracked.go": "package audio\n"})
            after = self._fingerprint(registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_reported_for_cacheable_component(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=self.fake_runner, dry_run=True
            )
        self.assertEqual(code, 0)
        fingerprint = report["components"]["audio"]["fingerprint"]
        self.assertIsNotNone(fingerprint)
        self.assertEqual(len(fingerprint), 64)


    def test_cache_pass_roundtrip(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fingerprint = "a" * 64
            ok = verify_component.store_cache_pass(
                root, "audio", fingerprint, 1234, "fast", {"go": "go version go1.23.0"}
            )
            entry = verify_component.read_cache_entry(root, "audio", fingerprint)
        self.assertTrue(ok)
        self.assertIsNotNone(entry)
        self.assertEqual(entry["status"], "PASS")
        self.assertEqual(entry["gate"], "audio")
        self.assertEqual(entry["fingerprint"], fingerprint)
        self.assertEqual(entry["duration_ms"], 1234)
        self.assertEqual(entry["mode"], "fast")

    def test_pass_run_is_cached(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=self.fake_runner
            )
            fingerprint = report["components"]["audio"]["fingerprint"]
            entry = verify_component.read_cache_entry(root, "audio", fingerprint)
        self.assertEqual(code, 0)
        self.assertIsNotNone(entry)
        self.assertEqual(entry["status"], "PASS")
        self.assertEqual(entry["fingerprint"], fingerprint)

    def test_cache_write_is_atomic_without_partial_files(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fingerprint = "b" * 64
            record = {"status": "PASS", "fingerprint": fingerprint, "gate": "audio"}
            ok = verify_component.write_cache_entry(root, "audio", fingerprint, record)
            entry_dir = verify_component.cache_entry_path(root, "audio", fingerprint).parent
            files = [p.name for p in entry_dir.iterdir()]
        self.assertTrue(ok)
        self.assertEqual(files, [f"{fingerprint}.json"])

    def test_failed_run_is_never_cached(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}

            def failing_runner(argv, timeout, cwd):
                return verify_component.Execution(verify_component.CommandResult("FAIL", 1, 3))

            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=failing_runner
            )
            cache = verify_component.cache_root(root)
        self.assertEqual(code, verify_component.EXIT_FAILURE)
        self.assertEqual(report["components"]["audio"]["status"], "FAIL")
        self.assertFalse(cache.exists() and any(cache.rglob("*.json")))

    def test_timeout_run_is_never_cached(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}

            def timeout_runner(argv, timeout, cwd):
                return verify_component.Execution(
                    verify_component.CommandResult("TIMEOUT", None, 30, timed_out=True)
                )

            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=timeout_runner
            )
            cache = verify_component.cache_root(root)
        self.assertEqual(code, verify_component.EXIT_TIMEOUT)
        self.assertFalse(cache.exists() and any(cache.rglob("*.json")))

    def test_dry_run_does_not_cache(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}

            def unexpected_runner(argv, timeout, cwd):
                raise AssertionError("dry-run must not invoke commands")

            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=unexpected_runner, dry_run=True
            )
            cache = verify_component.cache_root(root)
        self.assertEqual(code, 0)
        self.assertEqual(report["components"]["audio"]["status"], "PASS")
        self.assertFalse(cache.exists() and any(cache.rglob("*.json")))

    def test_corrupt_entry_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fingerprint = "c" * 64
            entry_path = verify_component.cache_entry_path(root, "audio", fingerprint)
            entry_path.parent.mkdir(parents=True, exist_ok=True)
            entry_path.write_text("{ not valid json", encoding="utf-8")
            entry = verify_component.read_cache_entry(root, "audio", fingerprint)
        self.assertIsNone(entry)

    def test_read_cache_entry_rejects_non_pass(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fingerprint = "d" * 64
            verify_component.write_cache_entry(
                root, "audio", fingerprint, {"status": "FAIL", "fingerprint": fingerprint}
            )
            entry = verify_component.read_cache_entry(root, "audio", fingerprint)
        self.assertIsNone(entry)

    def test_read_cache_entry_rejects_fingerprint_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fingerprint = "e" * 64
            verify_component.write_cache_entry(
                root, "audio", fingerprint, {"status": "PASS", "fingerprint": "f" * 64}
            )
            entry = verify_component.read_cache_entry(root, "audio", fingerprint)
        self.assertIsNone(entry)


    def test_cache_hit_skips_commands_and_reports_cached_pass(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            first, first_code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=self.fake_runner
            )
            self.assertEqual(first_code, 0)
            self.assertEqual(first["components"]["audio"]["status"], "PASS")

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            second, second_code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner
            )
            component = second["components"]["audio"]
        self.assertEqual(second_code, 0)
        self.assertEqual(calls, [])
        self.assertEqual(component["status"], "CACHED_PASS")
        self.assertTrue(component["cache_hit"])
        self.assertIsNotNone(component["original_duration_ms"])
        self.assertEqual(second["final"], "PASS")

    def test_cache_hit_misses_when_source_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            verify_component.run_components(registry, ["audio"], repo_root=root, runner=self.fake_runner)

            # Changing a registered source file invalidates the fingerprint.
            self.write_tree(root, {"internal/audio/compiler.go": "package audio\n\nfunc Render() {}\n"})

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            second, second_code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner
            )
            component = second["components"]["audio"]
        self.assertEqual(second_code, 0)
        self.assertEqual(len(calls), 1)
        self.assertEqual(component["status"], "PASS")
        self.assertFalse(component["cache_hit"])

    def test_cache_hit_entry_rejects_schema_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fingerprint = "a" * 64
            record = {
                "schema_version": 1,
                "cache_schema_version": "verify-fingerprint-schema-v0",
                "gate": "audio",
                "fingerprint": fingerprint,
                "status": "PASS",
            }
            verify_component.write_cache_entry(root, "audio", fingerprint, record)
            entry = verify_component.cache_hit_entry(root, "audio", fingerprint)
        self.assertIsNone(entry)

    def test_cached_dependency_satisfies_dependent(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "internal/base/lib.go": "package base\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {
                "base": self.cacheable_component(["internal/base/"], go_packages=["./internal/base/..."]),
                "audio": self.cacheable_component(["internal/audio/"], dependencies=["base"]),
            }
            verify_component.run_components(registry, ["audio"], repo_root=root, runner=self.fake_runner)

            # Change only audio's source: base still hits its cache, audio re-runs.
            self.write_tree(root, {"internal/audio/compiler.go": "package audio\n\nfunc NewRender() {}\n"})

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner
            )
            base = report["components"]["base"]
            audio = report["components"]["audio"]
        self.assertEqual(code, 0)
        self.assertEqual(base["status"], "CACHED_PASS")
        self.assertTrue(base["cache_hit"])
        self.assertEqual(audio["status"], "PASS")
        self.assertFalse(audio["cache_hit"])
        self.assertEqual(len(calls), 1)
        self.assertEqual(report["final"], "PASS")


    def test_cache_summary_counts_hits_and_misses(self) -> None:
        components = {
            "audio": {"cache_hit": True, "original_duration_ms": 5000, "status": "CACHED_PASS"},
            "render": {"cache_hit": False, "duration_ms": 2000, "status": "PASS"},
            "stock": {"cache_hit": False, "duration_ms": 3000, "status": "PASS"},
            "blocked": {"cache_hit": False, "status": "BLOCKED"},
        }
        summary = verify_component.cache_summary(components)
        self.assertEqual(summary["hits"], 1)
        self.assertEqual(summary["misses"], 2)
        self.assertEqual(summary["executed"], 2)
        self.assertEqual(summary["saved_ms"], 5000)
        self.assertEqual(len(summary["gates"]), 3)

    def test_format_cache_summary_lists_hits_and_misses(self) -> None:
        summary = {
            "hits": 1,
            "misses": 1,
            "executed": 1,
            "saved_ms": 5000,
            "gates": [
                {"gate": "audio", "result": "HIT", "duration_ms": 5000},
                {"gate": "render", "result": "MISS", "duration_ms": 2000},
            ],
        }
        text = verify_component.format_cache_summary(summary)
        self.assertIn("VERIFY CACHE", text)
        self.assertIn("hits=1", text)
        self.assertIn("misses=1", text)
        self.assertIn("saved_ms=5000", text)
        self.assertIn("HIT   saved 5.0s", text)
        self.assertIn("MISS  2.0s", text)


    def test_fingerprint_changes_when_go_sum_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "audio", "fast", root, toolchain)
            self.write_tree(root, {"go.sum": "example v1.0.1 h1:def\n"})
            after = self._fingerprint(registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_hashes_go_package_source_outside_paths(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "node-scraper/index.js": "module.exports = {}\n",
                "internal/infra/scraper/scraper.go": "package scraper\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"ns": self.cacheable_component(
                ["node-scraper/"],
                go_packages=["./internal/infra/scraper/..."],
                node_tests=[["npm", "--prefix", "node-scraper", "test"]],
            )}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "ns", "fast", root, toolchain)
            self.write_tree(root, {"internal/infra/scraper/scraper.go": "package scraper\n\nfunc Fetch() {}\n"})
            after = self._fingerprint(registry, "ns", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_changes_when_registry_paths_change(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "internal/audio_extra/extra.go": "package extra\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            toolchain = {"go": "go version go1.23.0"}
            before_registry = {"audio": self.cacheable_component(["internal/audio/"])}
            after_registry = {"audio": self.cacheable_component(["internal/audio/", "internal/audio_extra/"])}
            before = self._fingerprint(before_registry, "audio", "fast", root, toolchain)
            after = self._fingerprint(after_registry, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)

    def test_fingerprint_changes_when_dependency_list_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "internal/base/lib.go": "package base\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            toolchain = {"go": "go version go1.23.0"}
            base = self.cacheable_component(["internal/base/"], go_packages=["./internal/base/..."])
            no_dep = {"base": base, "audio": self.cacheable_component(["internal/audio/"])}
            with_dep = {"base": base, "audio": self.cacheable_component(["internal/audio/"], dependencies=["base"])}
            before = self._fingerprint(no_dep, "audio", "fast", root, toolchain)
            after = self._fingerprint(with_dep, "audio", "fast", root, toolchain)
        self.assertNotEqual(before, after)


    def test_live_gate_is_never_cached(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/live/probe.go": "package probe\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {
                "live": {
                    "paths": ["internal/live/"],
                    "go_packages": ["./internal/live/..."],
                    "node_tests": [],
                    "python_tests": [],
                    "dependencies": [],
                    "timeout_seconds": 30,
                    "race_timeout_seconds": 30,
                    "race_enabled": True,
                    "live_tests": [["make", "verify-live"]],
                    "cacheable": False,
                    "cache_scope": "live",
                }
            }
            report, code = verify_component.run_components(
                registry, ["live"], repo_root=root, runner=self.fake_runner, include_live=True
            )
            cache = verify_component.cache_root(root)
        self.assertEqual(code, 0)
        self.assertEqual(report["components"]["live"]["status"], "PASS")
        self.assertFalse(cache.exists() and any(cache.rglob("*.json")))

    def test_corrupt_cache_entry_fails_closed_and_runs(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            fingerprint = verify_component.component_fingerprint(
                registry, "audio", "fast", False, root, toolchain
            )
            entry_path = verify_component.cache_entry_path(root, "audio", fingerprint)
            entry_path.parent.mkdir(parents=True, exist_ok=True)
            entry_path.write_text("{ corrupt", encoding="utf-8")

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner, toolchain=toolchain
            )
            component = report["components"]["audio"]
        self.assertEqual(code, 0)
        self.assertEqual(len(calls), 1)
        self.assertEqual(component["status"], "PASS")
        self.assertFalse(component["cache_hit"])

    def test_cache_hit_misses_when_test_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "internal/audio/compiler_test.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            verify_component.run_components(registry, ["audio"], repo_root=root, runner=self.fake_runner)

            # Changing only the test file invalidates the fingerprint.
            self.write_tree(root, {"internal/audio/compiler_test.go": "package audio\n\nfunc TestRender() {}\n"})

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            second, second_code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner
            )
            component = second["components"]["audio"]
        self.assertEqual(second_code, 0)
        self.assertEqual(len(calls), 1)
        self.assertEqual(component["status"], "PASS")
        self.assertFalse(component["cache_hit"])

    def test_cache_hit_misses_when_go_mod_changes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n\ngo 1.23\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            verify_component.run_components(registry, ["audio"], repo_root=root, runner=self.fake_runner)

            self.write_tree(root, {"go.mod": "module example\n\ngo 1.24\n"})

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            second, second_code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner
            )
            component = second["components"]["audio"]
        self.assertEqual(second_code, 0)
        self.assertEqual(len(calls), 1)
        self.assertEqual(component["status"], "PASS")
        self.assertFalse(component["cache_hit"])

    def test_race_and_fast_use_distinct_cache_entries(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            # Populate the fast cache entry.
            verify_component.run_components(registry, ["audio"], repo_root=root, runner=self.fake_runner, mode="fast")

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            report, code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner, mode="race"
            )
            component = report["components"]["audio"]
        self.assertEqual(code, 0)
        self.assertEqual(len(calls), 1)
        self.assertIn("-race", calls[0])
        self.assertEqual(component["status"], "PASS")
        self.assertFalse(component["cache_hit"])


    def test_touch_does_not_invalidate_cache(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "internal/audio/compiler.go": "package audio\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {"audio": self.cacheable_component(["internal/audio/"])}
            toolchain = {"go": "go version go1.23.0"}
            before = self._fingerprint(registry, "audio", "fast", root, toolchain)

            # Populate the cache with a first run.
            verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=self.fake_runner, toolchain=toolchain
            )

            # Change only the mtime, keeping the content byte-identical.
            os.utime(root / "internal/audio/compiler.go", (1_000_000_000, 1_000_000_000))

            after = self._fingerprint(registry, "audio", "fast", root, toolchain)
            self.assertEqual(before, after, "content-addressed fingerprint must ignore mtime")

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            second, second_code = verify_component.run_components(
                registry, ["audio"], repo_root=root, runner=recording_runner, toolchain=toolchain
            )
            component = second["components"]["audio"]
        self.assertEqual(second_code, 0)
        self.assertEqual(calls, [])
        self.assertEqual(component["status"], "CACHED_PASS")
        self.assertTrue(component["cache_hit"])


    def test_concurrent_writers_are_atomic(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fingerprint = "a" * 64
            durations = list(range(100, 116))  # 16 distinct durations

            def write_one(duration_ms: int) -> None:
                verify_component.store_cache_pass(
                    root, "audio", fingerprint, duration_ms, "fast", {"go": "go version go1.23.0"}
                )

            threads = [threading.Thread(target=write_one, args=(d,)) for d in durations]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join()

            entry = verify_component.read_cache_entry(root, "audio", fingerprint)
            entry_dir = verify_component.cache_entry_path(root, "audio", fingerprint).parent
            files = [p.name for p in entry_dir.iterdir()]
        self.assertIsNotNone(entry, "concurrent writes must never leave truncated JSON")
        self.assertEqual(entry["status"], "PASS")
        self.assertEqual(entry["fingerprint"], fingerprint)
        self.assertIn(
            entry["duration_ms"],
            durations,
            "final record must be one whole write, not an interleaving",
        )
        self.assertEqual(len(files), 1, "no partial or temp files may remain")


    def test_required_artifacts_must_be_non_empty_strings(self) -> None:
        registry = {"x": self.minimal_definition(required_artifacts=[123])}
        with self.assertRaisesRegex(
            verify_component.RegistryError, "required_artifacts entries must be non-empty strings"
        ):
            self.load_registry_json(registry)

    def test_cached_gate_does_not_hide_missing_required_artifact(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_tree(root, {
                "web/src/main.ts": "export {}\n",
                "web/package.json": "{}\n",
                "web/dist/index.html": "<html></html>\n",
                "go.mod": "module example\n",
                "go.sum": "example v1.0.0 h1:abc\n",
            })
            registry = {
                "web": {
                    "paths": ["web/src/", "web/package.json"],
                    "go_packages": [],
                    "node_tests": [["npm", "--prefix", "web", "run", "build"]],
                    "python_tests": [],
                    "dependencies": [],
                    "timeout_seconds": 30,
                    "race_timeout_seconds": 30,
                    "race_enabled": False,
                    "live_tests": [],
                    "required_artifacts": ["web/dist/index.html"],
                    "cacheable": True,
                    "cache_scope": "content",
                }
            }
            # First run populates the cache with the artifact present.
            first, first_code = verify_component.run_components(
                registry, ["web"], repo_root=root, runner=self.fake_runner
            )
            self.assertEqual(first_code, 0)
            self.assertEqual(first["components"]["web"]["status"], "PASS")

            # Delete the artifact the gate is supposed to produce.
            (root / "web/dist/index.html").unlink()

            calls: list[tuple[str, ...]] = []

            def recording_runner(argv, timeout, cwd):
                calls.append(tuple(argv))
                return verify_component.Execution(verify_component.CommandResult("PASS", 0, 1))

            second, second_code = verify_component.run_components(
                registry, ["web"], repo_root=root, runner=recording_runner
            )
            component = second["components"]["web"]
        self.assertEqual(second_code, 0)
        self.assertEqual(len(calls), 1, "a cached gate must re-run when its required artifact is missing")
        self.assertEqual(component["status"], "PASS")
        self.assertFalse(component["cache_hit"])


if __name__ == "__main__":
    unittest.main()
