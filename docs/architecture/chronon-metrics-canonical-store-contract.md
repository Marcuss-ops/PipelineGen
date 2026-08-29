# PipelineGen — Chronon metrics canonical-store contract

**Status:** architectural contract
**Authority:** `internal/capabilities/cliprender` owns the sidecar contract (parser `chronon_sidecar.go` + `ChrononMetricsAdapter`); `internal/platform/sqlite/performance` owns the canonical granular store (`performance_operations`, migration 217); `internal/capabilities/scripts` + `internal/platform/sqlite/rendermetrics` own the canonical per-attempt row (`render_attempt_analytics`, migrations 215 + 227). The `cmd/admin` `performance-cold-warm` verifier is a read projection.
**Scope:** the rule that the Chronon sidecar JSON is a **transport/debug payload** and **SQLite is the canonical history of metrics** — where Chronon's measured phases live, how they get there, and what is forbidden.

This document is the normative contract for every Chronon metric surface. It complements the Job–Attempt–Run observability contract ([`job-attempt-run-observability-contract.md`](job-attempt-run-observability-contract.md)): that contract governs the kernel run model; this one governs the Chronon render-measurement boundary and its durable history. The current implementation surface is recorded in [`architecture/observability-measurement-matrix.yaml`](../../architecture/observability-measurement-matrix.yaml).

## 1. The rule

**The Chronon sidecar JSON (`*.timing.json`, schema `chronon3d.frame-timing.v1`) is a transport/debug payload — never the metrics database.** The canonical history of Chronon metrics is SQLite:

```text
SIDECAR JSON        = payload di trasporto/debug   (debugging approfondito, opzionale)
SQLite              = storia canonica delle metriche

CANONICAL GRANULAR STORE  = performance_operations        (migration 217)
CANONICAL PER-ATTEMPT ROW = render_attempt_analytics      (migrations 215 + 227)
```

The rule has three consequences:

1. **One parse, one canonical write.** The sidecar is parsed once by `cliprender.ParseChrononSidecar` and projected through the `ChrononMetricsAdapter` into `performance_operations` via the `OperationReportProjectionRecorder` seam — the only permitted path into that table. No consumer reads the sidecar as the source of truth.
2. **No new tables.** The granular phases and the per-attempt row both land in the existing canonical tables. A new `chronon_metrics_v2`-style table is forbidden (it would duplicate the architecture).
3. **Best-effort, fail-open.** A parse or record failure is logged and never fails the render. The sidecar may be absent (non-instrumented build): the canonical tables simply carry fewer rows. A missing phase is never recorded as a fabricated zero.

## 2. Data flow

The two Chronon render entry points publish into the same canonical store:

```text
┌─ Direct path (chronon_clip_renderer.go) ─────────────────────────────┐
│  chronon3d_cli --report → chronon.mp4 + *.timing.json (sidecar)      │
│        │                                                             │
│        ▼                                                             │
│  ParseChrononSidecar (exclusive_wall_timeline + job.gpu + cache)     │
│        │                                                             │
│        ▼                                                             │
│  ChrononMetricsAdapter.Publish                                       │
│        │  one OperationReport per measured phase                     │
│        ▼                                                             │
│  OperationReportProjectionRecorder → performance_operations          │
└──────────────────────────────────────────────────────────────────────┘

┌─ Queue path (render_queue.go) ───────────────────────────────────────┐
│  RenderingGen worker artifact (render_ms / encode_ms / output facts) │
│        │                                                             │
│        ▼                                                             │
│  BuildRenderAttemptAnalyticsWithWait → RenderAttemptRecorder         │
│        │                                                             │
│        ▼                                                             │
│  rendermetrics.Registry → render_attempt_analytics (upsert by        │
│                          attempt_id, idempotent)                     │
└──────────────────────────────────────────────────────────────────────┘

        both tables are read-only projected downstream:
        dashboard / benchmark / cold-warm verifier / Preparation Fabric
```

