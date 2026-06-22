# External Worker Definition of Done

> STATUS: ACTIVE
>
> This checklist is the only valid basis for declaring `pipelinegen-worker` operational.

## Certification identity

Fill before approval:

```text
commit SHA:
release/tag:
server image digest:
worker image digest:
database migration version:
registered job types:
worker count tested:
certification environment:
certification date:
reviewer:
```

All evidence must refer to the same commit and environment.

## Gate 1 — Registry and handlers

- [ ] one canonical remote worker registry exists;
- [ ] registry contains at least one real handler;
- [ ] no no-op/fake handler is registered;
- [ ] every handler calls a canonical application use case;
- [ ] no handler duplicates in-process business logic;
- [ ] empty registry fails startup;
- [ ] nil handler rejected;
- [ ] duplicate registration rejected;
- [ ] unsupported dispatch returns a typed error;
- [ ] `JobTypes()` is deterministic and sorted;
- [ ] registry is safe for concurrent reads;
- [ ] registry cannot be mutated after claim loop starts;
- [ ] handler tests are green;
- [ ] race detector is green.

## Gate 2 — Capability safety

- [ ] capabilities are derived from the registry;
- [ ] environment configuration can only narrow registered types;
- [ ] malformed capability JSON fails startup;
- [ ] empty configured capability list fails startup;
- [ ] unknown configured job type fails startup;
- [ ] empty claim filter is rejected for remote workers;
- [ ] unsupported jobs are never claimed;
- [ ] supported jobs are claimed with the exact type filter;
- [ ] registered capabilities are logged and observable;
- [ ] there is no second manually maintained capability list.

## Gate 3 — Startup and configuration

- [ ] config file must exist and parse;
- [ ] config validation is enforced;
- [ ] required master URL resolves correctly;
- [ ] worker token is required outside development;
- [ ] invalid token fails registration;
- [ ] missing token fails registration when auth enabled;
- [ ] master preflight is bounded by timeout;
- [ ] worker does not claim before successful registration;
- [ ] worker ID/name/version are explicit;
- [ ] build version and commit are logged;
- [ ] workspace is writable;
- [ ] required binaries are verified;
- [ ] startup failure exits non-zero;
- [ ] startup cleanup runs.

## Gate 4 — Broker and session

- [ ] registration creates a valid session;
- [ ] heartbeat renews session;
- [ ] expired session cannot claim;
- [ ] expired session cannot report progress;
- [ ] expired session cannot complete or fail;
- [ ] stale worker session cannot mutate a job;
- [ ] invalid worker/session pair rejected;
- [ ] broker errors are classified;
- [ ] long-poll cancellation works;
- [ ] no busy loop on broker failure;
- [ ] reconnect/re-registration behavior documented and tested.

## Gate 5 — Lease and fencing

- [ ] claim returns lease ID and expected revision;
- [ ] complete validates worker ID, lease ID and revision;
- [ ] fail validates worker ID, lease ID and revision;
- [ ] progress validates active ownership;
- [ ] long jobs renew lease before expiry;
- [ ] renewal stops on cancellation/terminal state;
- [ ] lease loss prevents late terminal writes;
- [ ] two workers cannot own the same active lease;
- [ ] late completion is rejected;
- [ ] crash recovery occurs after lease expiry;
- [ ] zero duplicate terminal completions in tests.

## Gate 6 — Handler execution

For every registered job type:

- [ ] payload version validated;
- [ ] required fields validated;
- [ ] malformed payload classified non-retryable;
- [ ] context cancellation propagated;
- [ ] progress bounded and monotonic;
- [ ] transient failures classified retryable;
- [ ] permanent failures classified non-retryable;
- [ ] handler is idempotent or protected by idempotency key;
- [ ] result serializes to JSON;
- [ ] output contract documented;
- [ ] no global mutable state;
- [ ] no temp file leak;
- [ ] retry does not duplicate side effects.

## Gate 7 — Asset transfer

- [ ] input asset IDs validated;
- [ ] asset download stays inside workspace;
- [ ] path traversal rejected;
- [ ] interrupted download handled;
- [ ] corrupted input handled;
- [ ] duplicate input IDs normalized;
- [ ] output paths validated;
- [ ] arbitrary host files cannot be uploaded;
- [ ] output upload is idempotent;
- [ ] retry after upload does not duplicate output;
- [ ] missing required output fails job;
- [ ] optional missing output behavior documented;
- [ ] workspace cleanup occurs after terminal acknowledgement.

## Gate 8 — Runtime separation

- [ ] production server mode does not start local job runner;
- [ ] external worker is the intended executor;
- [ ] compatibility `--mode all` is isolated from production default;
- [ ] production Compose does not run local and remote workers unintentionally;
- [ ] scheduler ownership is explicit;
- [ ] maintenance ownership is explicit;
- [ ] singleton jobs are not started by every replica;
- [ ] worker image contains required media tools;
- [ ] server image remains lightweight;
- [ ] server and worker share only required volumes;
- [ ] runtime mode is visible in logs/metrics.

