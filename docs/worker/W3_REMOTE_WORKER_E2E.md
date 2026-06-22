# W3 — Remote worker end-to-end certification

> PRIORITY: P1
>
> STATUS: pending
>
> PREREQUISITES: W1 and W2 verified on `main`

## Objective

Prove that the external worker can safely execute real jobs through the complete remote path:

```text
API enqueue
→ persistent job store
→ worker register
→ heartbeat
→ capability-filtered claim
→ asset staging
→ handler execution
→ progress
→ output upload
→ complete/fail acknowledgement
→ final API-visible state
```

This document certifies behavior. It is not permission to add broad features.

## Required branch

```text
codex/remote-worker-e2e-certification
```

## Allowed scope

```text
internal/application/jobs/worker/**
internal/infrastructure/remote/**
internal/infrastructure/jobs/local/**
internal/api/workers/**
internal/api/jobs/**
cmd/worker/**
scripts/worker-e2e/**
testdata/worker/**
docs/worker/**
docs/certification/**
docker-compose.yml
```

Production code changes are allowed only for defects proven by the E2E.

## Out of scope

- unrelated refactors;
- new providers;
- database replacement;
- new UI;
- new job types unless required as a dedicated test fixture and isolated from production registries;
- broad API redesign.

## Certification identity

Record before running:

```text
commit SHA:
image digests:
server mode:
worker image:
worker registered job types:
database path/version:
Qdrant version:
FFmpeg version:
yt-dlp version:
CPU/RAM:
start timestamp:
```

All results must refer to one exact commit.

## Phase 0 — Build a deterministic test harness

Create scripts under:

```text
scripts/worker-e2e/
```

Minimum scripts:

```text
00_clean.sh
01_build.sh
02_start_server.sh
03_start_worker.sh
04_enqueue.sh
05_assert_success.sh
06_assert_failure.sh
07_kill_worker.sh
08_restart_worker.sh
09_assert_no_duplicates.sh
10_cleanup.sh
```

Requirements:

- `set -euo pipefail`;
- no secret literals;
- environment validation;
- deterministic temp directory;
- cleanup trap;
- JSON parsing via `jq` where needed;
- non-zero exit on failed assertion;
- output includes commit SHA and scenario name.

## Phase 1 — Unit and contract baseline

Run first:

```bash
go test ./internal/application/jobs/worker/...
go test ./internal/infrastructure/remote/...
go test ./internal/infrastructure/jobs/local/...
go test ./internal/api/workers/...
go test -race ./internal/application/jobs/worker/...
go build ./cmd/server
go build ./cmd/worker
```

Contract tests must cover:

- register request/response;
- heartbeat request;
- claim long-poll behavior;
- progress;
- complete;
- fail;
- asset download;
- output upload;
- authentication failures;
- malformed JSON;
- timeout/cancellation;
- session expiry;
- lease mismatch;
- revision mismatch.

## Phase 2 — Clean Compose topology

Start only the separated topology:

```bash
docker compose down -v --remove-orphans
docker compose build --no-cache
docker compose up -d qdrant artlist-scraper pipelinegen-server
docker compose ps
```

Assert:

- server healthy;
- server ready;
- server mode is not `all`;
- local in-process runner disabled;
- worker routes mounted;
- database migrations complete.

Then start worker:

```bash
docker compose up -d pipelinegen-worker
```

Assert:

- preflight succeeds;
- registration returns success;
- handler list non-empty;
- advertised capabilities equal registered types;
- heartbeat succeeds twice;
- no claim error loop;
- workspace is writable;
- required binaries exist.

## Phase 3 — Happy-path job

Select the smallest real remote-safe job from W1.

Test data must be deterministic and not depend on production accounts.

Steps:

1. enqueue through the normal API/service path;
2. capture job ID;
3. verify queued state;
4. verify external worker claims it;
5. verify worker ID/session/lease recorded;
6. verify at least one progress update;
7. verify handler output;
8. verify output files uploaded/stored;
9. verify completed terminal state;
10. verify local runner never claimed it.

Assertions:

```text
claim count = 1
terminal completion count = 1
worker ID = external worker
job revision increases as expected
result JSON decodes
output exists
workspace cleaned after acknowledgement
```

## Phase 4 — Capability isolation

Prepare at least two job types:

```text
supported-by-worker-A
unsupported-by-worker-A
```

Tests:

- [ ] worker A claims supported job;
- [ ] worker A never claims unsupported job;
- [ ] unsupported job remains queued;
- [ ] compatible worker B can later claim it;
- [ ] empty capability worker cannot register/claim;
- [ ] unknown capability worker cannot start;
- [ ] configured subset is enforced exactly.

Observe database/broker calls, not only logs.

## Phase 5 — Duplicate prevention

Scenarios:

### Two workers, same capability

Start two workers with the same supported type.

Assert:

- only one receives the lease;
- only one executes handler;
- only one uploads terminal outputs;
- only one completes;
- the loser continues polling safely.

### Duplicate enqueue with same idempotency key

Assert:

- one canonical job or one canonical output according to existing semantics;
- no duplicate Drive/Qdrant/file output;
- retries do not multiply side effects.

### Late completion after lease loss

Simulate worker A losing lease and worker B recovering job.

Assert:

- A’s late complete/fail is rejected;
- B is the only valid terminal writer;
- revision/lease fencing works.

## Phase 6 — Worker crash recovery

Test at these points:

1. after registration, before claim;
2. after claim, before handler;
3. during handler;
4. after output creation, before upload;
5. after upload, before complete;
6. during complete request.

For each scenario record:

```text
job state after crash
lease expiry time
recovery worker
retry count
output duplicates
temp files
final terminal state
```

Exit conditions:

