# W2 — Server/worker mode cutover

> PRIORITY: P1
>
> STATUS: pending
>
> PREREQUISITE: W1 verified on `main`

## Objective

Separate API/server responsibilities from external worker responsibilities without running two unintended executors against the same queue.

Final production topology:

```text
pipelinegen-server  → HTTP API, internal worker broker routes, scheduler/maintenance only when explicitly assigned
pipelinegen-worker  → remote job executor with registered handlers and explicit capabilities
```

The server must not run the in-process job runner in the same production Compose profile unless compatibility mode is deliberately selected.

## Current risk

Current Compose starts:

```text
pipelinegen-server --mode all
pipelinegen-worker
```

`--mode all` starts the in-process job runner. Once the external worker is enabled, both executors compete for the same queue. Leasing should prevent simultaneous ownership, but this topology is ambiguous and makes failures, load and capacity impossible to attribute.

## Required branch

```text
codex/server-worker-mode-cutover
```

## Allowed scope

```text
docker-compose.yml
Dockerfile
cmd/server/**
cmd/worker/**
internal/app/bootstrap.go
internal/app/lifecycle.go
internal/api/common/health.go
internal/api/routes.go
internal/infrastructure/config/**
config*.yaml
Makefile
scripts/smoke/**
docs/worker/W2_SERVER_WORKER_CUTOVER.md
```

## Out of scope

- new job handlers;
- job schema changes;
- queue replacement;
- PostgreSQL migration;
- unrelated API changes;
- YouTube or Artlist business logic;
- broad Docker optimization.

## Phase 0 — Verify W1

Before changing runtime modes:

```bash
go test ./internal/application/jobs/worker/...
go test ./internal/infrastructure/jobs/local/...
go build ./cmd/worker
```

Run a focused registry check and record:

```text
registered job types:
advertised job types:
worker startup with empty registry:
worker startup with invalid configured type:
```

Stop if W1 is not verified.

## Phase 1 — Define canonical modes

The code currently recognizes modes including `all`, `worker`, `scheduler` and `maintenance`.

Document and enforce one canonical production mapping:

| Binary | Mode | Responsibilities |
|---|---|---|
| server | `server` or `http` | HTTP, internal broker routes, no local job claim loop |
| worker | worker binary | remote register/heartbeat/claim/execute |
| optional scheduler | `scheduler` | enqueue scheduled work only |
| optional maintenance | `maintenance` | enqueue/perform maintenance according to ownership |
| compatibility | `all` | single-process development only |

If `server` mode is not currently recognized by `startBackgroundJobs`, add explicit semantics instead of relying on a mode that accidentally disables everything.

Required rule:

```text
unknown mode → startup error
```

Do not silently treat an unknown mode as a partial configuration.

Tests:

- [ ] server mode does not start local runner;
- [ ] worker mode starts only intended local components when used internally;
- [ ] scheduler mode does not claim jobs;
- [ ] maintenance mode does not start HTTP unless explicitly designed;
- [ ] all mode preserves development compatibility;
- [ ] invalid mode fails startup.

## Phase 2 — Move production Compose to explicit topology

Target:

```yaml
pipelinegen-server:
  command: ["--mode", "server", "--config", "/etc/pipelinegen/config.yaml"]

pipelinegen-worker:
  command: ["--config", "/etc/pipelinegen/config.yaml"]
  restart: unless-stopped
```

Requirements:

- [ ] server does not claim jobs;
- [ ] worker registers before claim loop;
- [ ] worker uses the same canonical job store through broker API;
- [ ] worker token is provided through secret/env, not literal YAML;
- [ ] server and worker share only the volumes they actually need;
- [ ] worker gets FFmpeg/yt-dlp/Python;
- [ ] server remains lightweight;
- [ ] dependencies use readiness, not mere container start;
- [ ] no production default uses `latest` without a deployment override/version strategy.

## Phase 3 — Scheduler and maintenance ownership

`--mode all` currently starts scheduler and maintenance alongside the local runner.

Choose and document one owner for each:

```text
channel monitor
Drive sync scheduler
maintenance enqueue loop
backup enqueue loop
cache prewarm
job scanner/zombie recovery
metrics refresher
```

For every background component answer:

| Component | Owner process | Singleton required | Leader election/lock | Restart behavior |
|---|---|---:|---|---|

Rules:

- singleton tasks must not start in every server replica;
- a worker replica must not start scheduler tasks unless assigned;
- if leader election does not exist, deploy exactly one scheduler process;
- ownership must be visible in logs and metrics;
- process mode must control startup deterministically.

## Phase 4 — Readiness by role

Server readiness must verify:

