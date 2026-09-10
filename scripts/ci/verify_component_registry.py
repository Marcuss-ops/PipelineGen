"""Declarative registry loading, validation and command planning."""
from __future__ import annotations

import json
import shlex
import time
from pathlib import Path
from typing import Any, Iterable, Mapping

from verify_component_core import Command, DEFAULT_TIMEOUT_SECONDS, RegistryError

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

        # Content-addressed verification cache policy. Fail-closed default: a
        # component is cacheable only when explicitly declared so, and live
        # gates (they depend on external state, not just code) must never be
        # cached.
        cacheable = value.get("cacheable", False)
        if not isinstance(cacheable, bool):
            raise RegistryError(f"component={name}: cacheable must be boolean")
        cache_scope = value.get("cache_scope", "content")
        if not isinstance(cache_scope, str) or not cache_scope.strip():
            raise RegistryError(f"component={name}: cache_scope must be a non-empty string")
        live_entries = _require_list(value.get("live_tests"), "live_tests", name)
        if cacheable and live_entries:
            raise RegistryError(f"component={name}: cacheable must be false when live_tests is non-empty")
        required_artifacts = _require_list(value.get("required_artifacts"), "required_artifacts", name)
        if any(not isinstance(entry, str) or not entry.strip() for entry in required_artifacts):
            raise RegistryError(f"component={name}: required_artifacts entries must be non-empty strings")

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
        registry[name]["cacheable"] = cacheable
        registry[name]["cache_scope"] = cache_scope
        registry[name]["required_artifacts"] = required_artifacts

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