- no lost job;
- no double completion;
- no stale worker allowed to commit;
- recovery occurs within defined lease/retry window;
- orphan files are cleaned or tracked.

## Phase 7 — Server restart recovery

Steps:

1. start server and worker;
2. register and heartbeat;
3. restart server while worker idle;
4. verify worker reconnect behavior;
5. restart server while job active;
6. verify broker calls recover or fail explicitly;
7. verify worker session renewal/re-registration policy.

Required decision:

```text
Does server restart preserve worker session, or must worker re-register?
```

Document and test one canonical behavior.

## Phase 8 — Heartbeat and session expiry

Tests:

- heartbeat success;
- temporary heartbeat failure;
- heartbeat failure longer than session TTL;
- claim with expired session;
- progress with expired session;
- complete with expired session;
- worker re-registration;
- stale session cannot mutate job.

Assert:

```text
expired session → no claim/progress/complete/fail
```

## Phase 9 — Lease renewal

Use a handler longer than the lease renewal threshold.

Assert:

- renew occurs before expiry;
- expiry extends;
- expected revision semantics remain valid;
- cancellation stops renewal;
- failed renewal prevents unsafe terminal writes;
- another worker cannot claim while lease is valid;
- recovery occurs after lease truly expires.

If the current runner has no renewal loop, W3 cannot pass for long jobs. Implement a focused lease-renewal fix with tests or mark the external worker limited to jobs shorter than the lease TTL and keep production disabled for long jobs.

## Phase 10 — Asset transfer

Input staging tests:

- valid input asset;
- missing input asset;
- corrupted input;
- duplicate input IDs;
- interrupted download;
- path traversal payload;
- oversized asset.

Output upload tests:

- `output_path`;
- `pdf_path`;
- `markdown_path`;
- `output_files` string list;
- `output_files` object list;
- duplicate output path;
- missing output file;
- interrupted upload;
- retry after upload before complete.

Assert:

- paths stay inside workspace/allowed roots;
- uploads are idempotent;
- missing optional output is handled explicitly;
- missing required output fails job;
- no arbitrary host file can be uploaded.

## Phase 11 — Failure classification

Create a table:

| Failure | Retryable | Consumes retry | Terminal state | Alert |
|---|---:|---:|---|---|
| invalid payload | no | no/once | failed | low |
| unsupported type | no claim | no | queued | high config alert |
| provider timeout | yes | yes | retry/failed | medium |
| lease lost | no local retry | no | recovered elsewhere | high |
| output upload timeout | yes | yes | retry | medium |
| auth failure | no | no | worker degraded | high |

Tests must prove classifications match actual broker state.

## Phase 12 — Drain and shutdown

Test:

```bash
docker stop --time 30 pipelinegen-worker
```

Idle worker:

- stops claim loop;
- exits cleanly;
- no session mutation after exit.

Busy worker:

- stops new claims;
- completes within grace or cancels safely;
- does not upload/complete after losing lease;
- leaves recoverable job state.

## Phase 13 — Security

Tests:

- missing token;
- invalid token;
- token for wrong environment;
- replayed request if signatures/nonces exist;
- malformed worker ID;
- oversized JSON;
- path traversal in assets;
- worker attempting unsupported route;
- logs checked for token/session leakage.

## Phase 14 — Observability assertions

Verify metrics/logs expose:

- registered worker count;
- heartbeat age;
- current capabilities;
- claim count;
- running jobs;
- completions/failures;
- lease lost;
- job duration;
- runtime mode;
- local runner disabled.

No high-cardinality labels.

## Phase 15 — Soak test

Minimum initial soak:

```text
4 hours for W3 merge gate
24 hours before production declaration
```

Workload:

- repeated supported jobs;
- at least two workers;
- scheduled worker restart;
- server restart;
- one temporary provider failure;
- one asset transfer retry.

Exit:

- zero lost jobs;
- zero duplicate terminal completion;
- no memory growth without explanation;
- no persistent temp growth;
- no zombie workers;
- heartbeat stable;
- backlog returns to zero.

## Required report

Create:

```text
docs/certification/<version>/REMOTE_WORKER_E2E.md
```

Include:

- commit SHA;
- environment;
- registered job types;
- commands;
- scenario table;
- pass/fail;
- log/artifact links;
- known limits;
- go/no-go.

## Required commands

```bash
go test ./internal/application/jobs/worker/...
go test ./internal/infrastructure/remote/...
go test ./internal/infrastructure/jobs/local/...
go test ./internal/api/workers/...
go test -race ./internal/application/jobs/worker/...
go build ./cmd/server
go build ./cmd/worker
docker compose config
docker compose build --no-cache
bash scripts/worker-e2e/00_clean.sh
bash scripts/worker-e2e/01_build.sh
# continue in numeric order
```

## Exit gate

W3 is complete only when:

- [ ] complete remote happy path passes;
- [ ] capability isolation passes;
- [ ] two-worker duplicate prevention passes;
- [ ] lease-loss fencing passes;
- [ ] worker crash recovery passes;
- [ ] server restart recovery passes;
- [ ] session expiry passes;
- [ ] long-job renewal passes or long jobs remain explicitly unsupported;
- [ ] asset transfer security/idempotency passes;
- [ ] graceful drain passes;
- [ ] 4-hour soak passes;
- [ ] certification report committed;
- [ ] CI is green;
- [ ] post-merge verification runs on `main`.

## Rollback

If W3 exposes data loss, duplicate completion, invalid fencing or unsafe uploads:

1. disable external worker;
2. restore server compatibility `--mode all` deployment;
3. preserve failed test database/logs as artifacts;
4. open a focused bug PR;
5. rerun W3 from a clean environment after merge.
