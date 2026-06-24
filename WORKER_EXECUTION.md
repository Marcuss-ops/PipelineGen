# PipelineGen Worker Execution Runbook

> STATUS: CURRENT
>
> OWNER: runtime / worker maintainers

This document describes the current remote worker contract. The old empty
registry migration plan is complete; keep this file as an operational runbook,
not as a historical task list.

## Runtime Topology

PipelineGen supports two deliberate execution modes:

- `server --mode all`: HTTP server plus in-process jobs runner. This is the
  local development default.
- `server --mode server` plus `cmd/worker`: HTTP server only, with an external
  worker claiming jobs through `/internal/v1/workers/*`.

Production Compose uses the split topology. `pipelinegen-server` starts with:

```text
--mode server --config /etc/pipelinegen/config.yaml
```

`pipelinegen-worker` starts as a separate container and connects to the server
through `VELOX_MASTER_URL`.

## Worker Startup Contract

`cmd/worker` builds a worker-only composition root, then calls:

```go
app.BuildWorkerRegistry(root)
```

`BuildWorkerRegistry` is the single source of truth for remote worker
capabilities:

- it copies every registered handler from the in-process jobs dispatcher;
- it adapts each handler to the remote worker `Tools` shape;
- it derives capability job types from the registry itself;
- it returns `worker.ErrNoHandlers` when the dispatcher has no handlers.

The worker also checks `registry.Len()` and exits non-zero if no handlers are
registered. An empty registry must never start and claim jobs.

## Capability Selection

By default, the worker advertises every job type present in the registry.

`VELOX_WORKER_CAPABILITIES` may restrict that set, but it must be valid JSON
with a non-empty `job_types` array. Unknown job types are rejected during
startup.

Example:

```json
{"job_types":["media.reindex"]}
```

Do not maintain a separate manual capability list. Add or remove capabilities
by registering or removing the corresponding job handler in the canonical jobs
dispatcher.

## Authentication

Remote worker routes live under:

```text
/internal/v1/workers/*
/internal/v1/worker-assets/*
```

They require `VELOX_WORKER_TOKEN`. Admin tokens are not accepted for worker
broker calls.

## Verification

Before changing worker wiring, run:

```bash
go test ./internal/app/... -short
go test ./cmd/worker/...
go build ./cmd/worker/
```

For full local runtime verification, start the server and worker in split mode:

```bash
docker compose up --build pipelinegen-server pipelinegen-worker
```

Expected signals:

- server healthcheck passes;
- worker logs `worker registry built` with at least one handler;
- worker registration succeeds against `/internal/v1/workers/register`;
- invalid `VELOX_WORKER_CAPABILITIES` makes the worker fail fast.

## Maintenance Rules

- Keep the in-process dispatcher as the canonical handler registry.
- Do not introduce a second worker-only registry or hand-written capability
  table.
- Do not allow an empty capability set to mean "claim every job".
- Keep server/worker split mode explicit; do not run `server --mode all` and an
  external worker against the same production queue unless intentionally
  testing compatibility behavior.
