# RenderingGen Queue Service

Central pull-based job queue for RenderingGen. Producers (PipelineGen) submit
jobs; GPU workers **pull** them (`claim → render → complete/fail`) and hold a
renewable lease while rendering. Binary artifacts live in the object store;
the queue stores only metadata, state, history and metrics.

The wire contract is implemented in `internal/server` and mirrored 1:1 by the
public client package (`client/`), which producers and workers import so they
never reimplement the HTTP format.

## Conventions

- All request and response bodies are JSON (`Content-Type: application/json`).
- `204 No Content` means success with an empty body.
- `409 Conflict` signals a state/precondition failure (duplicate job, wrong
  worker, job not running).
- The `lease` value is a `time.Duration` serialized as an integer number of
  **nanoseconds** (e.g. `30000000000` = 30s).
- Timestamps are RFC 3339 strings.

## Endpoints

### Producer (submit and poll)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/jobs` | Submit a render job |
| `GET` | `/jobs/{id}` | Current state + certified artifact |
| `GET` | `/jobs/{id}/wait` | Long-poll until the job is terminal (producer) |
| `GET` | `/jobs/depth` | Queue depth snapshot (autoscaling) |
| `POST` | `/jobs/{id}/cancel` | Producer cancellation (terminal; never re-claimed) |

#### `POST /jobs`

A job is **one render segment**, not one overlay: `render_plan` carries every
element of the segment (base video, background, phrases, keywords, images,
animations) as the **semantic `renderinggen.overlay-plan.v1` document** emitted
by PipelineGen. RenderingGen compiles it exclusively into
`chronon.render-plan.v2` before execution, so Chronon3d composes the segment in
a single pass. A concrete Chronon plan is **not** accepted on this boundary.
The envelope schema is `contracts/renderinggen.job.v1.schema.json`.

Request (only `id`, `schema`, `version`, `render_plan` and `assets` are used):

```json
{
  "id": "video-983",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": {
    "schema_version": "renderinggen.overlay-plan.v1",
    "plan_id": "video-983",
    "video_id": "video-983",
    "width": 1920,
    "height": 1080,
    "fps_num": 30,
    "fps_den": 1,
    "source": { "asset_id": "base", "sha256": "<sha256>" },
    "items": [
      { "id": "phrase-1", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": "caption_card", "text": "QUESTO CAMBIA TUTTO", "start_ms": 2000, "end_ms": 3833 }
    ]
  },
  "assets": [ { "hash": "<sha256>", "logical_path": "assets/semantic/base.mp4" } ]
}
```

- `id` is optional; when omitted the server assigns one.
- Response `201 Created`: `{"id":"job-123"}`.
- Response `409 Conflict`: a job with that `id` already exists. Producers treat
  this as idempotent success and poll the existing job.
- `not_before` (RFC 3339) defers the job: it is stored immediately but **no
  worker can claim it until the time is due**. Omitted or a past time keeps the
  historical immediately-claimable behaviour, so existing producers are
  unaffected. The deferral is enforced by the claim query (and by the in-memory
  backend identically), so a worker cannot bypass it by ignoring the field —
  this is how a producer pre-loads a queue of thousands of jobs without
  overrunning the GPU workers.

#### `POST /jobs/{id}/cancel`

Request body is ignored. Response `204 No Content`. Cancelling an
already-cancelled job is a no-op (idempotent, `204`); cancelling a
`completed`/`failed` job returns `409 Conflict`; an unknown job returns
`404`. Cancellation is **terminal**: a cancelled job is never claimable again
and lease expiry never requeues it, so a cancelled job can never trigger a new
Chronon render across lease/claim cycles. An in-flight attempt (already
claimed when the cancel landed) is closed as `cancelled` in the attempt
history — its later `complete`/`fail` reports are rejected because the job is
no longer running. Cancellation never increments `attempts`: every claim that
would have invoked Chronon already happened before the cancel.

#### `GET /jobs/{id}`

Response `200 OK` — the full job, including `state`, `attempts`, timestamps and
(once completed) the `artifact`. Response `404 Not Found` when unknown.

#### `GET /jobs/{id}/wait?max_wait_ms=N`

