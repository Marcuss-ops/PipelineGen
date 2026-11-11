#!/usr/bin/env python3
"""Verify the checked-in RenderingGen queue snapshot and optional sibling."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
VENDOR = ROOT / "third_party" / "queue"
LOCK = ROOT / "third_party" / "queue.sync.json"
COMPATIBILITY = ROOT / "third_party" / "queue.compatibility.json"
COMPATIBILITY_DOC = ROOT / "docs" / "operations" / "renderinggen-queue-compatibility.md"
VENDOR_NOTE = "VENDOR.md"
LOCK_FILE = "queue.sync.json"


def queue_files(root: Path) -> dict[str, str]:
    files: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        relative = path.relative_to(root).as_posix()
        if relative in {VENDOR_NOTE, LOCK_FILE}:
            continue
        files[relative] = hashlib.sha256(path.read_bytes()).hexdigest()
    return files


def upstream_path(explicit: str | None) -> Path | None:
    candidate = explicit or os.environ.get("RENDERINGGEN_QUEUE_DIR")
    if candidate:
        path = Path(candidate).expanduser().resolve()
        if not path.is_dir():
            raise ValueError(f"RenderingGen queue directory does not exist: {path}")
        return path
    sibling = (ROOT.parent / "RenderingGen" / "queue").resolve()
    return sibling if sibling.is_dir() else None


def compare_trees(upstream: Path, vendor: Path) -> list[str]:
    errors: list[str] = []
    left = {p.relative_to(upstream).as_posix(): p for p in upstream.rglob("*") if p.is_file()}
    right = {
        p.relative_to(vendor).as_posix(): p
        for p in vendor.rglob("*")
        if p.is_file() and p.relative_to(vendor).as_posix() != VENDOR_NOTE
    }
    for name in sorted(left.keys() - right.keys()):
        errors.append(f"vendored queue is missing upstream file: {name}")
    for name in sorted(right.keys() - left.keys()):
        errors.append(f"vendored queue has file absent upstream: {name}")
    for name in sorted(left.keys() & right.keys()):
        if left[name].read_bytes() != right[name].read_bytes():
            errors.append(f"vendored queue differs from RenderingGen: {name}")
    return errors


def write_lock() -> dict[str, object]:
    version = (VENDOR / "VERSION").read_text(encoding="utf-8").strip()
    lock: dict[str, object] = {
        "schema_version": 1,
        "module": "github.com/Marcuss-ops/RenderingGen/queue",
        "version": version,
        "upstream_base_commit": "cd9749a",
        "files": queue_files(VENDOR),
    }
    LOCK.write_text(json.dumps(lock, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return lock


def run_tests(root: Path, label: str) -> int:
    print(f"RUN go test ./... ({label})")
    # Repository and migration integration tests share a disposable database;
    # serialize package execution so concurrent package tests cannot truncate
    # each other's queue rows or race schema setup.
    return subprocess.run(["go", "test", "-p=1", "./..."], cwd=root, check=False).returncode


def run_pipelinegen_adapter_tests(root: Path) -> int:
    print("RUN go test ./internal/platform/renderinggen (PipelineGen adapter)")
    return subprocess.run(
        ["go", "test", "-count=1", "./internal/platform/renderinggen"],
        cwd=root,
        check=False,
    ).returncode


def version_tuple(value: object) -> tuple[int, int, int] | None:
    if not isinstance(value, str):
        return None
    parts = value.removeprefix("v").split(".")
    if len(parts) != 3 or not all(part.isdigit() for part in parts):
        return None
    return tuple(int(part) for part in parts)  # type: ignore[return-value]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream", help="RenderingGen/queue path (defaults to sibling checkout if present)")
    parser.add_argument("--write-lock", action="store_true", help="refresh the checked-in snapshot digest lock")
    parser.add_argument("--tests", action="store_true", help="run vendored/upstream queue tests and PipelineGen's RenderingGen adapter tests")
    args = parser.parse_args()

    if args.write_lock:
        lock = write_lock()
        print(f"LOCKED {len(lock['files'])} queue files at version {lock['version']}")

    try:
        upstream = upstream_path(args.upstream)
    except ValueError as exc:
        print(f"FAIL {exc}", file=sys.stderr)
        return 2

    if not LOCK.is_file():
        print(f"FAIL missing snapshot lock: {LOCK.relative_to(ROOT)}", file=sys.stderr)
        return 2
    try:
        lock = json.loads(LOCK.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"FAIL cannot read queue snapshot lock: {exc}", file=sys.stderr)
        return 2

    errors: list[str] = []
    if lock.get("schema_version") != 1 or lock.get("module") != "github.com/Marcuss-ops/RenderingGen/queue":
        errors.append("queue snapshot lock has an unsupported schema or module path")
    actual_version = (VENDOR / "VERSION").read_text(encoding="utf-8").strip()
    if lock.get("version") != actual_version:
        errors.append(f"queue version {actual_version!r} differs from locked {lock.get('version')!r}")
    if lock.get("files") != queue_files(VENDOR):
        errors.append("vendored queue files drifted from third_party/queue.sync.json; review the upstream change and run --write-lock")

    try:
        compatibility = json.loads(COMPATIBILITY.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"FAIL cannot read queue compatibility matrix: {exc}", file=sys.stderr)
        return 2
    queue_module = compatibility.get("queue_module", {})
    server = compatibility.get("renderinggen_server", {})
    minimum = version_tuple(queue_module.get("minimum_version"))
    actual = version_tuple(actual_version)
    if compatibility.get("schema_version") != 1:
        errors.append("queue compatibility matrix has an unsupported schema")
    if actual is None or minimum is None or actual < minimum:
        errors.append(f"queue version {actual_version!r} does not meet matrix minimum {queue_module.get('minimum_version')!r}")
    if queue_module.get("verified_version") != actual_version:
        errors.append("matrix verified_version does not match third_party/queue/VERSION")
    if server.get("queue_module_version") != actual_version:
        errors.append("RenderingGen server matrix version does not match the vendored queue module")
    try:
        compatibility_doc = COMPATIBILITY_DOC.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"cannot read queue compatibility documentation: {exc}")
    else:
        supported_tuple = (
            f"| render request v2; job envelope v1 | `{actual_version}` | "
            f"queue module `{actual_version}` | Supported and pinned |"
        )
        if supported_tuple not in compatibility_doc:
            errors.append(
                "compatibility documentation's supported tuple does not match "
                f"the verified queue version {actual_version!r}"
            )
    feature_probes = {
        "atomic-submit-batch": ("client/client.go", "func (c *Client) SubmitBatch"),
        "idempotency-key-unique": ("migrations/011_idempotency_index.sql", "CREATE UNIQUE INDEX"),
        "lease-generation-fencing": ("migrations/030_lease_generation.sql", "lease_generation"),
        "job-children-read": ("client/client.go", "func (c *Client) Children"),
        "prepared-render-package": ("client/client.go", "type PreparedRenderPackageRef struct"),
        "prepared-family-contiguous-ranges": ("internal/model/model.go", "prepared render family starts at frame"),
        "prepared-family-total-frame-coverage": ("internal/model/model.go", "prepared render family ends at frame"),
        "prepared-package-profile-required": ("client/client.go", "prepared_render_package profile_id is required"),
        "idempotent-replay-request-match": ("internal/model/model.go", "func SameSubmitRequest(a, b Job) bool"),
        "atomic-idempotent-batch-replay": ("internal/service/service.go", "func (s *Service) SubmitBatchIdempotent(ctx context.Context, jobs []model.Job) (bool, error)"),
    }
    required_features = server.get("required_features", [])
    if not isinstance(required_features, list) or set(required_features) != set(feature_probes):
        errors.append("compatibility matrix required_features differs from the gate's certified feature set")
    for feature in required_features if isinstance(required_features, list) else []:
        probe = feature_probes.get(feature)
        if probe is None:
            continue
        relative, expected = probe
        source = VENDOR / relative
        if not source.is_file() or expected not in source.read_text(encoding="utf-8"):
            errors.append(f"queue snapshot is missing compatibility feature {feature!r} ({relative})")

    if upstream is None:
        print("INFO no sibling RenderingGen checkout; validating the pinned snapshot")
    else:
        errors.extend(compare_trees(upstream, VENDOR))
        upstream_version_file = upstream / "VERSION"
        if upstream_version_file.is_file():
            upstream_version = upstream_version_file.read_text(encoding="utf-8").strip()
            if upstream_version != actual_version:
                errors.append(f"upstream queue version {upstream_version!r} != vendored {actual_version!r}")
        print(f"INFO compared vendored queue with {upstream}")

    if errors:
        for error in errors:
            print(f"FAIL {error}", file=sys.stderr)
        return 1
    print(f"PASS RenderingGen queue snapshot {actual_version}: {len(lock['files'])} files verified")

    if args.tests:
        if run_tests(VENDOR, "vendored") != 0:
            return 1
        if upstream is not None and run_tests(upstream, "upstream") != 0:
            return 1
        if run_pipelinegen_adapter_tests(ROOT) != 0:
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
