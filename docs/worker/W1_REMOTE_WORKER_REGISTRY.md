# W1 — Remote worker registry and capability safety

> PRIORITY: P0
>
> STATUS: pending
>
> BLOCKS: W2, W3 and production use of `pipelinegen-worker`

## Objective

Make `cmd/worker` start with a non-empty canonical handler registry and advertise exactly the job types it can execute.

After this work:

- a remote worker cannot start without handlers;
- a remote worker cannot claim unsupported jobs;
- capabilities are derived from the same registry used for dispatch;
- an unsupported job remains queued for another compatible worker;
- no job is failed merely because the worker claimed a type it never supported;
- no second job registry or dispatcher is introduced.

## Current defect

The current flow is effectively:

```go
registry := worker.NewRegistry()
runner := worker.NewRunner(broker, registry, ...)
runner.Run(ctx)
```

No handler is registered between `NewRegistry()` and `Run()`.

When `VELOX_WORKER_CAPABILITIES` is empty, the worker sends no job types. The local broker converts an empty type list to `nil`, which allows `ClaimNext` to search without a type filter. The worker can therefore claim any job and then fail at `registry.Dispatch` with `no worker handler registered`.

## Required branch

```text
codex/worker-handler-registry
```

## Allowed scope

Primary files:

```text
cmd/worker/main.go
internal/application/jobs/worker/registry.go
internal/application/jobs/worker/runner.go
internal/application/jobs/worker/*_test.go
internal/infrastructure/jobs/local/broker.go
internal/infrastructure/remote/jobbrokerclient/**
internal/api/workers/**
internal/app/**worker**.go
```

A new focused package is allowed only when it becomes the canonical builder for remote handlers, for example:

```text
internal/app/workerregistry/
```

or

```text
internal/application/jobs/worker/handlers/
```

Choose the location after inspecting existing ownership. Do not create both.

Secondary files may be modified only to expose existing use cases through a remote-safe handler.

## Out of scope

- new job types;
- replacing the job store;
- changing SQLite to PostgreSQL;
- new queue technology;
- API route redesign;
- YouTube refactor unrelated to remote execution;
- Docker mode cutover;
- broad composition-root cleanup;
- architecture baseline updates.

## Phase 0 — Inventory before coding

Run:

```bash
git fetch origin
git checkout main
git pull --ff-only origin main
rg 'Register\(' internal/application/jobs internal/app internal/api --type go
rg 'JobType|job type|Type:' internal/application internal/domain internal/app --type go
rg 'NewRegistry\(' --type go
rg 'NewRunner\(' --type go
rg 'ClaimNext\(' --type go
rg 'VELOX_WORKER_CAPABILITIES|WorkerCapabilities' --type go
```

Build a table in the PR description:

| Job type | Existing canonical handler/use case | Required dependencies | Remote-safe now | Input assets | Output assets |
|---|---|---|---:|---|---|
| example | exact package/symbol | exact dependencies | yes/no | IDs/files | files/result |

Rules:

- Do not implement a remote handler for a job with no canonical use case.
- Do not copy handler logic from the in-process dispatcher.
- Do not mark a handler remote-safe if it depends on process-local state unavailable to `cmd/worker`.
- Start with the smallest genuinely supported subset.

Checklist:

- [ ] list all job types currently registered in the in-process dispatcher;
- [ ] list all job types claimed by the remote broker API;
- [ ] identify canonical use case for each candidate remote type;
- [ ] identify required files/assets;
- [ ] identify dependencies available inside worker runtime;
- [ ] classify unsupported types explicitly.

## Phase 1 — Strengthen the registry API

Enhance the existing registry instead of introducing a parallel list.

Required behavior:

```go
type Registry struct {
    // existing synchronization and handlers
}

func (r *Registry) Register(jobType string, h Handler) error
func (r *Registry) Dispatch(ctx context.Context, j *domainjob.Job, tools *Tools) (map[string]any, error)
func (r *Registry) JobTypes() []string
func (r *Registry) Len() int
func (r *Registry) Has(jobType string) bool
```

