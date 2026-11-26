# PipelineGen ↔ RenderingGen Queue compatibility

**DEMOL-05 (2026-11-26): one Queue module, one source of truth.**
`RenderingGen/queue` is the only copy. `refactored/third_party/queue/` and its
lock files no longer exist.

`go.mod` resolves the historical import path with a path replacement:

```
replace github.com/Marcuss-ops/RenderingGen/queue => ../RenderingGen/queue
```

The replacement is not a fork. The published repository name does not match the
historical module path (`github.com/Marcuss-ops/RenderingGen` returns 404), so no
versioned reference can fetch it. The monorepo layout (and CI, which checks out
RenderingGen as a sibling) supplies the module by path.

Do **not** reintroduce a vendored copy, a snapshot lock, or a synchronization
script. `make queue-module-check` (run by `verify-architecture`) fails if
`third_party/queue` exists or the replacement is not the sibling path.

Docker builds supply the module through the `renderinggen-queue` named build
context; `make/docker.mk` passes
`--build-context renderinggen-queue=../RenderingGen/queue` and the Dockerfile
copies it to `/RenderingGen/queue/`.

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

The tuple also requires the durable three-state execution policy
(`job-execution-policy`): `render_jobs.execution_policy` is stored on submit
from the producer's `require_gpu` (omitted means `gpu_preferred`, `true` means
`gpu_only`, `false` means `cpu_only`), returned on every claim, and enforced by
the claim query, which hides a `gpu_only` job from a worker whose registered
capability inventory does not include `gpu`. A producer therefore keeps
declaring `require_gpu`; it never has to send the derived policy itself.

## Updating the tuple

1. Land the contract change in `RenderingGen/queue` (migrations, client,
   server, tests) and run its suite there.
2. Update the table above in the same change if the supported version moves.
3. From `refactored/`, run:
   ```sh
   make queue-module-check
   go build ./...
   go test ./internal/platform/renderinggen ./internal/capabilities/scripts/renderqueue -count=1
   ```
4. Verify the `renderinggen-queue` Docker build context still produces the
   builder target (`docker buildx build --target builder --build-context
   renderinggen-queue=../RenderingGen/queue .`).

The queue repository's own CI owns the PostgreSQL integration suite; PipelineGen
no longer runs a second copy of those tests against the vendored tree.
