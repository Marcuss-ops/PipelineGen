# QDRANT-002 — separation of internal routes from public `/api/*`

> Ticket: QDRANT-002 (June 2026) — outbox events handler + QDRANT-001
> recovery plan ticket. Splits the server-to-server internal surfaces
> (outbox, mediasearch) onto the WorkerAuth-protected `/internal/v1/*`
> namespace. Owns the anti-regression contract that these handlers must
> NEVER mount under `/api/*`.
> Owner: `internal/api/routes.go`, `internal/app/registry.go`.

## STATO REALE (June 2026 closure)

- `internal/api/routes.go::Setup()` mounts `OutboxHandler` +
  `MediasearchHandler` on `internalGroup` (which is the
  `engine.Group("/internal/v1")` derived AFTER `WorkerAuth`).
- `cmd/server/main.go` calls `server.SetOutboxHandler` /
  `server.SetMediasearchHandler` to plumb the handlers from
  `AppDeps.OutboxHandler` / `AppDeps.MediasearchHandler`.
- The `publicGroup` (under `/api`) does NOT include `OutboxHandler`
  or `MediasearchHandler`. Their registration in the public
  `module.Registry` was REMOVED in this closure.
- `internal/app/registry.go::WireRegistry` now produces a
  `RegistryWiring` field `OutboxHandler` + `MediasearchHandler` that
  is plumbed via `AppDeps`, **not** via `registry.RegisterAllRoutes`.

The result: `GET /api/internal/v1/outbox/*` returns 404; the canonical
path is `GET /internal/v1/outbox/{status,events}` and
`POST /internal/v1/media/search`.

## LEGACY DA ELIMINARE

| Item | Where | Status |
|---|---|---|
| `routes.go` lines that registered `outbox` / `mediasearch` in the public registry | pre-PR (June 2026) | **removed** |
| References to `/api/internal/v1/*` in handler smoke tests | `tests/operational/*.sh` | remaining — these tests target the wrong URL path and must be updated when runbooks are revised |
| `cmd/admin` debugging flags that curl `/api/internal/v1/outbox/status` | operator runbooks in `docs/operations/` | pre-PR docs will be retired on the next docs-sync PR |

Any reintroduction of a `/api/internal/*` mount is **a regression**: the
public API is anonymous (admin middleware), the internal API requires the
worker token.

## GATE ANTI-REGRESSIONE

```bash
# 1. No handler RegisterRoutes should be called by SetOutboxHandler/
#    SetMediasearchHandler on the public group.
grep -RIn "SetOutboxHandler\|SetMediasearchHandler" \
  internal/api/ cmd/server/
# → expect exactly the wiring in routes.go (1 cite) and the call in
#   cmd/server/main.go (1 cite). Anything else is a leak.

# 2. The public registry must not see outbox / mediasearch.
grep -RIn "/api/internal/v1" internal/api/routes.go  # → 0 hits late 2026 retry (only historical comments remain)

# 3. The dedicated anti-regression test goes green.
go test ./internal/api/... -run TestRoutes_NoApiInternalV1Prefix -count=1 -v  # → PASS

# 4. The internal handlers resolve correctly under /internal/v1.
curl -fsS -H "Authorization: Bearer $VELOX_WORKER_TOKEN" \
  http://127.0.0.1:8080/internal/v1/outbox/status  # → 200
curl -fsS -X POST -H "Authorization: Bearer $VELOX_WORKER_TOKEN" \
  http://127.0.0.1:8080/internal/v1/media/search \
  -H "Content-Type: application/json" -d '{"query":"test"}'  # → 200 or 4xx (mediasearch-specific)

# 5. Public path returns 404 (no leak).
curl -fsS http://127.0.0.1:8080/api/internal/v1/outbox/status  # → exit 22 / non-200
```

If any gate fails, the regression is a security leak (handlers out of
WorkerAuth-protected namespace) and must be reverted immediately.
