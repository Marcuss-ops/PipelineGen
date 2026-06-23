# Health Boundary Migration and Hardening

> Status: BOUNDARY COMPLETE — HARDENING OPEN
>
> Verified against `main` at `401e3847` on 2026-06-23.
>
> Priority now: MEDIUM. The former API-layer blocker has been removed.

## Verified result

The layer boundary is now correct:

```text
internal/api/common/health.go
    -> internal/application/system/health/Service
        -> internal/infrastructure/health/*Checker
```

The HTTP handler no longer imports or performs:

- `database/sql`;
- SQLite driver registration;
- OAuth token file reads;
- Google Drive HTTP requests;
- Qdrant HTTP requests;
- jobs-table queries.

Deep health now maps an unhealthy aggregate to HTTP 503, and the special
`health.go` exclusion was removed from the API SQL/import check.

## Implemented files

```text
internal/application/system/health/
    ports.go
    service.go

internal/infrastructure/health/
    sqlite_checker.go
    drive_checker.go
    qdrant_checker.go
    jobs_checker.go

internal/api/common/
    health.go                 # thin transport, retained in common
```

The package location differs from the original target (`internal/api/system/`),
but location alone is not a blocker because the current handler is transport
only.

## What is complete

### API transport boundary

`HealthHandler` now performs only:

1. nil-service protection;
2. query parsing;
3. request timeout creation;
4. service invocation;
5. HTTP status/JSON mapping.

### Application aggregation

`internal/application/system/health/Service` owns:

- the canonical check-name switch;
- checker selection;
- aggregate health status;
- missing-checker behavior;
- unknown-name unhealthy behavior.

### Infrastructure ownership

SQL, token-file access and remote HTTP probes are now located under
`internal/infrastructure/health/`, where implementation-specific behavior
belongs.

### HTTP status correction

- fast process health: HTTP 200;
- healthy deep/selected checks: HTTP 200;
- unhealthy deep/selected checks: HTTP 503;
- readiness database failure: HTTP 503.

## Remaining hardening

The boundary move is complete, but the implementation still duplicates
resources and lacks typed policy.

### 1. Unknown checks return 503 instead of 400

Current behavior:

```text
GET /health?check=unknown
-> application result OK=false
-> handler maps all unhealthy results to HTTP 503
```

Required behavior:

```text
unknown check name -> typed ErrUnknownCheck -> HTTP 400
known unhealthy checker -> HTTP 503
```

Implement typed errors or a structured result classification rather than
inferring error class from a generic map.

### 2. Duplicate check names are not normalized

The handler trims values but does not remove empty or duplicate entries. The
application service overwrites duplicate keys after executing the checker more
than once.

Required:

- reject or ignore empty entries;
- de-duplicate while preserving deterministic order;
- validate against a canonical registry before executing checks.

### 3. Generic `map[string]any` result contract

`CheckResult` currently requires conventions such as an `"ok"` key at runtime.
This is fragile and makes malformed checker output possible.

Recommended model:

```go
type CheckResult struct {
    OK         bool
    DurationMS int64
    ErrorCode  string
    Error      string
    Details    map[string]any
}
```

The JSON representation may remain compatible while the internal contract
becomes typed.

### 4. SQLite checker reopens the database

`SQLiteChecker` derives a path from `Storage.DataDir`, registers the SQLite
driver and opens a new handle on every probe.

Required:

- inject the canonical open DB or a narrow `PingContext` capability;
- avoid a second connection pool;
- move schema readiness to an existing database repository/migration service;
- do not duplicate the canonical media DB path calculation.

Suggested port:

```go
type DBProbe interface {
    PingContext(context.Context) error
    SchemaReady(context.Context) error
}
```

### 5. Jobs checker is only a table-existence check

The current jobs checker opens the same database again and verifies that a
`jobs` table exists. This does not establish runner or broker health.

Required details should distinguish:

```json
{
  "store_reachable": true,
  "schema_ready": true,
  "runner_enabled": true,
  "runner_started": true
}
```

Inject a narrow capability from the canonical jobs bundle rather than deriving
broker health from SQLite alone.

### 6. Drive checker rereads and parses the token file

The Drive checker currently:

- stores credential/token paths;
- reads `token.json` per request;
- parses `access_token` manually;
- creates a raw Drive REST request with its own HTTP client.

Required:

