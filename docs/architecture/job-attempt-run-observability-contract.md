# PipelineGen — Job–Attempt–Run observability contract

**Status:** architectural contract (Phase 0)
**Authority:** `internal/kernel/observability` for the observability model; SQLite remains the canonical business state store.
**Scope:** identity, lifecycle, timing semantics, ownership boundaries, and invariants required before wrapper or use-case instrumentation.

This document is the normative contract for the first observability migration. It deliberately describes the target semantics even where the current implementation still exposes compatibility fields. The current implementation and migration surface are recorded in [`architecture/observability-measurement-matrix.yaml`](../../architecture/observability-measurement-matrix.yaml).

## 1. Architectural decision

`internal/kernel/observability` is the single source of truth for observability semantics. It owns the shared vocabulary and report shape for:

- `Run`;
- `Attempt`;
- `Stage`;
- `Operation`;
- `Artifact`;
- `ChildRun`;
- `Counters`;
- `TimingReport`;
- `Recorder`;
- `Registry`.

Capability packages may provide adapters and concrete provider wrappers, but they must not introduce equivalent timing reports, provider timing interfaces, local stage-duration maps, or unbounded metric labels.

Compatibility is subordinate to the contract: the canonical report is the authoritative writer, while legacy surfaces are projections that may exist only until their explicit removal conditions in the measurement matrix pass. Two independent timers must never measure the same boundary.

## 2. Identity model

### 2.1 Entities

| Entity | Definition | Identity rule | Lifetime |
|---|---|---|---|
| **Job** | The immutable logical work request, represented by the durable job row. | `JobID` is the stable logical identity. It does not change across retries. | From enqueue through retention of the logical job record. |
| **Attempt** | One concrete execution of a job, including a retry, recovery execution, or re-claim that performs work. | `AttemptID` is a newly generated, durable, immutable identifier. It is never derived from `retry_count`, `job_results.attempt`, row count, `revision`, or `lease_id`. | From claim/start through terminal or abandoned outcome. |
| **Run** | The observability document associated with one attempt. | `RunID` is separate from `AttemptID`; the first version is one-to-one: one attempt has exactly one run. | From `RUNNING` creation through finalization and retention. |
| **Lease** | The temporary worker fence that currently owns an execution. | `LeaseID` identifies ownership/fencing only. It is not an attempt identity. | Until expiry, release, lease loss, or terminal job transition. |
| **Revision** | The compare-and-swap version of the job row. | `Revision` is a CAS fence/version, not an attempt number. | Incremented by job-row mutations according to the job store contract. |

The target relationships are:

```text
Job 1 ─── N Attempts
Attempt 1 ─── 1 Run
Run 1 ─── N Stages
Run 1 ─── N Operations
Run 1 ─── N Artifacts
Run 1 ─── N ChildRuns
```

`RunID` and `AttemptID` remain separate even though the initial schema enforces a one-to-one relationship. This leaves room to regenerate or reconstruct a report without changing execution identity.

### 2.2 Attempt creation

A new attempt is created atomically with the claim/execution transition, or by a dedicated attempt repository participating in the same transaction boundary. The creation operation must persist:

- `AttemptID`;
- `JobID`;
- `RunID`;
- attempt ordinal for display only, if needed;
- attempt availability timestamp;
- attempt start timestamp;
- worker and lease references;
- the job revision observed at claim;
- terminal outcome and error classification when finalized.

The attempt ordinal may be derived for presentation, but it must not be used as the durable identity.

### 2.3 Attempt lifecycle

The canonical lifecycle is:

```text
AVAILABLE
  → CLAIMED
  → RUNNING
  → SUCCEEDED
  → FAILED
  → CANCELLED
  → ABANDONED
```

Not every attempt visits every state. A retryable failure transitions the job to a retry wait and creates a later attempt; the backoff interval belongs to the job-level report, not to the queue wait of the later attempt.

`ABANDONED` is reserved for worker loss or lease expiry. It is not a synonym for application failure.

### 2.4 Run status

The canonical run status values are:

| Status | Meaning |
|---|---|
| `RUNNING` | The attempt is executing and its lease is still valid. |
| `SUCCEEDED` | The attempt completed the requested work successfully. |
| `FAILED` | The attempt ended with an application or provider error. |
| `CANCELLED` | The attempt was intentionally cancelled or stopped by policy. |
| `ABANDONED` | The attempt was recovered after worker/lease loss; `error_code=WORKER_LOST`. |