## Gate 9 — Health, readiness and observability

- [ ] server `/health` works;
- [ ] server `/ready` verifies broker/store availability;
- [ ] worker registration state observable;
- [ ] worker heartbeat age observable;
- [ ] worker capabilities observable;
- [ ] current running job count observable;
- [ ] claim, complete and fail counters available;
- [ ] lease-loss counter available;
- [ ] local runner enabled/disabled metric available;
- [ ] no high-cardinality job IDs in metric labels;
- [ ] alerts exist for missing heartbeat;
- [ ] alerts exist for claim/failure loops;
- [ ] logs do not expose tokens.

## Gate 10 — Graceful shutdown and recovery

- [ ] idle worker exits cleanly on SIGTERM;
- [ ] active worker stops new claims before shutdown;
- [ ] active worker drains within grace window or cancels safely;
- [ ] forced kill produces recoverable job state;
- [ ] server restart behavior tested;
- [ ] worker restart behavior tested;
- [ ] heartbeat/session recovery tested;
- [ ] no job lost after restart;
- [ ] no job completed twice after restart;
- [ ] orphan workspace files controlled.

## Gate 11 — Migration and database safety

- [ ] migration 068 clean-database test passes;
- [ ] upgrade from 067 test passes;
- [ ] repeat startup after 068 passes;
- [ ] partial schema policy documented and tested;
- [ ] existing rows preserved;
- [ ] width default verified;
- [ ] height default verified;
- [ ] group_name default verified;
- [ ] media search smoke passes;
- [ ] clip search smoke passes;
- [ ] `PRAGMA integrity_check` returns `ok`;
- [ ] `PRAGMA foreign_key_check` is clean;
- [ ] backup and restore drill completed.

## Gate 12 — Tests and CI

- [ ] `go test ./internal/application/jobs/worker/...` passes;
- [ ] `go test ./internal/infrastructure/jobs/local/...` passes;
- [ ] `go test ./internal/infrastructure/remote/...` passes;
- [ ] `go test ./internal/api/workers/...` passes;
- [ ] `go test -race ./internal/application/jobs/worker/...` passes;
- [ ] `go vet` passes for touched packages;
- [ ] `go build ./cmd/server` passes;
- [ ] `go build ./cmd/worker` passes;
- [ ] `go build ./...` passes;
- [ ] Docker server image builds;
- [ ] Docker worker image builds;
- [ ] `docker compose config` passes;
- [ ] required GitHub checks are green;
- [ ] no direct-to-main unreviewed code change remains.

## Gate 13 — End-to-end scenarios

- [ ] worker registers;
- [ ] worker heartbeats at least twice;
- [ ] supported job completes;
- [ ] unsupported job remains unclaimed;
- [ ] two workers produce one claim/one completion;
- [ ] duplicate enqueue does not duplicate side effects;
- [ ] worker crash recovery passes;
- [ ] server restart recovery passes;
- [ ] lease-loss fencing passes;
- [ ] asset input/output path passes;
- [ ] authentication negative tests pass;
- [ ] graceful drain passes;
- [ ] 4-hour soak passes;
- [ ] 24-hour soak passes before production approval.

## Gate 14 — Documentation and operations

- [ ] `WORKER_EXECUTION.md` reflects current state;
- [ ] W1 updated with completed evidence;
- [ ] W2 updated with completed evidence;
- [ ] W3 certification report committed;
- [ ] W4 migration evidence committed;
- [ ] worker runbook exists;
- [ ] token rotation documented;
- [ ] rollback documented;
- [ ] compatibility-mode fallback documented;
- [ ] known unsupported job types listed;
- [ ] owner/on-call responsibility defined.

## Required final commands

Run from a clean checkout of the candidate commit:

```bash
git status -sb
go mod tidy
git diff --exit-code -- go.mod go.sum
go test ./internal/application/jobs/worker/...
go test ./internal/infrastructure/jobs/local/...
go test ./internal/infrastructure/remote/...
go test ./internal/api/workers/...
go test -race ./internal/application/jobs/worker/...
go vet ./cmd/worker/... ./internal/application/jobs/worker/... ./internal/infrastructure/jobs/local/...
go build ./cmd/server
go build ./cmd/worker
go build ./...
docker compose config
docker compose build --no-cache
```

Then run the complete W3 E2E script sequence and W4 migration verification.

## Approval

```text
[ ] APPROVED — external worker is operational
[ ] APPROVED — production server/worker separation is active
[ ] APPROVED — multi-worker execution is safe for declared job types

Commit SHA: ______________________________________
Release/tag: ______________________________________
Registered job types: _____________________________
Maximum worker count tested: ______________________
Reviewer: _________________________________________
Date: _____________________________________________
Known limits: _____________________________________
```

## Absolute rule

If code, job contracts, broker semantics, migrations, worker image or runtime topology changes after certification, the affected gates must be rerun. Approval belongs to a specific commit and environment, not permanently to the repository name.
