# PipelineGen ↔ RenderingGen Queue compatibility

**DEMOL-05 / RGA-02:** PipelineGen consumes the reviewed Queue module release
`github.com/Marcuss-ops/RenderingGen/queue v0.1.0`. The source of truth is the
published module maintained by the RenderingGen repository. PipelineGen does
not require a sibling checkout or a vendored copy.

`make queue-module-check` and `percheck_queue_single_module` enforce the exact
version and reject local replacements, vendored Queue copies, and snapshot
locks. CI and Docker resolve the same version through Go module download, so
clean builds exercise the distribution path used by remote workers.

| PipelineGen producer | Queue module | RenderingGen worker/server | Status |
| --- | --- | --- | --- |
| render request v2; job envelope v1 | `v0.1.0` | Queue module `v0.1.0` | Published and pinned |
| any other/unverified version | any | any | Not certified |

The supported tuple requires atomic chunk-family submission, durable unique
idempotency keys, `lease_generation` fencing on worker transitions, and the
queue child lookup used to reconcile/replay chunk families. It also requires
the immutable prepared-render-package reference in the job contract, preserved
through batch submission, storage, claim and read. Every render chunk carries
the same package identity; the assembly-only anchor carries the final audio
plan. Queue ingress validates the complete family before mutation: one anchor,
one parent per child, dense `chunk_index` values from zero, and contiguous
half-open frame ranges in index order covering the prepared package's full
`total_frames`. Replays may reorder the HTTP array but cannot change indexed
render order or frame boundaries. The reference includes the compiler version
and output `profile_id`.

The tuple also requires durable three-state execution policy
(`job-execution-policy`): `render_jobs.execution_policy` is stored on submit
from producer `require_gpu` (omitted means `gpu_preferred`, `true` means
`gpu_only`, `false` means `cpu_only`), returned on every claim, and enforced by
the claim query against worker capability inventory.

## Updating the tuple

1. Land the contract change in `RenderingGen/queue` (migrations, client,
   server, tests), run its suite, and publish a new version.
2. Update the pinned version in PipelineGen's `go.mod` and this table in the
   same coordinated change.
3. From PipelineGen, run:
   ```sh
   make queue-module-check
   go mod verify
   go build ./...
   go test ./internal/platform/renderinggen ./internal/capabilities/scripts/renderqueue -count=1
   ```
4. Build the Docker target without any sibling build context.

The Queue repository's CI owns its PostgreSQL integration suite; PipelineGen
uses the published module as a consumer.
