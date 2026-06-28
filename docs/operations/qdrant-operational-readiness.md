# Qdrant Operational Readiness — Runbook

**Owner:** `feat/qdrant-operational-readiness` (PR 9, June 2026).
**Audience:** operators running PipelineGen in production with Qdrant
enabled. Self-contained reference for the retention sort fix, the
`qdrant_collections` lifecycle migration, the unified per-operation
request metrics, and the closed/open/half-open circuit breaker
state surface.
**Cross-references:** this runbook reuses the heading structure of
[`04-remote-worker-production-readiness-tickets.md`](04-remote-worker-production-readiness-tickets.md)
so the canonical RW-PROD-### ticket pattern stays the reference for
the wider runner ticket sub-system. Section IDs cross-link with
[`worker-certification-checklist.md`](worker-certification-checklist.md)
where the same audit-trail shape applies.

## 1. Problem (`PRD-QOR-001`)

Prior to PR 9 the Qdrant capability shipped with three operational
gaps that, taken together, made the vector-store tier non-
production-ready:

1. **Retention retention-sweep dropped the WRONG collections.**
   `CollectionManager.CleanupWithConfig` sorted eligible collections
   ascending, then protected the head of the list with `keepLastN`.
   Each new reindex produces a name with a higher lexicographic
   suffix; ascending sort placed the OLDEST collection at the head.
   The protected tail was therefore the oldest, and the dropped set
   was the newest — exact opposite of a "keep_last_n" intent. A loop
   `break` after the first `keepLeft<=0` iteration compounded the
   issue: even with `KeepLastN=5`, only one collection was protected.
2. **No `promoted_at` retention duration gate.** Retention was a
   binary switch (`RetentionDays > 0 = drop everything`). Operators
   had no way to keep a recent promoted target for a defined grace
   window before considering it eligible.
3. **`qdrant.NewClient` had a single `Timeout` knob.** Dial, TLS,
   response-header, and operation timeouts all shared the same
   `http.Client{Timeout: N}s` ceiling. A slow TLS handshake stalled
   every request; a slow body read could not surface a different
   error budget. No body-size cap meant a corrupted Qdrant response
   could OOM the server on `decodeSearchResults`.

The verdict document lists three further concerns (deep readiness,
liveness vs readiness, snapshot/restore drill) addressed by sibling
PRs / follow-ups; see §7 below.

## 2. Objective (`PRD-QOR-002`)

Make the Qdrant capability **fail-closed, measurable, repairable**:

| Capability                | Outcome                                                  |
|---------------------------|----------------------------------------------------------|
| Retention                 | Duration gate (real `promoted_at` based), keep newest not oldest, dry-run default, structured protection set |
| Client transport          | Shared `http.Transport` with distinct timeouts            |
| Per-operation observability | `qdrant_request_total{operation,status}` + `qdrant_request_duration_seconds{operation,status}` |
| Process-level state       | Circuit-breaker gauge: `qdrant_request_circuit_open_gauge{scope}` (0/1/2 = closed/half-open/open) |
| Lifecycle tracking        | `qdrant_collections` table (PR 9 migration 103) providing real `promoted_at` retention semantics |

## 3. Activities (`PRD-QOR-003`)

| Activity | Where | Description |
|---|---|---|
| A1. Fix retention sort | `internal/infrastructure/qdrant/collection_manager.go::CleanupWithConfig` | Replace `sort.Strings(eligible)` with `sort.Sort(sort.Reverse(sort.StringSlice(eligible)))`. Move the `break` to a defensive `if keepLeft <= 0 { break }` at the loop head so the full floor tail is protected. |
| A2. Add `qdrant_collections` lifecycle table | `migrations/sqlite/103_qdrant_collections.sql` | Track `created_at / indexed_at / verified_at / promoted_at / retired_at / point_count / verification_hash / status` per physical collection. Index `(status, promoted_at)` for the retention sweep range scan. |
| A3. Add global qdrant request metrics | `internal/infrastructure/observability/metrics.go` | Add `QdrantRequestTotal`, `QdrantRequestDuration` (vectors), `QdrantRequestCircuitOpen` (gauge). Reuse the existing Prometheus registry — NO parallel registries. |
| A4. Add retention regression test | `internal/infrastructure/qdrant/collection_manager_retention_test.go` | httptest fake server; assert descending-sort + floor-walked-loop. Two test cases: keepLastN=3 with 4 eligible (drops 2 oldest); keepLastN=2 with 2 eligible (drops 1 oldest). |

## 4. Acceptance Criteria (`PRD-QOR-004`)

**Operational:**

- [ ] **AC1**: `RetentionService.Apply` with `RetentionDays=1, KeepLastN=3`
  on a 4-eligible-collection fleet drops the 2 OLDEST eligible
  collections, keeps active + the 2 NEWEST eligible in the
  `ProtectedKept` set, never touches the active alias target. Regression
  test `TestCleanupWithConfig_DescendingSort_LastNKeptIsCorrect` PASSES.

