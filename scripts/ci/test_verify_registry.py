#!/usr/bin/env python3
"""Contract tests for the registry-backed component verification targets."""

from __future__ import annotations

import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[2]
REGISTRY_PATH = ROOT / "config" / "verify-components.json"
MAKE_COMPONENTS_PATH = ROOT / "make" / "verify.components.mk"
MAKEFILE_PATH = ROOT / "Makefile"


class VerifyRegistryContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.registry = json.loads(REGISTRY_PATH.read_text(encoding="utf-8"))
        cls.make_components = MAKE_COMPONENTS_PATH.read_text(encoding="utf-8")
        cls.makefile = MAKEFILE_PATH.read_text(encoding="utf-8")

    def test_expanded_components_are_registered(self) -> None:
        expected = {
            "script", "research", "clips", "stock", "qdrant", "indexing",
            "drive", "docs", "voiceover", "images", "translation", "timeline",
            "storage", "database", "jobs", "api", "ollama", "youtube",
            "artlist", "node-scraper", "kernel",
        }
        self.assertEqual(len(self.registry), 21)
        self.assertEqual(set(self.registry), expected)

    def test_entries_have_real_paths_and_go_packages(self) -> None:
        for name, definition in self.registry.items():
            self.assertTrue(definition["paths"], name)
            if not definition.get("utility", False):
                self.assertTrue(definition["go_packages"], name)
            self.assertGreater(definition["timeout_seconds"], 0, name)
            self.assertIsInstance(definition["race_enabled"], bool, name)
            for registered_path in definition["paths"]:
                self.assertTrue(
                    (ROOT / registered_path).exists(),
                    f"{name}: missing registry path {registered_path}",
                )

    def test_dependency_graph_is_known_and_acyclic(self) -> None:
        expected = {
            "research": {"database", "script"},
            "qdrant": {"database"},
            "indexing": {"database", "qdrant"},
            "docs": {"database", "drive"},
            "voiceover": {"database", "drive"},
            "database": set(),
            "jobs": {"database"},
        }
        for component, dependencies in expected.items():
            self.assertEqual(set(self.registry[component]["dependencies"]), dependencies)
            self.assertTrue(dependencies <= self.registry.keys())

        visiting: set[str] = set()
        visited: set[str] = set()

        def visit(name: str) -> None:
            self.assertNotIn(name, visiting, f"dependency cycle at {name}")
            if name in visited:
                return
            visiting.add(name)
            for dependency in self.registry[name]["dependencies"]:
                visit(dependency)
            visiting.remove(name)
            visited.add(name)

        for component in self.registry:
            visit(component)

    def test_new_make_aliases_are_thin_registry_runner_calls(self) -> None:
        for component in (
            "research",
            "qdrant",
            "indexing",
            "docs",
            "voiceover",
            "database",
            "jobs",
        ):
            pattern = rf"(?m)^verify-{component}:\n\t@\$\(VERIFY_COMPONENT_RUNNER\) {component} \$\(VERIFY_COMPONENT_FLAGS\)$"
            self.assertRegex(self.make_components, pattern)
            self.assertNotRegex(
                self.make_components,
                rf"(?m)^verify-{component}:.*\n(?:\t.*\n)*\t.*verify-fast",
            )
            phony_section = self.makefile.split("# help", 1)[0]
            self.assertIn(f"verify-{component}", phony_section)
            self.assertIn(f'@echo "  make verify-{component}', self.makefile)

    def test_registry_component_commands_are_available_in_dry_run(self) -> None:
        for name in (
            "research",
            "qdrant",
            "indexing",
            "docs",
            "voiceover",
            "database",
            "jobs",
        ):
            commands = [f"go test {package}" for package in self.registry[name]["go_packages"]]
            self.assertTrue(commands, name)
            self.assertTrue(all(re.match(r"^go test \.?/", command) for command in commands))


if __name__ == "__main__":
    unittest.main()
