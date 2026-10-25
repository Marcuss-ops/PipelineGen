"""Regression guard for the rust/timeline parallel-run flake.

The `rust` component executes ``./internal/platform/media/rustexec/...`` and the
`timeline` component executes ``./internal/platform/media/...`` — a superset
that contains the very same test binary. When the component runner was free to
run them concurrently, `verify-changed` executed the rustexec end-to-end suite
twice at the same time; both copies spawn the real Rust workers and the
semantic planner assertion flaked with a query that was reported as not
grounded.

The registry prevents that by declaring a dependency edge (`timeline` depends
on `rust`), which makes the runner serialise the two components. This test fails
if the edge disappears or if a third component starts executing the rustexec
package set without an ordering edge.

Run directly from the repository root:

    python3 scripts/ci/test_verify_components_concurrency.py
"""

from __future__ import annotations

import json
import unittest
from pathlib import Path

REGISTRY = Path("config/verify-components.json")
RUSTEXEC_PACKAGE = "./internal/platform/media/rustexec/..."


def _package_base(pattern: str) -> str:
    """Return the directory a Go package pattern governs, minus the ellipsis."""
    value = pattern.strip()
    if value.startswith("./"):
        value = value[2:]
    if value == "...":
        return ""
    if value.endswith("/..."):
        return value[: -len("/...")].rstrip("/")
    return value


def _pattern_covers(outer: str, inner: str) -> bool:
    """Does ``outer`` select every package ``inner`` selects?"""
    outer_base = _package_base(outer)
    inner_base = _package_base(inner)
    if outer == inner:
        return True
    if not outer.rstrip().endswith("/...") and outer != "...":
        return False
    if outer_base == "":
        return True
    return inner_base == outer_base or inner_base.startswith(outer_base + "/")


def _patterns_overlap(left: str, right: str) -> bool:
    return _pattern_covers(left, right) or _pattern_covers(right, left)


def _depends_on(registry: dict[str, dict], component: str, target: str, seen: set[str] | None = None) -> bool:
    """Transitive dependency check over the registry DAG."""
    if component == target:
        return True
    seen = seen or set()
    if component in seen:
        return False
    seen.add(component)
    for dependency in registry.get(component, {}).get("dependencies", []):
        if _depends_on(registry, dependency, target, seen):
            return True
    return False


def _load_registry() -> dict[str, dict]:
    return json.loads(REGISTRY.read_text(encoding="utf-8"))


class RustTimelineConcurrencyRegressionTest(unittest.TestCase):
    def setUp(self) -> None:
        self.registry = _load_registry()

    def test_registry_declares_the_components(self) -> None:
        for name in ("rust", "timeline"):
            self.assertIn(name, self.registry, f"registry must declare the {name!r} component")

    def test_timeline_runs_the_rust_suite_first(self) -> None:
        self.assertTrue(
            _depends_on(self.registry, "timeline", "rust"),
            "timeline must depend on rust: both execute the rustexec test binary, "
            "and running them concurrently reproduces the semantic-planner flake",
        )

    def test_no_component_runs_rustexec_concurrently_with_another(self) -> None:
        components = sorted(self.registry)
        owners = [
            name
            for name in components
            if any(_patterns_overlap(package, RUSTEXEC_PACKAGE) for package in self.registry[name]["go_packages"])
        ]
        self.assertIn("rust", owners, "the rust component must own the rustexec consumer suite")
        for index, left in enumerate(owners):
            for right in owners[index + 1 :]:
                self.assertTrue(
                    _depends_on(self.registry, left, right) or _depends_on(self.registry, right, left),
                    f"{left} and {right} both execute {RUSTEXEC_PACKAGE} but neither depends on the "
                    "other; the runner may execute the same real-worker suite twice at once",
                )


if __name__ == "__main__":
    unittest.main()
