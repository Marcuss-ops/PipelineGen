# PipelineGen ↔ RenderingGen Queue compatibility

The canonical compatibility data lives in
[`third_party/queue.compatibility.json`](../../third_party/queue.compatibility.json).
The CI gate checks the vendored module version against this matrix and verifies
the pinned snapshot. In a workspace containing both repositories, the same
gate also compares every queue module file byte-for-byte.

| PipelineGen producer | Queue module | RenderingGen worker/server | Status |
| --- | --- | --- | --- |
| render request v2; job envelope v1 | `0.1.2` | queue module `0.1.2` | Supported and pinned |
| any other/unverified version | any | any | Not certified |

The supported tuple requires atomic chunk-family submission, durable unique
idempotency keys, `lease_generation` fencing on worker transitions, and the
queue child lookup used to reconcile/replay chunk families. It also requires
the immutable prepared-render-package reference in the job contract, preserved
through batch submission, storage, claim and read. Every render chunk must
carry the same package identity; the assembly-only anchor has no package
reference because it carries the final audio plan, not the child video plan.
Queue ingress validates the complete family before mutation: one anchor, one
parent for each child, dense `chunk_index` values from zero, and contiguous
half-open frame ranges in index order that cover the prepared package's full
`total_frames`. A replay may reorder the HTTP array, but cannot change the
indexed render order or any frame boundary. The reference also requires the
compiler version and output `profile_id`, so the worker cannot reuse a package
whose rendering profile is unspecified.
The `--tests` gate runs the queue suite in both the vendored and available
upstream modules, then runs `go test -count=1 ./internal/platform/renderinggen`
in PipelineGen. In CI it provisions an isolated PostgreSQL 18 service and sets
`RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1`, so repository/migration integration
tests cannot silently skip. Queue package tests run serially against that
disposable database to avoid truncation and schema-setup races.

## Updating the tuple

Sync the upstream module and its host fixtures as described in
[`third_party/queue/VENDOR.md`](../../third_party/queue/VENDOR.md). Review the
upstream diff, update `queue.compatibility.json` only after verifying the
required API and state-transition behavior, regenerate the snapshot lock with
`python3 scripts/ci/check_queue_vendor.py --write-lock`, then run
`python3 scripts/ci/check_queue_vendor.py --tests`.