Requirements:

- `JobTypes()` returns a defensive copy;
- types are sorted for deterministic logs and tests;
- empty and whitespace-only types are rejected;
- duplicate registration is rejected;
- nil handlers are rejected;
- registry remains safe for concurrent reads;
- registry freezes before the claim loop, or registration is otherwise impossible after startup;
- no caller can mutate the internal map.

Tests:

- [ ] register valid handler;
- [ ] reject empty type;
- [ ] reject whitespace-only type;
- [ ] reject nil handler;
- [ ] reject duplicate;
- [ ] deterministic sorted `JobTypes()`;
- [ ] `Len()` and `Has()`;
- [ ] concurrent reads;
- [ ] dispatch supported type;
- [ ] dispatch unsupported type returns typed/sentinel error.

Prefer a sentinel error:

```go
var ErrHandlerNotRegistered = errors.New("worker handler not registered")
```

so tests and runner logic do not parse strings.

## Phase 2 — Canonical handler builder

Create one builder that receives already-constructed dependencies and registers the supported remote handlers.

Indicative shape:

```go
type Deps struct {
    // only dependencies required by supported remote handlers
}

func BuildRegistry(deps Deps) (*worker.Registry, error) {
    r := worker.NewRegistry()
    // register supported handlers
    if r.Len() == 0 {
        return nil, ErrNoHandlers
    }
    return r, nil
}
```

Constraints:

- maximum 8–10 fields in `Deps`;
- do not pass a global `ComposeRoot` into individual handlers;
- do not use a service locator;
- handler calls canonical application use cases;
- handler does not duplicate business logic;
- infrastructure remains outside application;
- each registration has a focused test.

If no existing job type can be executed remotely with the current binary, stop and document the missing dependencies. Do not register fake/no-op handlers.

## Phase 3 — Derive capabilities from the registry

Remove the independent manual capability source as the authority.

Canonical flow:

```go
registry, err := BuildRegistry(deps)
if err != nil {
    log.Fatal("build worker registry", zap.Error(err))
}

jobTypes := registry.JobTypes()
if len(jobTypes) == 0 {
    log.Fatal("worker has no registered job handlers")
}

caps := appjobs.WorkerCapabilities{
    JobTypes: jobTypes,
    // other measured capabilities if present
}
```

Environment configuration may narrow capabilities, but it must never advertise a type missing from the registry.

Safe narrowing algorithm:

```text
configured set ∩ registered set
```

Rules:

- missing environment variable → all registered types;
- configured unknown type → startup error;
- configured empty JSON array → startup error;
- malformed JSON → startup error, not silent empty capabilities;
- duplicate configured values → normalize;
- final set sorted and non-empty.

Do not keep the current behavior that silently converts malformed capability JSON to an empty struct.

Tests:

- [ ] no env → registered types;
- [ ] valid subset → subset;
- [ ] unknown type → error;
- [ ] malformed JSON → error;
- [ ] empty array → error;
- [ ] duplicate values normalized;
- [ ] final capabilities exactly equal claim filter.

## Phase 4 — Fail closed in `cmd/worker`

Startup sequence must be:

```text
load config
validate config
initialize logging
initialize remote-safe dependencies
build canonical registry
validate non-empty registry
derive capabilities from registry
preflight server readiness
register worker with derived capabilities
start heartbeat
start claim loop
```

Required changes:

- [ ] registry built before registration;
- [ ] empty registry exits non-zero;
- [ ] capability parse error exits non-zero;
- [ ] registered capabilities logged;
- [ ] version/commit logged;
- [ ] worker token required when server auth requires it;
- [ ] no claim begins before successful registration;
- [ ] cleanup runs on startup failure.

Recommended exit codes:

```text
2 configuration error
3 dependency/wiring error
4 registration error
```

## Phase 5 — Fail closed in broker claim

Remote workers must not use an empty type list as “all types”.

Choose one canonical enforcement point:

### Preferred

Reject worker registration when `JobTypes` is empty.

