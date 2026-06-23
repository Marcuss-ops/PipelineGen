# Health Boundary Migration

> Priority: BLOCKER
>
> Current source: `internal/api/common/health.go`
>
> Target ownership:
>
> - `internal/application/system/health/` owns health policy and aggregation.
> - `internal/infrastructure/health/` owns SQL, Drive, Qdrant and job probes.
> - `internal/api/system/` owns query parsing and HTTP status mapping only.
> - `internal/app/` constructs and injects all concrete checkers.

## Problem statement

The current health handler is both HTTP transport and infrastructure probe. It
opens SQLite, reads OAuth token files, constructs HTTP requests to Google Drive
and Qdrant, and inspects the jobs table. This creates four problems:

1. API code owns infrastructure behavior.
2. The handler cannot be tested without filesystem/network/SQLite setup.
3. Job and Drive liveness are inferred through implementation details rather
   than capability contracts.
4. Architectural checks must special-case the health file, allowing the
   violation to survive indefinitely.

The migration must preserve the public routes:

```text
GET /health
GET /ready
```

It may correct response status semantics, but response-body changes must be
explicitly covered by compatibility tests.

## Target package layout

```text
internal/application/system/health/
    model.go
    errors.go
    ports.go
    service.go
    service_test.go

internal/infrastructure/health/
    sqlite_checker.go
    drive_checker.go
    qdrant_checker.go
    jobs_checker.go
    *_test.go

internal/api/system/
    health_handler.go
    health_handler_test.go
    handler.go or module.go

internal/app/
    compose_health.go
    wire_system.go
```

Do not create a second generic `health` framework elsewhere. This package must
be the single owner for system health aggregation.

## Application model

Use application-owned neutral types. Do not expose Gin values or concrete SDK
responses.

Suggested shape:

```go
package health

type CheckName string

const (
    CheckDatabase CheckName = "db"
    CheckDrive    CheckName = "drive"
    CheckQdrant  CheckName = "qdrant"
    CheckJobs     CheckName = "jobs"
)

type CheckResult struct {
    OK         bool
    DurationMS int64
    Details    map[string]any
    ErrorCode  string
    Error      string
}

type Request struct {
    Deep   bool
    Checks []CheckName
}

type Result struct {
    OK     bool
    Status string
    Checks map[CheckName]CheckResult
}
```

`Details` may contain stable diagnostics such as collection name, point count or
`enabled=false`, but must never expose OAuth tokens, raw DSNs, filesystem paths,
SQL text or internal error stacks.

## Application ports

The application service should depend on one small interface per capability or
one uniform checker interface registered by name.

Recommended uniform contract:

```go
type Checker interface {
    Name() CheckName
    Check(ctx context.Context) CheckResult
}
```

The service stores checkers in a map built at construction time:

```go
type Service struct {
    checkers map[CheckName]Checker
}

func NewService(checkers ...Checker) (*Service, error)
func (s *Service) Run(ctx context.Context, req Request) (Result, error)
func (s *Service) Ready(ctx context.Context) (Result, error)
```

Constructor rules:

- Reject duplicate checker names.
- Reject typed-nil checkers using the existing `pkg/portutil` convention.
- Missing optional checkers may be omitted, but requested missing checks return
  a typed `ErrCheckerUnavailable`.
- The service owns the canonical allowlist. Unknown check names return
  `ErrUnknownCheck`; they must never produce a false healthy response.

Suggested typed errors:

```go
var (
    ErrUnknownCheck       = errors.New("unknown health check")
    ErrCheckerUnavailable = errors.New("health checker unavailable")
)
```

## Request semantics

### Fast health

`GET /health` without `deep` or `check` performs no remote I/O. It confirms the
process and router are alive:

```json
{"ok":true,"status":"healthy"}
```

### Deep health

`GET /health?deep=true` runs every registered checker with a request-scoped
timeout.

- All checks healthy: HTTP 200.
- One or more required checks unhealthy: HTTP 503.
- Disabled optional capability: checker returns `OK=true` and
  `details.enabled=false`.

### Selected checks

`GET /health?check=db,drive` runs only the requested checks.

- Trim and de-duplicate names.
- Empty entries are ignored.
- Any unknown name returns HTTP 400 with a stable error code.
- A known but unavailable checker returns HTTP 503.

### Readiness

`GET /ready` should run only startup-critical checks. Define the list in the
application service, not the handler. Recommended critical checks:

- database connection and schema availability;
- job store/broker availability when workers are enabled;
- required configuration validation.

Drive and Qdrant readiness should be required only when the corresponding
feature is enabled and necessary for the selected deployment mode.

## Infrastructure adapters

### SQLite checker

File: `internal/infrastructure/health/sqlite_checker.go`

Rules:

- Inject the already-open canonical database handle or a narrow `PingContext`
  interface. Do not reopen the database from `Storage.DataDir`.
- Verify connection with `PingContext`.
- If schema verification is required, query through the existing database
  ownership package and check canonical tables/migration state.
- Do not import Gin.

Suggested dependency:

```go
type DBPinger interface {
    PingContext(context.Context) error
}
```

If table inspection is retained, define a dedicated repository method such as
`SchemaReady(ctx) error` under infrastructure/database instead of embedding raw
SQL in the checker.

### Drive checker

File: `internal/infrastructure/health/drive_checker.go`

Rules:

- Inject the already-created Drive client or a narrow adapter.
- Do not read `token.json` or `credentials.json` in the checker.
- Do not parse OAuth token files manually.
- Probe a low-cost endpoint through the official client.
- Treat authentication errors differently from network timeouts in `ErrorCode`.

Suggested narrow port implemented by an infrastructure adapter:

