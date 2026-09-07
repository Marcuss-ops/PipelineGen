"""Shared constants, errors and value types for component verification."""
from __future__ import annotations

import json
import shlex
from dataclasses import dataclass
from pathlib import Path
from typing import Any

DEFAULT_TIMEOUT_SECONDS = 300
DEFAULT_REGISTRY = Path("config/verify-components.json")
DEFAULT_REPORT = Path("artifacts/verify/latest.json")
EXIT_CONFIG_ERROR = 2
EXIT_FAILURE = 1
EXIT_TIMEOUT = 124

# Bump this whenever the fingerprint input contract changes (new inputs, a
# different ordering, or a different hashing strategy).  A cache entry produced
# under an older schema must never be treated as a hit.
FINGERPRINT_SCHEMA_VERSION = "verify-fingerprint-schema-v1"

# Cache record schema version and on-disk location.  This is local tooling
# state (Git-ignored and fully rebuildable), never business state.
CACHE_SCHEMA_VERSION = 1
CACHE_DIR = ".cache/pipelinegen/verify"


class RegistryError(ValueError):
    """The registry cannot be safely executed."""


class VerificationError(RuntimeError):
    """A verification command failed or timed out."""


@dataclass(frozen=True)
class Command:
    argv: tuple[str, ...]
    kind: str
    source: str

    @property
    def key(self) -> tuple[str, ...]:
        return self.argv

    @property
    def display(self) -> str:
        return shlex.join(self.argv)


@dataclass(frozen=True)
class CommandResult:
    status: str
    exit_code: int | None
    duration_ms: int
    timed_out: bool = False

    def as_dict(self) -> dict[str, Any]:
        return {
            "status": self.status,
            "exit_code": self.exit_code,
            "duration_ms": self.duration_ms,
            "timed_out": self.timed_out,
        }


@dataclass(frozen=True)
class Execution:
    result: CommandResult
    stdout: str = ""
    stderr: str = ""


@dataclass
class ComponentRun:
    name: str
    dependencies: list[str]
    commands: list[Command]
    timeout_seconds: float
    blocked_by: list[str]
    status: str = "PENDING"
    duration_ms: int = 0
    command_results: list[dict[str, Any]] | None = None
    skipped_live: list[str] | None = None
    race_skipped: bool = False
    fingerprint: str | None = None
    cache_hit: bool = False
    original_duration_ms: int | None = None

    def as_dict(self) -> dict[str, Any]:
        command_results = self.command_results or []
        result: dict[str, Any] = {
            "status": self.status,
            "duration_ms": self.duration_ms,
            "timeout_seconds": self.timeout_seconds,
            "commands": [command.display for command in self.commands],
            "packages": sum(command.kind == "go" for command in self.commands),
            "command_results": command_results,
            "dependencies": self.dependencies,
            "blocked_by": self.blocked_by,
            "skipped_live": self.skipped_live or [],
            "race_skipped": self.race_skipped,
            "fingerprint": self.fingerprint,
            "cache_hit": self.cache_hit,
        }
        if self.original_duration_ms is not None:
            result["original_duration_ms"] = self.original_duration_ms
        return result