The direct path writes the granular exclusive-wall phases (`chronon.startup`, `chronon.input_open`, `chronon.prepare`, `chronon.render_loop`, `chronon.encoder_drain`, `chronon.ffprobe`, `chronon.sha256`) with structured `metadata_json` (backend, decoder, encoder, CUDA bytes, cache facts). The queue path writes the coarse per-attempt row. The two paths are complementary projections of the same render; neither replaces the other.

## 3. Canonical vs transport vs derived

| Layer | Surface | Role |
|---|---|---|
| **Canonical** | `performance_operations` (migration 217) | Granular phase history: one row per measured phase per run, with run/job identity resolved from the canonical run, certified output facts (source SHA/duration, WxH, fps, bytes) and `metadata_json`. |
| **Canonical** | `render_attempt_analytics` (migrations 215 + 227) | Coarse per-attempt row: `render_ms` / `encode_ms` from the certified artifact, content census, queue observation metrics, output facts, Drive identity. Upsert keyed by `attempt_id`. |
| **Transport/debug** | `*.timing.json` sidecar | The engine's own exclusive-wall measurements at their origin. Transport between Chronon and PipelineGen; kept on disk for deep debugging. Never queried as the metrics database. |
| **Transport/debug** | `/tmp/*.profile.json` + manual `jq` | Ad-hoc certification artifacts. Useful for one-off GPU certification; forbidden as the permanent workflow (see §4). |
| **Derived** | `cmd/admin performance-cold-warm` verifier | Read projection: `GROUP BY operation` (AVG/MIN/MAX `elapsed_ms`) over `performance_operations`, split cold #1 vs warm #2-N. Reads SQLite only. |
| **Derived** | `WorkHistorySource` / Preparation Fabric estimator | Read projection feeding `expected_work_ms` estimates. Reads SQLite only. |
| **Derived** | Prometheus / dashboards | Derived live views; never a second timer (per the Job–Attempt–Run contract §1.1). |

## 4. Forbidden patterns

```text
✗ Querying /tmp/*.profile.json with jq as the metrics workflow.
  The verifier must be: job_id / run_id → SQLite → comparative report.

✗ Creating a new table (chronon_metrics_v2, chronon_sidecar_*).
  The existing canonical tables are the single measurement point.

✗ INSERT into performance_operations outside the OperationReportProjectionRecorder seam.
  Only one canonical write path exists (one boundary → one OperationReport → recorder → SQLite).

✗ Re-timing a phase Chronon already measured (a second independent timer for the same boundary).

✗ Recording a missing phase as zero, or guessing cold/warm from cache flags.
  Missing measurement = absent row; the cold/warm split is positional by attempt order.
```

## 5. Allowed surfaces

- The sidecar JSON **may** remain on disk for deep debugging (per-run `*.chronon.timing.json` under the metrics dir) and is transported verbatim — it is never re-derived.
- The canonical tables are the **single source for queries**: dashboards, benchmark/regression reports, the cold-warm verifier, and the Preparation Fabric scheduler all read SQLite.
- `render_attempt_analytics` continues to record `render_ms`/`encode_ms` **in parallel** with the granular phases in `performance_operations` — no new table, both existing.

```text
DB   = storia canonica
JSON = diagnostica opzionale
```

## 6. Acceptance gates

The contract is met when the implementation demonstrates:

```text
SIDECAR_PARSED_ONCE=PASS              one parse → canonical write
PHASES_IN_PERFORMANCE_OPERATIONS=PASS chronon.* rows per measured phase, run-bound
ATTEMPT_ROW_IN_RENDER_ANALYTICS=PASS  render_ms/encode_ms upsert by attempt_id
NO_NEW_TABLES=PASS                    sqlite_master holds only the canonical tables
NO_ZERO_FABRICATION=PASS              absent phase = absent row
BEST_EFFORT_FAIL_OPEN=PASS            parse/record failure never fails the render
VERIFIER_READS_SQLITE_ONLY=PASS       cold-warm report = GROUP BY operation over
                                      performance_operations, never the sidecar
DUAL_WRITE_PARALLEL=PASS              render_attempt_analytics + performance_operations
                                      both receive rows for the same render
```