- configuration valid;
- database reachable;
- migrations applied;
- worker broker routes mounted;
- job enqueue/store available.

Worker readiness/status must verify or expose:

- master reachable;
- registration active;
- last heartbeat result;
- non-empty handler registry;
- advertised capabilities;
- workspace writable;
- required binaries available;
- current lease state.

The worker may expose an internal probe endpoint or a command/status file, but do not add an unauthenticated public API casually.

At minimum, logs and metrics must distinguish:

```text
registered
claiming
running job
idle
heartbeat degraded
draining
stopped
```

## Phase 5 — Graceful shutdown and drain

Server:

- stop accepting new HTTP requests;
- allow in-flight requests to finish;
- stop scheduler loops;
- do not corrupt broker state.

Worker:

- stop new claims;
- renew or finish current lease during grace window;
- if grace expires, cancel handler and return/release/fail according to defined semantics;
- upload/commit outputs only under a valid lease;
- close heartbeat/session cleanly if API supports it;
- cleanup workspace after terminal acknowledgement.

Tests:

- [ ] SIGTERM idle worker exits cleanly;
- [ ] SIGTERM active worker drains;
- [ ] forced kill allows lease recovery;
- [ ] server restart does not terminate remote worker process;
- [ ] worker reconnects/registers after server restart;
- [ ] no double terminal completion.

## Phase 6 — Authentication and secrets

Verify internal worker routes use a worker-specific credential policy.

Requirements:

- [ ] `VELOX_WORKER_TOKEN` required in non-development environments;
- [ ] no token committed in config;
- [ ] token redacted from logs;
- [ ] registration, claim, heartbeat, progress, complete, fail and asset transfer share consistent auth;
- [ ] token rotation procedure documented;
- [ ] invalid token test;
- [ ] missing token test;
- [ ] expired/revoked session test.

If the current generic API auth is used, document why it is sufficient and how scopes are restricted.

## Phase 7 — Observability

Add or verify metrics:

```text
pipelinegen_remote_workers_registered
pipelinegen_remote_worker_heartbeat_age_seconds
pipelinegen_remote_worker_claim_total
pipelinegen_remote_worker_jobs_running
pipelinegen_remote_worker_job_duration_seconds
pipelinegen_remote_worker_failures_total
pipelinegen_remote_worker_lease_lost_total
pipelinegen_local_runner_enabled
pipelinegen_runtime_mode_info
```

No worker ID or job ID as unbounded Prometheus labels.

Logs must include structured fields:

```text
worker_id
session_id (safe form)
job_type
job_id
lease_id (safe form)
runtime_mode
```

## Phase 8 — Compose smoke test

Clean test:

```bash
docker compose down -v --remove-orphans
docker compose build --no-cache
docker compose up -d qdrant artlist-scraper pipelinegen-server
docker compose ps
curl -fsS http://127.0.0.1:8081/health
curl -fsS http://127.0.0.1:8081/ready
```

Verify server has no local claim loop by logs/metrics.

Then:

```bash
docker compose up -d pipelinegen-worker
docker compose logs --no-color pipelinegen-worker
```

Required observations:

- worker preflight passes;
- worker registers once;
- non-empty capabilities logged;
- heartbeat succeeds;
- no unsupported job claimed;
- server still does not start local runner.

## Phase 9 — Compatibility profile

Keep single-process development only if useful, but make it explicit.

Preferred options:

```text
docker compose --profile compatibility up
```

or a separate file:

```text
docker-compose.compat.yml
```

Rules:

- production default is separated;
- compatibility mode is documented as non-scaled;
- compatibility mode does not start external worker;
- tests cover both topologies where practical.

## Required commands

```bash
gofmt -w cmd/server cmd/worker internal/app internal/api/common
go test ./internal/app/...
go test ./internal/api/...
go test ./cmd/worker/...
go vet ./internal/app/... ./internal/api/... ./cmd/worker/...
go build ./cmd/server
go build ./cmd/worker
go build ./...
docker compose config
docker compose build
```

## Exit gate

W2 is complete only when:

- [ ] production server mode does not run local job runner;
- [ ] external worker has real handlers and non-empty capabilities;
- [ ] scheduler/maintenance ownership is explicit;
- [ ] compatibility `all` mode is isolated from production default;
- [ ] graceful shutdown/drain is tested;
- [ ] worker auth is enforced;
- [ ] Compose smoke passes;
- [ ] server and worker images build;
- [ ] server/worker logs prove separated ownership;
- [ ] CI is green;
- [ ] post-merge verification runs on `main`.

## Rollback

Safe rollback:

```text
disable external worker
restore explicit compatibility deployment with server --mode all
verify local runner processes queue
```

Do not run compatibility and external worker together during rollback.