Event-driven completion: identical body to `GET /jobs/{id}`, but the request
parks until the job reaches a terminal state (`completed`, `failed`,
`cancelled`) or the bounded wait elapses, whichever comes first. `max_wait_ms`
defaults to 25s and is clamped to the same 25s ceiling. Response `404 Not Found`
when the job is unknown.

The handler is a pure read — it never mutates state and never assigns work —
and it blocks on the service's single wake-up primitive: `Submit`, `Complete`,
`Fail`, `Rendered`, `Cancel`, `Retry`, `RequeueExpired` and the PostgreSQL
`LISTEN` bridge all broadcast, and every waiter re-reads `render_jobs` after
waking. On another replica (whose local broadcast is not shared) the service's
bounded 250ms→2s re-poll bounds the observed latency, so signal loss can never
stall a producer.

`rendered` is deliberately NOT terminal: the artifact is durable but external
publication is still pending and the job remains re-claimable for a
publication-only retry.

#### `GET /jobs/depth`

Response `200 OK`:

```json
{ "pending": 3, "running": 1, "completed": 5, "failed": 0, "depth": 4 }
```

### Worker (pull and report)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/jobs/claim` | Atomically claim the next pending job |
| `POST` | `/jobs/{id}/complete` | Report successful render + artifact |
| `POST` | `/jobs/{id}/fail` | Report failure (requeue or fail permanently) |
| `POST` | `/jobs/{id}/renew` | Extend the lease during a long render |
| `POST` | `/jobs/{id}/progress` | Report render progress (frames done/total) |

#### `POST /jobs/claim`

Request: `{"worker":"renderinggen-77"}`.

Response `200 OK`:

```json
{
  "id": "video-983",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": { "schema_version": "renderinggen.overlay-plan.v1", "plan_id": "video-983", "video_id": "video-983", "width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1, "source": { "asset_id": "base", "sha256": "<sha256>" } },
  "assets": [ { "hash": "<sha256>", "logical_path": "videos/base.mp4" } ],
  "lease": 30000000000
}
```

Response `204 No Content` when the queue is empty. The claim is atomic
(`FOR UPDATE SKIP LOCKED` on PostgreSQL) so concurrent workers never receive
the same job.

#### `POST /jobs/{id}/complete`