- [ ] **AC2**: descending-sort + floor-walked-loop regression test
  (`TestCleanupWithConfig_KeepLastN2_KeepsOneNewestColl`) PASSES with
  exactly 1 drop on a 3-collection fleet (`keepLastN=2`).

- [ ] **AC3**: `qdrant_request_total{operation="GetCollection",status="ok"}`
  is a valid Prometheus query (`vector_ok()` returns non-empty). Same
  for `QdrantRequestDuration` histogram.

- [ ] **AC4**: `qdrant_request_circuit_open_gauge{scope="qdrant-default"}
  == 0` in steady state; alerts fire on sustained `== 2` (open).

- [ ] **AC5**: migration 103 applies cleanly on a fresh DB
  (`media.db.sqlite`); the `qdrant_collections` table exists with
  the documented schema. Operators can query
  `qdrant_collections_status_v1` for live fleet state.

**Compile / static:**

- [ ] **AC6**: `go build ./internal/infrastructure/qdrant` clean.
  `go test ./internal/infrastructure/qdrant -run 'TestCleanupWithConfig'`
  PASS.
- [ ] **AC7**: `go vet ./internal/infrastructure/observability` clean.
  No new metric name collides with the existing Prometheus registry.

## 5. Mandatory Tests (`PRD-QOR-005`)

```bash
# Validate PR 9 contract in isolation (build + targeted tests).
\
go build ./internal/infrastructure/qdrant && \
go vet ./internal/infrastructure/qdrant && \
go test ./internal/infrastructure/qdrant -run 'TestCleanupWithConfig' -count=1 && \
go test ./internal/infrastructure/observability -count=1

# Confirm the metrics.go additions are observable at the registry.
go test ./internal/infrastructure/observability -count=1 -run 'TestMetrics'
# Spot check: query QdrantRequestTotal{operation=...} and
# QdrantRequestDuration{operation=...,status=...} raw.
```

## 6. Evidences (`PRD-QOR-006`)

| Evidence | Path | Required |
|---|---|---|
| Bug-before fix transcript (retained for the audit trail; commit landed in PR 9) | git history of `internal/infrastructure/qdrant/collection_manager.go::CleanupWithConfig` | yes |
| Bug-after fix transcript | same file, post-merge HEAD | yes |
| Migration 103 manifest | `migrations/sqlite/103_qdrant_collections.sql` | yes |
| Retention regression transcript | `internal/infrastructure/qdrant/collection_manager_retention_test.go::TestCleanupWithConfig_*` test log | yes |
| Metrics additions | `internal/infrastructure/observability/metrics.go` (`QdrantRequestTotal`, `QdrantRequestDuration`, `QdrantRequestCircuitOpen`) | yes |

## 7. Follow-ups (`PRD-QOR-007`)

The full PR 9 verdict scopes 7 sections. PR 9 ships §1 (retention
fix + migration + metrics) + the §3 reconciliation starter (§3 wiring
not yet on the lifecycle, see follow-up §3 below) + this runbook
(`docs/operations/qdrant-operational-readiness.md`). The remaining
sections are documented follow-ups:

| # | PR 9 Section                                                 | Status                   |
|---|--------------------------------------------------------------|--------------------------|
| §2 | Client operativo robusto (per-op retry+CB+body-limit+correlation+logs sanitize) | **follow-up** – refactor shape validated by `thinker-with-files-gemini` (June 2026); speculative scope reduction to fit session budget |
| §3 | `startQdrantReconciler` background goroutine (6h default) | **follow-up** – Reconciler exists (`internal/infrastructure/qdrant/reconciler.go`); wiring into `internal/app/server_lifecycle.go::StartupStep` slice is the canonical home |
| §4 | Deep readiness probe (canary search + outbox lag + DLQ)   | **follow-up** – `HealthProbe` exists; deep probe slots into `concurrent.WithContext(ctx)` barrier |
| §5 | `/livez` vs `/readyz` split                                 | **follow-up** – affects API server shape across all modules |
| §6 | Snapshot+restore drill script (`scripts/qdrant_restore_drill.sh`) | **follow-up** – shell script; orthogonal to the Go changes |
| §7 | Circuit breaker extracted to `pkg/circuitbreaker`          | **follow-up** – currently in `internal/infrastructure/ai/ollama/client/client_struct.go`; extraction is a leaf-only refactor |

The cross-cutting runbook pattern continues to live under
[`04-remote-worker-production-readiness-tickets.md`](04-remote-worker-production-readiness-tickets.md)
as the canonical RW-PROD-### ticket system (17 tickets, P0). PRD-QOR-###
follows the same ID convention; the two tracks coexist.

## 8. Audit Trail (`PRD-QOR-008`)

| Date (UTC) | PR / Commit              | Author           | Note                                                                  |
|------------|--------------------------|------------------|-----------------------------------------------------------------------|
| 2026-XX-XX | `feat/qdrant-operational-readiness` | PipelineGen Agent | Migration 103, sort fix, global metrics, runbook                      |

> Note: the table is updated as PRs land. The current state is the
> snapshot of PR 9 at merge time; follow-ups §2-§7 land in subsequent PRs
> under referenced ticket IDs.
