# QDRANT-005 — Qdrant readiness probe + lifecycle wiring (Phase 1 / 3)

> Ticket: QDRANT-005 (June 2026) — three-phase closure:
> 1. **Phase 1 (this closure)** — `HealthProbe` against
>    `GET {baseURL}/collections`, registered with the new
>    `api.LifecycleManager.AddProbe(name, fn)` so `/ready` reflects
>    Qdrant reachability, not just SQLite.
> 2. Phase 2 — golden queries + filter smoke placeholders wire into
>    `buildSwitchReport` (TODOs in `cmd/admin/reindex_qdrant.go`); outbox
>    dead-letter counter feeds `SwitchReport.DeadLetterOpen`.
> 3. Phase 3 — background "stale-link cleaner" (the 12-hour sweep that
>    removes Qdrant points whose Drive file was trashed).
> Owner: `internal/infrastructure/qdrant/` + `internal/app/` +
> `internal/api/`.

## STATO REALE (June 2026 closure — Phase 1)

- `internal/infrastructure/qdrant/health.go` — concrete `HealthProbe`
  with `Probe(ctx context.Context) error` issuing a single
  `GET {baseURL}/collections` with a dedicated `*http.Client`
  (5s timeout, plus `context.WithTimeout(5s)` for defence in depth).
  Sends `X-Api-Key` header if `client.APIKey() != ""`.
- `internal/infrastructure/qdrant/client.go` — `Client.apiKey` field
  + public accessor `APIKey()` so the probe (and any future
  authenticated diagnostic) can sign requests without importing the
  private state.
- `internal/api/server.go` — `LifecycleManager` interface extended
  with `AddProbe(name string, fn func(ctx context.Context) error)`.
  The `minimalLifecycle` returned by `NewMinimalLifecycle` now
  implements the new method as a no-op (for tests / dev bootstrap).
- `internal/app/server_lifecycle.go` — `*serverLifecycle` decomposes
  the three fixed probe fields (dbProbe / vectorProbe / driveProbe)
  into a `probes []*probeEntry` slice; the constructor still takes
  those three probes individually and wires them via `AddProbe`
  internally so existing wire paths are unchanged.
- `internal/app/build_bundles_process.go` — when
  `cfg.Qdrant.Enabled && clipIndexerService.IsEnabled()`,
  `qdrant.NewHealthProbe(qdrantClient)` is stored in
  `ProcessBundle.QdrantHealthProbe`.
- `internal/app/wire_services.go` — reads `QdrantHealthProbe` with a
  type-assertion into `AppDeps.QdrantProbe` (typed
  `interface{ Probe(ctx context.Context) error }`).
- `cmd/server/main.go` — direct call
  `deps.Lifecycle.AddProbe("qdrant", deps.QdrantProbe.Probe)`. The
  earlier type-assertion-the-LifecycleManager pattern (which silently
  failed in production) is gone.
- `internal/api/routes_test.go::TestRoutes_NoApiInternalV1Prefix` —
  anti-regression test pinning the QDRANT-002 separation-of-routes
  + QDRANT-005 wiring invariants.

## LEGACY DA ELIMINARE

| Item | Where | Status |
|---|---|---|
| Type-assertion `deps.Lifecycle.(interface{ AddProbe(...) })` that silently dropped Qdrant liveness from `/ready` | `cmd/server/main.go` (pre-PR) | **removed** — direct call against `LifecycleManager.AddProbe` |
| HTTP probes that used `http.DefaultClient` without a timeout context | QDRANT-005 (this closure) | replaced by the dedicated `*http.Client{Timeout: 5*time.Second}` + `context.WithTimeout(5s)` |
| `/ready` returning 200 when Qdrant was unreachable | `internal/api/server.go` (pre-PR) | **fixed** — probe registered with `api.LifecycleManager.AddProbe`; readiness waits for it |
| Qdrant API key ignored on the probe | n/a (probe didn’t exist) | **fixed** — `X-Api-Key` header sent when `cfg.APIKey != ""` |

## GATE ANTI-REGRESSIONE

```bash
# 1. The probe must be wired via the interface method, not via
#    type-assertion that could silently fail.
grep -n "Lifecycle.AddProbe\|deps.Lifecycle.AddProbe" cmd/server/main.go internal/api/
# → at least one direct call. Type-assertion pattern removed.

# 2. The HealthProbe sends X-Api-Key when configured (no silent skip).
grep -n "X-Api-Key\|APIKey" internal/infrastructure/qdrant/health.go internal/infrastructure/qdrant/client.go

# 3. The probe uses a dedicated client (NOT http.DefaultClient) with a
#    real timeout.
grep -n "http.DefaultClient" internal/infrastructure/qdrant/health.go  # → 0 hits

# 4. Phase 2 + Phase 3 placeholders MUST stay explicit (their TODO
#    markers are the contract that future PRs will pick up):
grep -n "TODO QDRANT-005" cmd/admin/reindex_qdrant.go internal/infrastructure/qdrant/health.go

# 5. Compiles clean and the anti-regression test green.
go build ./... && \
  go test ./internal/api/... -run 'TestRoutes_NoApiInternalV1Prefix' -count=1 -v

# 6. Integration smoke (operator runbook): with Qdrant reachable,
#    /ready returns 200; with Qdrant unreachable, /ready returns 503
#    within 5 s. Track on QDRANT-005/Phase 2.

# 7. Phase 2 + Phase 3 tickets — NOT closed in this PR:
#    * dead-letter counter sourced from outbox.Dispatcher
#    * golden-queries runner surfacing GoldenQueriesOK=false
#    * 12-hour stale-link cleaner loop
```

Any gate failure means Phase 1 regressed: the readiness plane no longer
reflects Qdrant reachability, which means `/ready` says "ready" while
the vector backend is down — a silent consumer-side failure mode.
