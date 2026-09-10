"""Deterministic component fingerprinting and toolchain detection."""
from __future__ import annotations

import hashlib
import json
import os
import shlex
import subprocess
import sys
from pathlib import Path
from typing import Any, Mapping

from verify_component_core import DEFAULT_TIMEOUT_SECONDS, FINGERPRINT_SCHEMA_VERSION
from verify_component_registry import build_commands

def _update(hasher: Any, data: bytes) -> None:
    """Feed length-prefixed bytes so concatenation is unambiguous."""
    hasher.update(len(data).to_bytes(8, "big"))
    hasher.update(data)


def _read_bytes(path: Path) -> bytes:
    """Read a file, returning a stable marker when it is missing/unreadable."""
    try:
        return path.read_bytes()
    except OSError:
        return b"<unreadable>"


def _iter_regular_files(directory: Path) -> list[str]:
    """Return sorted relative paths of regular (non-symlink) files under a directory."""
    rel_paths: list[str] = []
    for current, dirnames, filenames in os.walk(directory):
        dirnames.sort()
        filenames.sort()
        current_path = Path(current)
        for filename in filenames:
            file_path = current_path / filename
            if file_path.is_file() and not file_path.is_symlink():
                rel_paths.append(str(file_path.relative_to(directory)))
    return rel_paths


def _hash_path(root: Path, entry: str) -> str:
    """Hash the real working-tree content of one registry path.

    Content is read straight from the filesystem, so committed, staged, and
    untracked files are all represented — never just ``git rev-parse HEAD``.
    Missing or unreadable paths degrade to a stable marker so the fingerprint
    stays deterministic instead of silently dropping inputs.
    """
    target = root / entry
    hasher = hashlib.sha256()
    if target.is_file() and not target.is_symlink():
        _update(hasher, entry.encode("utf-8"))
        _update(hasher, _read_bytes(target))
    elif target.is_dir() and not target.is_symlink():
        for rel in _iter_regular_files(target):
            _update(hasher, f"{entry}/{rel}".encode("utf-8"))
            _update(hasher, _read_bytes(target / rel))
    else:
        _update(hasher, f"<missing:{entry}>".encode("utf-8"))
    return hasher.hexdigest()


def _go_package_dir(package: str) -> str:
    """Derive the filesystem directory a Go package pattern points at."""
    value = package
    if value.startswith("./"):
        value = value[2:]
    if value.endswith("/..."):
        value = value[:-4]
    elif value.endswith("..."):
        value = value[:-3]
    return value.rstrip("/")


def detect_toolchain(root: Path) -> dict[str, str]:
    """Probe toolchain versions that influence builds, best effort.

    Probes are intentionally best effort: a missing tool simply drops out of
    the fingerprint, and its commands would fail anyway.  Python's version is
    taken from the interpreter instead of a subprocess.
    """
    toolchain: dict[str, str] = {}
    for key, argv in (("go", ("go", "version")), ("node", ("node", "--version"))):
        try:
            completed = subprocess.run(
                list(argv),
                cwd=str(root),
                check=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=5,
            )
        except (OSError, subprocess.TimeoutExpired):
            continue
        value = (completed.stdout + completed.stderr).strip()
        if value:
            toolchain[key] = value
    toolchain["python"] = (
        f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}"
    )
    return toolchain


def component_fingerprint(
    registry: Mapping[str, Mapping[str, Any]],
    name: str,
    mode: str,
    include_live: bool,
    root: Path,
    toolchain: Mapping[str, str] | None = None,
    _memo: dict[tuple[str, str, bool], str] | None = None,
) -> str:
    """Compute a deterministic content-addressed fingerprint for one component.

    The fingerprint covers: registered path contents (working tree), the exact
    command list, dependency fingerprints (transitively), Go/Node manifests,
    toolchain versions, GOOS/GOARCH, the verification mode, and the runner
    schema version.  Anything that can change a deterministic result changes
    the fingerprint; environment-dependent live state is excluded by design.
    """
    if _memo is None:
        _memo = {}
    key = (name, mode, include_live)
    if key in _memo:
        return _memo[key]

    definition = registry[name]
    hasher = hashlib.sha256()
    _update(hasher, FINGERPRINT_SCHEMA_VERSION.encode("utf-8"))
    _update(hasher, name.encode("utf-8"))
    _update(hasher, mode.encode("utf-8"))

    # Dependencies contribute their own content fingerprint, so a change deep
    # in the DAG invalidates every dependent component transitively.
    for dependency in sorted(definition.get("dependencies", [])):
        _update(hasher, b"dependency")
        _update(hasher, dependency.encode("utf-8"))
        _update(
            hasher,
            component_fingerprint(
                registry, dependency, mode, include_live, root, toolchain, _memo
            ).encode("utf-8"),
        )

    for entry in sorted(definition.get("paths", [])):
        _update(hasher, b"path")
        _update(hasher, entry.encode("utf-8"))
        _update(hasher, _hash_path(root, entry).encode("utf-8"))

    # Go package source directories can live outside the registered paths
    # (e.g. a Node component that also runs one Go package).  Hash them too so
    # a source or test change in those packages still invalidates the cache.
    for package in sorted(definition.get("go_packages", [])):
        directory = _go_package_dir(package)
        if not directory:
            continue
        _update(hasher, b"go-package-source")
        _update(hasher, directory.encode("utf-8"))
        _update(hasher, _hash_path(root, directory).encode("utf-8"))

    commands, _, _ = build_commands(name, definition, mode, include_live)
    for command in commands:
        _update(hasher, b"command")
        _update(hasher, shlex.join(command.argv).encode("utf-8"))

    _update(hasher, b"race_enabled")
    _update(hasher, ("true" if definition.get("race_enabled") else "false").encode("utf-8"))
    _update(hasher, b"timeout_seconds")
    _update(hasher, str(definition.get("timeout_seconds", DEFAULT_TIMEOUT_SECONDS)).encode("utf-8"))
    _update(hasher, b"race_timeout_seconds")
    _update(
        hasher,
        str(
            definition.get(
                "race_timeout_seconds", definition.get("timeout_seconds", DEFAULT_TIMEOUT_SECONDS)
            )
        ).encode("utf-8"),
    )

    if definition.get("go_packages"):
        for manifest in ("go.mod", "go.sum"):
            _update(hasher, manifest.encode("utf-8"))
            _update(hasher, _read_bytes(root / manifest))
    if definition.get("node_tests"):
        for manifest in ("package.json", "package-lock.json", "npm-shrinkwrap.json"):
            target = root / manifest
            if target.is_file():
                _update(hasher, manifest.encode("utf-8"))
                _update(hasher, _read_bytes(target))

    for tool in sorted(toolchain or {}):
        _update(hasher, tool.encode("utf-8"))
        _update(hasher, toolchain[tool].encode("utf-8"))
    _update(hasher, ("GOOS=" + os.environ.get("GOOS", "")).encode("utf-8"))
    _update(hasher, ("GOARCH=" + os.environ.get("GOARCH", "")).encode("utf-8"))

    fingerprint = hasher.hexdigest()
    _memo[key] = fingerprint
    return fingerprint