```go
type DriveProbe interface {
    Ping(ctx context.Context) error
}
```

The checker remains infrastructure because the implementation uses the Drive
SDK; the application service sees only `Checker`.

### Qdrant checker

File: `internal/infrastructure/health/qdrant_checker.go`

Rules:

- Reuse the existing Qdrant service/client instead of constructing a second
  raw `http.Client` in the API layer.
- Return healthy-disabled when vector search is disabled.
- Probe readiness and optionally collection statistics.
- Use bounded context timeouts inherited from the application request.
- Do not silently convert collection-not-found into healthy if the collection
  is required by the enabled feature.

### Jobs checker

File: `internal/infrastructure/health/jobs_checker.go`

Rules:

- Do not infer broker liveness solely from the existence of a SQLite table.
- Inject a narrow application/infrastructure capability that can verify the
  canonical store or broker.
- At minimum, ping the canonical job store and verify required schema through
  its repository.
- If the local runner exposes lifecycle state, include it as a separate detail
  rather than conflating it with database readiness.

Possible result details:

```json
{
  "store_reachable": true,
  "runner_enabled": true,
  "runner_started": true
}
```

## Composition wiring

Create a focused same-package composition file:

```text
internal/app/compose_health.go
```

Recommended bundle:

```go
type HealthBundle struct {
    Service *health.Service
}
```

`BuildHealthBundle` constructs the concrete checkers from existing root
capabilities:

```go
func BuildHealthBundle(
    cfg *config.Config,
    db *storage.SQLiteDB,
    driveClient *drive.Service,
    vectorSvc *qdrant.Service,
    jobs *JobsBundle,
    log *zap.Logger,
) (*HealthBundle, error)
```

Keep the dependency count narrow. If the signature grows, pass small existing
bundles rather than the whole `ComposeRoot`.

Wire the resulting application service into `internal/api/system`:

```go
healthHandler := systemapi.NewHealthHandler(root.Health.Service, log)
```

The router must not call `common.NewHealthHandler(cfg)` after migration.

## Thin HTTP handler

The final API handler may do only the following:

1. Parse `deep` and `check` query values.
2. Convert check strings to application names.
3. Call `health.Service.Run` or `Ready`.
4. Map typed application errors to status codes.
5. Serialize the application result.

It must not:

- open databases;
- read files;
- create network clients;
- know token paths, collection URLs or database filenames;
- query schema tables;
- start goroutines.

Recommended status mapping:

| Condition | HTTP |
|---|---:|
| Fast health OK | 200 |
| Deep/selected checks all OK | 200 |
| Known check unhealthy/unavailable | 503 |
| Unknown check name | 400 |
| Invalid query format | 400 |
| Request timeout/cancel | 503 or 499-equivalent logging; never report healthy |
| Internal unexpected error | 500 |

## Migration sequence

### Phase 0: characterization tests

Before moving code, add handler tests covering current routes and desired fixed
semantics:

- fast `/health` returns 200 and short body;
- deep healthy returns 200;
- deep unhealthy returns 503;
- selected known checks execute only those checks;
- unknown check returns 400;
- `/ready` returns 503 when a critical checker fails;
- disabled Qdrant/Drive behavior follows feature flags.

Use fake application checkers. No real network or SQLite is required for API
tests.

### Phase 1: application package

Add models, errors, checker registry and aggregation service. Unit-test:

- duplicate checker rejection;
- stable execution order where deterministic output matters;
- unknown names;
- unavailable checkers;
- timeout/cancellation propagation;
- all-healthy and partially-unhealthy aggregation.

### Phase 2: concrete adapters

Implement SQLite, Drive, Qdrant and jobs checkers with focused tests. Prefer
existing service fakes or `httptest.Server` for network clients. Do not use live
Google/Qdrant dependencies in unit tests.

### Phase 3: composition

Build and inject `HealthBundle`. Verify typed-nil guards and optional-feature
behavior.

### Phase 4: transport replacement

Move the route handler to `internal/api/system/health_handler.go`, switch router
wiring, then delete `internal/api/common/health.go`.

If `common` contains other utilities, leave those files; do not preserve a
health forwarding wrapper.

### Phase 5: architecture gate

Remove the `common/health.go` exclusion from
`scripts/ci-architectural-checks.sh`. Add a hard rule that fails on forbidden
infrastructure imports anywhere under `internal/api/`, except explicitly
justified middleware/config binding cases.

## Tests and validation

Focused commands:

```bash
go test ./internal/application/system/health/...
go test ./internal/infrastructure/health/...
go test ./internal/api/system/...
go test ./internal/app/...
go vet ./internal/application/system/health/... ./internal/infrastructure/health/... ./internal/api/system/... ./internal/app/...
go build ./...
bash scripts/ci-architectural-checks.sh
```

Static exit checks:

```bash
! rg 'database/sql|go-sqlite3|googleapis.com/drive|qdrant|os\.ReadFile|sql\.Open' internal/api --type go
! test -f internal/api/common/health.go
rg 'NewHealthHandler' internal/app internal/api --type go
```

## Definition of done

This migration is complete when:

- health policy and aggregation live under
  `internal/application/system/health/`;
- SQL, Drive, Qdrant and jobs probes live under
  `internal/infrastructure/health/`;
- the API handler contains only parsing, service invocation and response
  mapping;
- unknown selected checks return HTTP 400;
- unhealthy deep/readiness checks return HTTP 503;
- existing Drive/Qdrant/database clients are reused rather than recreated;
- the API architecture gate has no health-file exception;
- generated API docs show only the real `/health` and `/ready` routes;
- focused tests, full build, vet and architecture checks pass.