A run that is still `RUNNING` after its lease is no longer valid must be finalized as `ABANDONED`, not silently rewritten as `FAILED`.

## 3. Canonical run envelope

The kernel report must carry at least:

```text
RunID
JobID
JobType
AttemptID
ParentRunID (optional)
Status
CreatedAt
AttemptAvailableAt
AttemptStartedAt
AttemptFinishedAt
QueueWaitMs
WallTimeMs
RetryBackoffMs (job-level aggregate)
SemaphoreWaitMs
RateLimitWaitMs
ProviderWaitMs (operation detail and optional aggregate)
ActiveMs
BlockedMs
AccumulatedOperationMs
Stages
Operations
Artifacts
ChildRuns
Counters
ErrorCode
Error
ObservabilityDegraded
```

The current `RunReport` already provides much of the envelope. Missing or provisional fields must be added through the kernel contract rather than capability-local structures. In particular, the current runtime may populate `AttemptID` from `lease_id` and calculate `active_ms` by summing stages; those are documented compatibility divergences, not compliant target semantics.

### 3.1 Timestamp requirements

All timestamps are UTC, monotonic ordering is validated where the source clock permits, and persisted duration fields are non-negative integers in milliseconds. A timestamp may be absent only when its semantic event has not happened yet; it must not be replaced with a fabricated successful value.

Required event timestamps:

- `job_created_at`: logical job enqueue time;
- `attempt_available_at`: time the attempt became eligible to run;
- `attempt_started_at`: time the worker began the attempt after claim;
- `attempt_finished_at`: time the attempt reached a terminal or abandoned state;
- `retry_available_at`: time a retry becomes eligible after programmed backoff.

## 4. Duration semantics

### 4.1 Queue wait

For one attempt:

```text
queue_wait_ms = attempt_started_at - attempt_available_at
```

For the first attempt:

```text
attempt_available_at = job_created_at
```

For a retry:

```text
attempt_available_at = retry_available_at
```

The retry backoff must not be counted again in the later attempt's queue wait. If a claim is rejected or a worker loses its lease before work begins, the attempt still receives a terminal/abandoned outcome with the timestamps available at the time of recovery.

### 4.2 Wall time

For one attempt:

```text
wall_time_ms = attempt_finished_at - attempt_started_at
```

`wall_time_ms` is elapsed real time. It is never calculated by summing stages or operations.

For a job with multiple attempts, the report may expose:

- per-attempt wall time on each run;
- a job-level sum for diagnostic purposes;
- a job-level wall interval from logical job start to terminal completion.

The names must make the aggregation explicit; a sum of attempt walls must not be presented as one attempt's wall time.

### 4.3 Retry backoff

For a job:

```text
retry_backoff_ms = Σ(retry_available_at - previous_attempt_finished_at)
```

Only programmed time between an attempt's end and the next attempt becoming available belongs in this measure. Provider retries internal to one operation may increment operation retry counters and operation wait observations, but they do not create a new job attempt unless the job runtime actually releases and reclaims the job.

### 4.4 Operation time

```text
accumulated_operation_ms = Σ(all operation durations)
```

Operation duration is the elapsed time inside a concrete technical/provider boundary. It may include the provider request and adapter-local serialization, but it must not include unrelated orchestration time.

Parallel operations are summed for explanation, not for wall time. For example:

```text
4 parallel downloads × 10,000 ms
wall_time_ms                   ≈ 10,000
accumulated_operation_ms       ≈ 40,000
```

### 4.5 Provider wait

`provider_wait_ms` is the time spent inside an external provider call, such as:

```text
Drive, Qdrant, Ollama, TTS, Artlist, YouTube,
Google Docs, image providers, transcription providers
```

It is an operation detail. A report may expose a derived run/attempt aggregate only as a projection of operation intervals; that aggregate is never added to `accumulated_operation_ms`, `active_ms`, or `wall_time_ms`. If provider wait is a sub-interval of an operation, it must not be added to the operation duration as a second independent total.

### 4.6 Semaphore wait

`semaphore_wait_ms` is time spent waiting to acquire an internal concurrency slot, including configured limits for:

```text
TTS, Ollama, download, FFmpeg, Drive, image processing
```