Request: `{"worker":"renderinggen-77","data":{ ...artifact... }}` — `data` is
the certified [Artifact](#artifact-copy-only-certification). Response
`204 No Content`, or `409 Conflict` if the job is not running or is owned by
another worker.

#### `POST /jobs/{id}/fail`

Request: `{"worker":"renderinggen-77","data":{"reason":"CUDA error"}}`.
Response `204 No Content`. Jobs that have not exhausted `max_attempts` are
requeued; otherwise they fail permanently.

#### `POST /jobs/{id}/renew`

Request: `{"worker":"renderinggen-77"}`. Response `204 No Content`, or
`409 Conflict` if the lease already expired and the job was requeued.

#### `POST /jobs/{id}/progress`

Request: `{"worker":"renderinggen-77","data":{"frames_done":485,"frames_total":1800}}`.
Response `204 No Content`, `409 Conflict` when the job is not running or is
owned by another worker, `404 Not Found` for unknown jobs. The queue stores
the snapshot (plus its own `last_frame_at` timestamp) so `GET /jobs/{id}`
exposes live render position without asking the worker; a report from a
worker that no longer owns the lease is rejected, never applied.

### Worker registry

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/workers/register` | Upsert a worker's identity + first heartbeat |
| `POST` | `/workers/heartbeat` | Record a heartbeat (liveness) |
| `GET` | `/workers` | List registered workers |
| `GET` | `/workers/health` | Aggregate ready/busy/offline snapshot |

#### `POST /workers/register`

Request is a [Worker](#worker). Response `204 No Content`.

#### `POST /workers/heartbeat`

Request: `{"worker":"renderinggen-77"}`. Response `204 No Content`, or
`404 Not Found` when the worker is not registered. Each heartbeat is appended
to an append-only `worker_heartbeats` ledger while `last_heartbeat_at` holds the
current liveness.

#### `GET /workers/health`

Response `200 OK`: `{"ready":2,"busy":1,"offline":0,"total":3}`.

### Ops

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | Liveness — `{"status":"ok"}` |
| `GET` | `/metrics` | Prometheus exposition |

Prometheus metrics: `renderinggen_jobs_pending` (gauge),
`renderinggen_render_duration_seconds` (histogram),
`renderinggen_queue_wait_seconds` (histogram),
`renderinggen_lease_expired_total` (counter),
`renderinggen_workers_ready` (gauge),
`renderinggen_workers_degraded` (gauge),
`renderinggen_workers_stale` (gauge),
`renderinggen_workers_offline` (gauge).

### Worker liveness

`GET /workers` annotates every worker with a DERIVED `liveness`
(`ready` | `degraded` | `stale` | `dead`) computed from its heartbeat age, and
`GET /workers/health` reports the same bands (`ready`, `busy`, `degraded`,
`stale`, `offline`, `total`). Both come from one function
(`internal/model/liveness.go`), so they cannot disagree.

`status` is NOT a liveness signal: it is the value the worker last REPORTED, and
a worker that stops (SIGSTOP, a wedged GPU driver, a frozen VM) keeps it forever.
Live observation on 2026-09-17: the only worker had all sixteen threads stopped
with a heartbeat hours old, while `/workers` still answered `status: ready` and
`/workers/health` answered `ready: 1` — a benchmark or a certification preflight
planned against a worker that could not claim a single job.

The bands are fractions of ONE configured number, `-worker-stale-after` (the
shipped unit runs `3m`; the internal default is `90s`) against the worker's 20s
heartbeat: beyond one third (60s) → `degraded`, beyond two thirds (120s) →
`stale`, beyond the window (or never heartbeated) → `dead`, which is what the
`offline` count has always meant. A window that is never configured classifies
every worker as `dead` rather than as unrestricted. A preflight must require at
least one worker with `liveness == "ready"`, not merely a registered row —
measured on 2026-09-17, the live registry held 17 rows all reporting
`status: ready`, of which exactly one was alive (the other sixteen had
heartbeats between 6 hours and 14 days old).

## Wire types

### Job

```json
{
  "id": "video-983",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": {},
  "assets": [ { "hash": "", "logical_path": "" } ],
  "state": "running",
  "worker": "renderinggen-77",
  "attempts": 2,
  "created_at": "2026-08-14T10:00:00Z",
  "queued_at": "2026-08-14T10:00:00Z",
  "started_at": "2026-08-14T10:00:05Z",
  "completed_at": "0001-01-01T00:00:00Z",	"lease_until": "2026-08-14T10:00:35Z",
	"fail_reason": "",
	"artifact": { },
	"progress": {
		"frames_done": 485,
		"frames_total": 1800,
		"last_frame_at": "2026-08-14T10:00:30.123456789Z",
		"worker": "renderinggen-77"
	}
}
```

`progress` is the last render progress the lease-owning worker reported via
`POST /jobs/{id}/progress` (absent until the first report). `frames_done` is
the last absolute frame position the renderer reported — use `last_frame_at`
age as the render liveness signal, not GPU memory.

`state` is one of `pending`, `running`, `finalizing`, `completed`, `failed`,
`rendered`, `cancelled` — the canonical vocabulary is `client.AllStates()`
(the internal `model.State` aliases that type). The `artifact` field is
populated only once the job completes.

### Artifact (copy-only certification)

`artifact` carries the copy-only certification of the rendered segment: the
facts a downstream assembler needs in order to concatenate it without decoding
and re-encoding it.

```json
{
  "id": "art-1",
  "kind": "segment",
  "storage_key": "overlay/2026/08/job-123/overlay.mp4",
  "artifact_url": "https://store/overlay.mp4",
  "artifact_hash": "<sha256>",
  "content_type": "video/mp4",
  "size_bytes": 28382193,
  "width": 1920,
  "height": 1080,
  "fps_num": 30,
  "fps_den": 1,
  "frame_count": 546,
  "duration_us": 18200000,
  "profile_id": "velox-h264-1080p30-v1",
  "copy_eligible": true,
  "codec": "h264",
  "codec_profile": "high",
  "closed_gop": true,
  "first_frame_keyframe": true,
  "backend": "software",
  "chronon_version": "0.1.0"
}
```

`copy_eligible`, `closed_gop` and `first_frame_keyframe` are the safety
guarantees: nobody has to guess whether a stream-copy is safe.

Who consumes them, accurately: a completed CHUNK family is assembled by the
worker's `ParentFinalizer` through the daemon's `ASSEMBLE_SEGMENTS`, and the
queue's parent-assembly contract (`queue.ValidateChildren`) refuses a family
whose children are not certified copy-safe — the three flags above are enforced
there, and a job carrying a `frame_range` is always structurally probed so the
flags exist. The VeloxEditing packet-copy assembler (`video.assemble.copy.v1`)
validates the same facts; it is currently **certified but unwired** (no
production caller) because overlays are composited inside the single Chronon
render pass (`clip-render-plan.v1` → `PlanOverlaySegment`), so it must not be
described as the production assembly path.

### Worker

```json
{
  "id": "renderinggen-77",
  "hostname": "gpu-04",
  "status": "ready",
  "renderinggen_version": "0.1.0",
  "chronon_version": "0.9.4",
  "overlay_schema_version": 1,
  "gpu_backend": "vulkan",
  "gpu_device": "NVIDIA RTX 4090",
  "gpu_driver": "550.54",
  "started_at": "2026-08-14T09:00:00Z",
  "last_heartbeat_at": "2026-08-14T10:00:30Z"
}
```

`status` is one of `unknown`, `ready`, `busy`, `draining`, `offline`.

`overlay_schema_version` is the semantic overlay contract version the worker
accepts. It is derived (`renderinggen/internal/version.OverlaySchema`), not an
independent counter, so it always equals the enforced
`renderinggen.overlay-plan.v1` version (`overlay.SemanticSchemaVersion`).

## Lifecycle and lease semantics

```
pending ──claim──▶ running ──complete──▶ completed
   ▲                 │
   │                 ├──fail (attempts < max)──▶ pending (retry)
   │                 └──fail (attempts >= max)─▶ failed
   └──lease expiry── running (RequeueExpired)

running ──render stored, publication pending──▶ rendered
running | finalizing ──parent finalization──▶ finalizing ──▶ completed

pending | running | finalizing | rendered ──cancel──▶ cancelled (TERMINAL)
```

A **cancelled** job is never claimable and never requeued by `RequeueExpired`;
cancellation therefore cannot produce a second Chronon invocation even when the
cancel lands while the job is leased/running and the lease later elapses.

- A claim opens a lease (`lease` field) and records a new `render_attempt`; the
  attempt history is append-only, never overwritten. A pending job whose
  `not_before` is in the future is skipped by the claim until it is due, so a
  deferred backlog never blocks or is blocked by immediately-claimable work.
- The worker renews via `POST /jobs/{id}/renew`; if the lease expires before
  completion, `RequeueExpired` requeues the job (or fails it after
  `max_attempts`), and another worker may claim it.
- Every state transition is recorded as an append-only `render_event`, so
  `render_jobs` holds the *current* state while `render_attempts` and
  `render_events` hold the *history*.

## Backends

- **In-memory** (default) — for local/dev and tests; no persistence.
- **PostgreSQL** — enabled when `DATABASE_URL` is set. Stores jobs, attempts,
  artifacts, workers, events and metrics; the advisory-locked migrations in
  `migrations/` run at startup. Binary files never go in the database.

## Client packages

- `client/` — the public Go client (import
  `github.com/Marcuss-ops/RenderingGen/queue/client`). Producer side:
  `Submit`, `Get`, `Depth`, `Health`, `Wait`, `WaitTerminal`. `WaitTerminal`
  is the event-driven completion call: it long-polls `/jobs/{id}/wait` and
  degrades on its own to the polling `Wait` loop when the route is absent (an
  older queue server). Worker side: `Claim`, `ClaimWait`, `Complete`, `Fail`,
  `Renew`.
- `renderinggen/internal/queue` — the worker's adapter over `client/`.
- PipelineGen uses `client/` via an adapter in
  `internal/platform/renderinggen`.
