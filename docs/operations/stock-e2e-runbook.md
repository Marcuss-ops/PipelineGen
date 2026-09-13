# Stock E2E Runbook — Operational Procedure

**Status**: updated 2026-09-13. The Stock shell battery
(`tests/operational/stock_e2e_*.sh` + `stock_e2e_full_battery.sh`), the Artlist
clean-test battery (`tests/operational/artlist/run_all.sh`) and the two
StockRust live scripts (`tests/operational/youtube_stock_live_e2e.sh`,
`stockrust_live_e2e.sh`) were deleted by commit `7e6965aab` ("purge 94% shell
+ 87% python dust"). Their `make` targets and CI jobs were retired in lockstep
with this document. Git history is the archive for the removed procedures.

**Audience**: operators and maintainers verifying the Stock path
(`search/direct URL → stage → cut → render → Drive → media_assets → outbox →
index → search → download`).

---

## §1 — Canonical HTTP entry points

| Step | Surface | Contract |
|---|---|---|
| Run (search or direct URL) | `POST /api/stock-pipeline/run` | `search_queries` / `direct_urls` / `drive_urls` / `clips`, `drive_folder_id`, `folder_name`, `subfolder`, the four-part duration contract, `async`, `persist`. Unknown fields are rejected (`UNKNOWN_FIELD`) |
| Search and run | `POST /api/stock-pipeline/search-and-run` | `queries: [{q, limit}]` — **not** `search_queries` — plus the same run contract. `search_queries` is the legacy `/run` shape: sent here it is ignored by the binding and the request then fails the source-presence gate with 400 |
| Job state | `GET /api/jobs/{job_id}/full` | poll until terminal: `SUCCEEDED` / `INDEX_PENDING` / `FAILED` / `CANCELLED` / `DEAD_LETTER` |
| Unified search | `POST /api/media/search` | `query`, `sources:["stock"]`, `mode:"hybrid"`, `universe:"catalog"`, `filters` |
| Download | `POST /api/media/:source/clips/:id/download` | `source=stock` → the produced MP4 (non-zero size, video stream, duration > 0) |

**Explicit duration contract**: `target_total_duration_seconds`,
`target_duration_per_source_seconds`, `clips_per_source` and
`clip_duration_seconds` are all-or-nothing. Once any one is supplied the other
three are required, `target_duration_per_source_seconds` must equal
`clips_per_source × clip_duration_seconds`, `target_total_duration_seconds`
must be a multiple of `target_duration_per_source_seconds`, and `download_mode`
must be `sections_only`. A partial contract is rejected with
`INVALID_PAYLOAD` (it is never silently downgraded to the legacy shape).

**Acknowledgement vs state**: `/api/stock-pipeline/run` returns the endpoint
acknowledgement (`status: "QUEUED"` async, `"completed"` inline, `"error"` on
rejection) plus `job_id`/`run_id` and `deduplicated`. That is **not** the job
outcome — read the broker state through `/api/jobs/{id}/full`.
`error_code` ∈ `UNKNOWN_FIELD | INVALID_URL | PATH_TRAVERSAL |
MAX_CLIPS_EXCEEDED | INVALID_PAYLOAD`; legacy top-level `group`/`folder_id`/
`folder_path`/`subfolder_name`/`create_subfolder` are rejected on
`/api/clips/process` and must be nested under `destination`.

## §2 — Canonical live gate (10 steps)

The Stock path's live certification is the shared pipeline E2E battery; there
is no Stock-only live target any more.

| Target | Layer | Driver |
|---|---|---|
| `make verify-pipeline-e2e` | hermetic, in-process | `internal/platform/httpserver/server_pipeline_e2e_test.go` |
| `make verify-pipeline-e2e-live` | live, 10/10 required | `tests/operational/pipeline_live_e2e.sh` (depends on `auth-check`) |

The Stock legs are steps 6–8 plus the stock half of step 9: `/run` with a
direct URL → `/search-and-run` with `queries` → Drive artifact present in the
job result → asset retrievable via `/api/media/search` and downloadable
through `/api/media/stock/clips/{id}/download` (>100 KB, decodable video
stream). Steps 3/4/5/10 cover the YouTube path and the idempotent replay.

Required environment: `DRIVE_ROOT_FOLDER_ID`, `STOCK_DIRECT_URL`, plus
`VELOX_ADMIN_TOKEN` (or `TOKEN_FILE`) and a running server. Plan a run with
`bash tests/operational/pipeline_live_e2e.sh --dry`; artifacts are retained
under `tests/operational/results/pipeline-live/`.

Registered in `config/verify-components.json` under `stock.live_tests`, so it
is reachable through the component runner and never part of the push chain.

## §3 — Headless Stock test surfaces (component gates)

| Target | What it covers |
|---|---|
| `make verify-stock-unit` | `test-stock-component` + `test-youtube-stock-fast` (contract, URL, metadata, transcript, highlights, download plan) |
| `make verify-stock-integration` | `test-youtube-stock-local` + `test-youtube-stock-resilience` (partial download, cache, dedupe, index, recovery, concurrency) |
| `make test-stock-youtube-e2e` | full stock package suite (`./internal/capabilities/assets/providers/stock/stockplan`) |
| `make test-stock-drive` / `test-stock-cut` / `test-stock-partial-download` | per-stage package suites under `stockpipeline`, `downloader` |
| `make benchmark-stock-download` | package benchmarks |

These are headless and CPU-first: they never require Drive, Qdrant, a browser,
or a running server.

## §4 — StockRust certification boundary

The render boundary is certified at three levels. **L1 and L3 are currently
shellless**: their live scripts were deleted, so `STOCKRUST=CERTIFIED` is
carried by the L2 Go battery plus the Rust crate tests.

| Layer | Former owner | Current owner |
|---|---|---|
| **L1** HTTP upstream (discovery → transcript selection → cut → persist → download) | `tests/operational/youtube_stock_live_e2e.sh` (deleted) | *none — reintroduce as a tracked driver before quoting L1* |
| **L2** Go adapter → Rust | `internal/platform/media/rustexec/*_test.go` | unchanged (canonical owner) |
| **L3** Rust binary (health, `render_stock` protocol, concat + full decode, concurrency, fail-closed, RTF) | `tests/operational/stockrust_live_e2e.sh` (deleted) | `cargo test --manifest-path rust/Cargo.toml` + the Go e2e suites in `rustexec` |

**L2 canonical assertions** (the only complete coverage of these surfaces):
`render.ValidateRenderPlan` + `ValidateManifestFiles` + `request.Validate()`
transport re-validation, manifest/plan/asset hash-drift rejection,
`render_stock → mux_audio_copy` final-audio sequence, encoder policy.

Honest limitation (godlike/07): the historical two-script battery never covered
the middle layer, and today no shell script covers L1/L3 at all. Canonical
`render_plan`, final audio copy and tamper hash-drift are certified **only** by
the L2 Go tests; do not claim end-to-end shell coverage for them.

## §5 — Native stage timing + persistence (`performance_runs`)

`render_stock` reports its ffmpeg encode wall time natively in
`metadata.ffmpeg_ms` (same pattern as `render_audio_plan`). The three-wall
breakdown is measured by `stockrust_performance_e2e_test.go`:

| Wall | Owner | Source |
|---|---|---|
| `stock.render` | Go `RenderCanonicalPlan` | `time.Since` around the call |
| `rust process` | `timingRunner` wrapping `persistentRustProcessRunner` | per-request round-trip |
| `ffmpeg` | Rust binary | `metadata.ffmpeg_ms` (native, no external shim) |

Invariant asserted by the test: `ffmpeg ≤ rust ≤ stock.render`. Canonical owner
of the `ffmpeg_ms` wire field: `rust/pipelinegen-muscles/src/protocol.rs::MediaMetadata`
+ `internal/platform/media/rustexec/protocol.go::mediaMetadata.FFmpegMS`.

Persistence: `wall_ms` lands in its dedicated column, `rtf` / `ffmpeg_ms` / the
full breakdown in `metadata_json` (`stockrustRunMetadata`). The write is
env-gated — set `STOCKRUST_PERF_DB_PATH` to a migrated SQLite DB, otherwise the
run is record-only (hermetic). `run_id` is the primary key with
`ON CONFLICT DO UPDATE`, so re-recording converges.

```sql
SELECT started_at, wall_ms,
       json_extract(metadata_json,'$.rtf')       AS rtf,
       json_extract(metadata_json,'$.ffmpeg_ms') AS ffmpeg_ms
FROM performance_runs
WHERE workload_id='stockrust_render'
ORDER BY started_at;
```

Round-trip proof: `TestStockRustPerformancePersistenceRoundTrip`.

## §6 — Policy for live batteries (binding)

Any future live battery must be:

1. **Tracked and executable** — no target may reference a path that does not
   exist in the repository.
2. **Manual-trigger only** — `workflow_dispatch`, never `push` / `pull_request`
   / `schedule`. Live stock runs write to Drive and mutate the media SSOT.
3. **Fail-closed with a machine-readable verdict** — emit one terminal verdict
   line and exit non-zero on the first unmet assertion.
4. **Receipt-backed** when it certifies a release — a run that cannot produce a
   verifiable receipt cannot authorize a ship.

## §7 — Cross-references

- `tests/operational/README.md` — the operational battery catalog and the
  exit-code/redaction discipline shared by every script there.
- `AGENTS.md` — operational rules, media SSOT cutover, gate hierarchy.
- `docs/operations/verify-release-and-live.md` — tier-3 pre-deploy gate, the
  tier-4 retirement record and the current live gate.
- `internal/platform/media/rustexec/` — L2 canonical tests and the
  `ffmpeg_ms` wire field.