- reuse the already-created authenticated Drive client;
- call a low-cost official client operation;
- distinguish authentication, permission, timeout and network failures;
- never duplicate OAuth parsing logic.

Suggested adapter dependency:

```go
type DriveProbe interface {
    Ping(context.Context) error
}
```

### 7. Qdrant checker creates a second client

The checker owns another `http.Client` and reconstructs `/readyz` and collection
URLs instead of reusing the canonical Qdrant service.

Required:

- inject a probe exposed by the existing Qdrant adapter/service;
- keep disabled-vector-search as healthy with `enabled=false`;
- make collection-not-found unhealthy when the enabled deployment requires the
  collection;
- preserve request-scoped cancellation.

### 8. Readiness policy is hard-coded in the handler

`Ready` currently calls:

```go
svc.Check(ctx, []string{"db"})
```

The application service should own the required readiness set because it depends
on deployment capabilities and feature flags.

Target API:

```go
func (s *Service) Ready(ctx context.Context) (Result, error)
func (s *Service) Check(ctx context.Context, req Request) (Result, error)
```

### 9. Checker tests are still required

Add focused tests for:

- DB success/failure and schema failure using a fake probe;
- Drive auth/network/timeout mapping;
- disabled and enabled Qdrant behavior;
- missing collection behavior;
- jobs store versus runner state;
- unknown check HTTP 400;
- duplicate/empty check normalization;
- context cancellation;
- aggregate partial failure;
- readiness feature-policy selection.

Do not require live Google Drive or Qdrant in unit tests.

## Recommended target structure

```text
internal/application/system/health/
    model.go
    errors.go
    registry.go
    ports.go
    service.go
    service_test.go

internal/infrastructure/health/
    sqlite_checker.go
    drive_checker.go
    qdrant_checker.go
    jobs_checker.go
    *_test.go

internal/app/
    compose_health.go

internal/api/common/
    health.go
    health_integration_test.go
```

Moving the handler from `common` to `system` is optional unless `common` becomes
a mixed-responsibility package. The critical requirement is transport purity,
which is already satisfied.

## Recommended checker registry

Replace the fixed four-field service with a canonical registry:

```go
type CheckName string

type Checker interface {
    Name() CheckName
    Check(context.Context) CheckResult
}

type Service struct {
    checkers map[CheckName]Checker
    ready    []CheckName
}
```

Constructor requirements:

- reject duplicate names;
- reject typed-nil checker values using `pkg/portutil` conventions;
- store deterministic order separately from the lookup map;
- validate readiness names at construction;
- permit omitted optional capabilities without panics.

## HTTP mapping

| Condition | HTTP |
|---|---:|
| Fast process health | 200 |
| Selected/deep checks healthy | 200 |
| Known component unhealthy | 503 |
| Known checker unavailable | 503 |
| Unknown check name | 400 |
| Invalid query | 400 |
| Unexpected internal error | 500 |

## Migration sequence

1. Add typed model and typed errors without changing JSON output.
2. Introduce checker registry and normalization tests.
3. Replace path-based SQLite/jobs checkers with injected canonical capabilities.
4. Replace Drive token parsing with the existing authenticated client.
5. Replace raw Qdrant HTTP with a canonical Qdrant probe.
6. Move readiness policy into the application service.
7. Add adapter and handler tests.
8. Keep API architectural checks hard for SQL/Drive SDK and expand Check 19 to
   cover every infrastructure import.

## Validation

```bash
go test ./internal/application/system/health/...
go test ./internal/infrastructure/health/...
go test ./internal/api/common/...
go test ./internal/app/...
go vet ./internal/application/system/health/... ./internal/infrastructure/health/... ./internal/api/common/... ./internal/app/...
go build ./...
bash scripts/ci-architectural-checks.sh

! rg 'database/sql|go-sqlite3|googleapis.com/drive|qdrant|os\.ReadFile|sql\.Open' internal/api/common/health.go
```

## Hardening definition of done

Health hardening is complete when:

- unknown selected checks return HTTP 400;
- check results and expected errors are typed;
- duplicate and empty check names are normalized;
- readiness policy is owned by the application service;
- SQLite and jobs probes reuse canonical DB/job capabilities;
- Drive and Qdrant probes reuse existing authenticated clients/services;
- job health distinguishes store/schema and runner lifecycle;
- checker and transport tests cover failures without live external services;
- no broad health-specific architecture exception exists.
