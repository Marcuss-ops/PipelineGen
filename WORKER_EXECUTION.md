# PipelineGen Worker Execution Plan

> STATUS: ACTIVE
>
> OWNER: worker implementation agent
>
> START HERE. Do not modify code before reading this file and the linked documents in order.

## Mission

Make the external `cmd/worker` safe, functional, testable and horizontally scalable without breaking the in-process job runner.

The current repository has already wired `/internal/v1/workers/*` and re-enabled the Docker worker, but the external worker still creates an empty handler registry. With empty capabilities, the broker may allow it to claim any job and then fail it because no handler is registered.

The goal is not merely “worker registers”. The goal is:

```text
worker starts only with supported handlers
capabilities come from the same registry
unsupported jobs are never claimed
one lease has one valid executor
server and worker modes are separated deliberately
restart does not lose or duplicate work
```

## Mandatory execution order

1. [W1 — Remote worker registry and capabilities](docs/worker/W1_REMOTE_WORKER_REGISTRY.md)
2. [W2 — Server/worker mode cutover](docs/worker/W2_SERVER_WORKER_CUTOVER.md)
3. [W3 — Remote worker E2E certification](docs/worker/W3_REMOTE_WORKER_E2E.md)
4. [W4 — Migration 068 verification](docs/worker/W4_MIGRATION_068_VERIFICATION.md)
5. [Worker Definition of Done](docs/worker/WORKER_DEFINITION_OF_DONE.md)

W1 is the blocker. Do not enable a production external worker before W1 is complete.

W2 starts only after W1 passes.

W3 certifies the complete remote path.

W4 can be implemented in parallel only if it does not modify worker, broker or composition files.

## Current verified baseline

Current `main` baseline when this plan was written:

```text
b081216aacd191ff360383cdfa306f4b6471105a
```

Observed facts:

- `/internal/v1/workers/register` is now mounted before router setup;
- `docker-compose.yml` starts `pipelinegen-worker` with `restart: unless-stopped`;
- server still starts with `--mode all`;
- `cmd/worker/main.go` calls `worker.NewRegistry()` but registers no handlers;
- empty `VELOX_WORKER_CAPABILITIES` becomes an empty job-type list;
- the broker treats an empty job-type list as no filter;
- the remote runner dispatches through the empty registry;
- missing handler causes the claimed job to fail;
- migration 068 adds `width`, `height`, `group_name` to `media_assets`;
- CI status is not currently visible for the baseline commit.

Re-verify these facts before editing. If the code changed, update the relevant document first.

## Non-negotiable architecture rules

1. One canonical worker registry.
2. Registered handlers are the source of truth for advertised capabilities.
3. No separate manually maintained handler list and capability list.
4. Empty registry must fail startup.
5. Empty capabilities must never mean “claim every job” for a remote worker.
6. The server may run an in-process worker only in explicit compatibility mode.
7. Production Compose must not run both `server --mode all` and an external worker unintentionally.
8. Lease, revision and worker session checks remain mandatory.
9. No duplicate dispatcher, queue or job registry.
10. No direct push to `main`.
11. Every PR must be small, rebased and independently testable.
12. Do not hide failures by broadening allowlists or changing baselines.

## Git workflow

For each work item:

```bash
git fetch origin
git checkout main
git pull --ff-only origin main
git checkout -b codex/<work-item>
```

Before editing files already touched recently:

```bash
git fetch origin
git diff HEAD..origin/main -- <path>
```

Before push:

```bash
git fetch origin
git rebase origin/main
git status -sb
git diff origin/main...HEAD
go test <touched-packages>
go vet <touched-packages>
git log -n 5 --oneline
```

After push:

```bash
git log -n 5 --oneline
git status -sb
```

## PR boundaries

### PR-W1

```text
fix(worker): register supported remote handlers safely
```

Only registry, capability derivation, startup validation and focused tests.

### PR-W2

```text
fix(runtime): separate server and external worker modes
```

Only Docker/Compose/runtime mode changes and their tests.

### PR-W3

```text
test(worker): certify remote worker lifecycle end to end
```

Only test harness, fixtures, runbook and minimal bug fixes discovered by the E2E.

### PR-W4

```text
test(migrations): verify media asset column migration
```

Only migration verification and migration-runner hardening if proven necessary.

Do not combine W1–W4 into one large PR.

## Immediate safety rule

Until W1 is verified, use one of these safe states:

```text
A. server --mode all, external worker disabled
```

or

```text
B. external worker started only in an isolated test environment with no production jobs
```

Do not use:

```text
server --mode all + external worker with empty registry on the same production queue
```

## Required evidence in every PR

- base commit SHA;
- changed file list;
- exact commands run;
- test output summary;
- negative tests performed;
- runtime mode used;
- rollback command;
- known residual risks;
- CI link;
- post-merge verification on `main`.

## Stop conditions

Stop and open a focused issue instead of improvising if:

- the job type has no existing canonical handler;
- handler execution requires dependencies unavailable to the remote worker;
- a job depends on in-process state that cannot cross the API boundary;
- asset transfer cannot represent required inputs or outputs;
- migration 068 has already been applied inconsistently in real environments;
- server and remote worker use incompatible job stores;
- the required fix would introduce a second queue or duplicate registry.

## Final declaration

The external worker is not considered operational until every checkbox in `docs/worker/WORKER_DEFINITION_OF_DONE.md` is verified against the same commit and environment.