Also reject claim requests with empty capabilities as defense in depth.

Indicative behavior:

```go
if len(cmd.Capabilities) == 0 {
    return nil, appjobs.ErrNoWorkerCapabilities
}
```

Do not break the in-process runner if it legitimately uses an unfiltered store call. The remote broker path and in-process runner path must remain semantically distinct.

Tests:

- [ ] empty registration capabilities rejected;
- [ ] empty claim capabilities rejected;
- [ ] unsupported type never passed to `ClaimNext`;
- [ ] supported type filter passed exactly;
- [ ] session validation still required;
- [ ] lease/revision checks unchanged.

## Phase 6 — Implement the first real handlers

Implement only handlers proven remote-safe by Phase 0.

Each handler must:

1. validate payload version;
2. validate required input fields;
3. stage input assets through `Tools` when required;
4. call the canonical use case;
5. report bounded progress;
6. return serializable output metadata;
7. expose output files through the existing output contract;
8. respect context cancellation;
9. avoid global mutable state;
10. be idempotent or use an existing idempotency key.

Handler test matrix:

- [ ] valid payload;
- [ ] malformed payload;
- [ ] missing asset;
- [ ] cancelled context;
- [ ] canonical use case error;
- [ ] output serialization;
- [ ] output upload contract;
- [ ] retry/idempotency;
- [ ] no temp file leak.

## Phase 7 — Runner safety

The runner must not turn a programming/configuration mismatch into silent queue damage.

Required behavior:

- startup proves registry/capabilities match;
- claimed job type must satisfy `registry.Has(job.Type)`;
- mismatch emits a high-severity error and does not execute;
- choose an explicit broker outcome for impossible mismatch:
  - release/requeue without consuming retry, if supported;
  - fail with non-retryable classification only if release is impossible.

Do not use a generic failure path without understanding retry semantics.

Additional requirements:

- renew long leases while handler runs;
- stop renew loop before completion/failure;
- preserve expected revision checks;
- cleanup workspace after terminal broker acknowledgement;
- classify transient and permanent failures.

If lease renewal is not currently active in the runner, create a focused follow-up PR or include it only if required to safely execute the first remote handler. Do not hide the omission.

## Phase 8 — Focused tests

Minimum commands:

```bash
gofmt -w cmd/worker internal/application/jobs/worker internal/infrastructure/jobs/local
go test ./internal/application/jobs/worker/...
go test ./internal/infrastructure/jobs/local/...
go test ./internal/api/workers/...
go test ./cmd/worker/...
go test -race ./internal/application/jobs/worker/...
go vet ./cmd/worker/... ./internal/application/jobs/worker/... ./internal/infrastructure/jobs/local/...
go build ./cmd/worker
```

Repository regression check:

```bash
go test ./internal/application/jobs/...
go test ./internal/app/...
go build ./...
```

Negative integration tests:

```text
empty registry → worker does not register
malformed capabilities → worker exits
unknown configured type → worker exits
empty claim filter → broker rejects
supported worker cannot claim unsupported job
unsupported job remains available for compatible worker
```

## Exit gate

W1 is complete only when:

- [ ] one canonical registry exists;
- [ ] at least one real remote-safe handler is registered, or external worker remains deliberately disabled;
- [ ] `JobTypes()` derives capabilities;
- [ ] manual capability config can only narrow registered types;
- [ ] empty/malformed capability config fails closed;
- [ ] empty registry fails startup;
- [ ] broker rejects empty remote capabilities;
- [ ] unsupported jobs are not claimed;
- [ ] registry/runner tests pass with race detector;
- [ ] worker binary builds;
- [ ] full repository builds;
- [ ] CI is green;
- [ ] post-merge verification is run on `main`.

## Rollback

Safe rollback:

1. revert the W1 PR;
2. set Docker external worker to disabled;
3. keep server in explicit `--mode all` compatibility mode;
4. verify queue processing through the in-process runner.

Never roll back by restoring empty capabilities as wildcard behavior.