The wait begins immediately before the acquire operation and ends when the slot is acquired or the attempt is cancelled. The actual work performed while holding the slot is not semaphore wait.

### 4.7 Rate-limit wait

`rate_limit_wait_ms` is time spent waiting for a token or the next permitted rate-limit window. It excludes the provider request itself. A rejected request with a retry hint records the actual wait only if the caller waits; a planned but skipped wait is not recorded as elapsed time.

### 4.8 Active time

`active_ms` is the duration of the union of intervals during which at least one stage or operation of the attempt was executable and active:

```text
active_ms = duration(union(active stage/operation intervals))
```

It is **not** the sum of stage durations. Stages may overlap, and an operation nested inside a stage does not create a second active interval for the same wall-clock work.

The existing kernel `activeMs` accumulator is therefore provisional compatibility behavior until interval collection is implemented.

### 4.9 Blocked time

`blocked_ms` is the duration of the union of intervals during which the attempt had no executable work because it was blocked by:

```text
semaphore
rate limiter
dependency child
resource lock
```

```text
blocked_ms = duration(union(blocked intervals))
```

If one operation waits while another operation is actively working, the run is not blocked for the overlapping portion. The collector must preserve interval start/end events and merge overlapping intervals; adding every wait duration is incorrect.

Retry backoff is a job-level wait and may be exposed separately. It must not be silently folded into attempt `blocked_ms` unless the report explicitly identifies the aggregation.

### 4.10 Stage and operation non-additivity

A stage describes application orchestration:

```text
acquire, process, persist, index, verify
```

An operation describes a nested technical boundary:

```text
youtube.download, ffmpeg.transcode, drive.upload,
sqlite.transaction, qdrant.upsert
```

Example:

```text
Stage: persist                       4,000 ms
├── Operation: drive.upload          2,500 ms
├── Operation: sqlite.transaction      300 ms
└── remaining application logic       1,200 ms
```

The total run uses wall-clock intervals. Operations explain stages; they are never added to stage totals to calculate run duration.

## 5. Classification rules

Every observation is classified by the semantic boundary that owns it:

| Question | Canonical type |
|---|---|
| What logical work was requested? | `Run` / `JobID` |
| Which concrete execution or retry performed it? | `Attempt` |
| Which application phase orchestrated it? | `Stage` |
| Which provider or technical boundary was called? | `Operation` |
| Which durable output was created or reused? | `Artifact` |
| How many items, bytes, cache events, retries, or failures occurred? | `Counter` |
| Is it only process/runtime health and not job work? | Infrastructure metric |
| Did a child job execute independently? | `ChildRun` linked to the parent run |

No new `map[string]int64`, local timing report, provider timing interface, or duration-specific JSON envelope is allowed when the observation fits one of these types.

## 6. Persistence and idempotency contract

The initial durable schema is:

```text
run_observability
run_stage_observations
run_operation_observations
run_artifact_observations
```

The artifact table may instead link to an existing canonical artifact/catalog table if it preserves the run relationship and observation identity.

The write sequence is:

```text
claim attempt
→ insert Run RUNNING
→ append stage/operation/artifact observations
→ finalize Run
```

Every observation receives a stable ID. Idempotent writes use the observation identity, never only semantic names:

```text
run_id
stage_observation_id
operation_observation_id
artifact_observation_id
```

The same stage or operation may occur more than once in one run, across retries, or in a batch. `stage + component + operation` is not a deduplication key.

Required write behavior:

```sql
INSERT ... ON CONFLICT DO UPDATE
```

A recorder failure:

- must not change the business result of a valid job;
- must emit a structured log with the run/attempt context redacted as required;
- must increment `observability_recorder_failures_total`;
- must set `observability_degraded=true` on the report when possible;
- must not masquerade as a successful persistence operation.

Crash recovery marks `RUNNING` runs whose lease is no longer valid and whose recovery threshold has elapsed as:

```text
status = ABANDONED
error_code = WORKER_LOST
```

## 7. Technical instrumentation boundaries

Technical timing is recorded only in shared wrappers/adapters, each called once per boundary:

