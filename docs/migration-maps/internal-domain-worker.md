# `internal/domain/worker` — Burned Down (this PR)

## Status

**done** — deleted in the PR that introduced the legacy-directory CI guard
(Check 13 in `scripts/ci-architectural-checks.sh`).

## What existed

A single file, `internal/domain/worker/worker.go`:

```go
package worker

type Worker struct {
    ID           string   `json:"id"`
    Capabilities []string `json:"capabilities"`
    MaxLeases    int      `json:"max_leases"`
}
```

## Migration target

The canonical worker concept lives in two layers:

- `internal/domain/job/` exposes `job.Job` (the runtime job abstraction — a unit
  of work that workers claim, lease, complete). It is the authoritative shape
  for "what a job looks like" when a worker handles it.
- `internal/application/jobs/Runner` is the production worker abstraction: it
  owns the lease/fence logic and the dispatch loop.

Before deletion we verified there is no third concept a `Worker` struct (with
`Capabilities` / `MaxLeases`) is currently modelling. The struct was a stub
written in anticipation of a multi-worker cluster topology that has not been
implemented.

## Audit results

Audit method: `rg 'internal/domain/worker' --type go` AND
`rg 'worker\.Worker\b|domain\.Worker\b' --type go` AND
`grep -rn "Worker{" --include='*.go' internal/`.

| Probe | Result |
|---|---|
| Go files importing `internal/domain/worker` | **0** (zero importers) |
| References to `worker.Worker{…}` literal | 0 |
| References to the `Capabilities` field shape | 0 (the `Capabilities []string` pattern is used everywhere but with different struct owners, none of which is `domain/worker`) |
| References to `MaxLeases` | 0 |

Net: deleting `internal/domain/worker/worker.go` does **not** break a Go type,
function, or constant reference.

## Cut-over steps (this PR, executed)

1. Verified the audit results above (this doc).
2. Removed `internal/domain/worker/worker.go` (the only file).
3. Removed `internal/domain/worker/` directory.
4. Updated `architecture/migration.yaml` (the `internal/domain/worker` entry —
   note `scripts/archcheck/baseline.json` did not contain a separate entry
   for `internal/domain/worker`, only for `internal/domain/outbox`).
5. Verified `go build ./...` and `go vet ./...` remain green.

## What to do if you need a Worker model in the future

When the multi-worker cluster topology arrives, place the model at
`internal/domain/job/worker.go` (the adjacent canonical domain package), and
import from there. Do **not** resurrect `internal/domain/worker/`.