```text
ObservedDrivePublisher
ObservedSQLiteExecutor
ObservedQdrantClient
ObservedProcessRunner
ObservedFFmpeg
ObservedDownloader
ObservedArtlistProvider
ObservedOllamaClient
ObservedTTSProvider
ObservedNLPProvider
ObservedTranscriptionProvider
ObservedImageProvider
ObservedGoogleDocsPublisher
```

Use cases open and close stages, but do not re-time operations already measured by an adapter. This guarantees one observation for every Drive upload regardless of whether the caller is stock, voiceover, VidRush, images, documents, YouTube, or Artlist.

## 8. Prometheus contract

Global labels allowed in the canonical family:

```text
job_type
stage
component
operation
status
cache_status
```

The following labels are forbidden:

```text
job_id, attempt_id, run_id, lease_id, URL, query, title,
clip_id, asset_id, segment_id, free-form language, filename, folder_id
```

`language` remains persisted report data in the initial version. It can become a label only after an explicit bounded-cardinality decision.

Canonical metric names:

```text
pipelinegen_runs_total
pipelinegen_run_duration_seconds
pipelinegen_queue_wait_seconds
pipelinegen_stage_duration_seconds
pipelinegen_operations_total
pipelinegen_operation_duration_seconds
pipelinegen_operation_failures_total
pipelinegen_operation_retries_total
pipelinegen_wait_duration_seconds
pipelinegen_artifacts_total
pipelinegen_bytes_processed_total
pipelinegen_cache_events_total
pipelinegen_observability_recorder_failures_total
```

Existing capability metrics may remain during migration only as projections of the same canonical observer. They may not introduce a second timer for the same boundary.

## 9. Compatibility and removal gates

The following compatibility sequence is normative: the kernel contract and `RunReport` are authoritative; the recorder writes the canonical report; `processmetrics`, `StageDurations`, and `VidRushTimingMetrics` are adapters/projections; consumers may use a dual-read comparison; legacy writers and types remain only until their explicit removal conditions pass.

Legacy parity is accepted only when, for a representative sample of runs:

```text
abs(legacy_duration - canonical_duration)
≤ max(legacy_duration × 5%, 50 ms)
```

The following conditions must be true before removing a legacy surface:

- all consumers read the canonical report;
- the canonical wrapper observes the boundary exactly once;
- parity passes for success, failure, retry, cancellation, and parallel fan-out;
- the legacy writer is disabled and no longer receives production writes;
- dashboards and API responses use the canonical projection;
- the relevant removal condition is recorded in the measurement matrix;
- no duplicate timing path remains.

## 10. Initial acceptance gates

The architecture work is complete only when the implementation can demonstrate:

```text
EVERY_JOB_HAS_RUN=PASS
EVERY_ATTEMPT_HAS_ATTEMPT_ID=PASS
RUN_ATTEMPT_ONE_TO_ONE=PASS

QUEUE_WAIT_PERSISTED=PASS
WALL_TIME_PERSISTED=PASS
RETRY_BACKOFF_PERSISTED=PASS
SEMAPHORE_WAIT_PERSISTED=PASS
RATE_LIMIT_WAIT_PERSISTED=PASS

FAILED_RUNS_PERSISTED=PASS
CANCELLED_RUNS_PERSISTED=PASS
ABANDONED_RUNS_RECOVERED=PASS

CHILD_RUNS_LINKED=PASS
ARTIFACTS_LINKED=PASS
COUNTERS_PERSISTED=PASS

DRIVE_OBSERVED=PASS
SQLITE_OBSERVED=PASS
QDRANT_OBSERVED=PASS
FFMPEG_OBSERVED=PASS
DOWNLOADERS_OBSERVED=PASS
OLLAMA_OBSERVED=PASS
TTS_OBSERVED=PASS
NLP_OBSERVED=PASS
IMAGE_PROVIDERS_OBSERVED=PASS

PROMETHEUS_LOW_CARDINALITY=PASS
REPORT_QUERYABLE_BY_JOB=PASS
REPORT_QUERYABLE_BY_ATTEMPT=PASS

LEGACY_PARITY=PASS
LEGACY_WRITERS_DISABLED=PASS
STAGE_DURATIONS_REMOVED=PASS
VIDRUSH_TIMING_REMOVED=PASS
PROCESSMETRICS_TIMING_REMOVED=PASS
NO_DUPLICATE_TIMING_PATHS=PASS
```

These gates are requirements for later implementation and verification; this artifact does not claim that they pass today.
