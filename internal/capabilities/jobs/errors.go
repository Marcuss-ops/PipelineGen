// Package jobs — errors.go (PR-jobs-retry-contract, July 2026).
//
// Canonical SSOT (godlike/06 one-canonical-owner-per-fact) for the typed
// sentinels consumed by the strict-typed retry + idempotency contract:
//
//   - ErrRegistryRequired            — composition-root fail-closed gate
//     for the *Service constructor; enforced
//     at NewService() startup so a nil-registry
//     wiring surfaces immediately as a typed
//     error rather than failing silently on
//     first Enqueue (godlike/07).
//
//   - ErrMaxRetriesUnknown           — strict typed lookup error returned by
//     Registry.GetMaxRetries when the jobType
//     is not registered. Propagates through
//     resolveMaxRetries() so Enqueue rejects
//     the request with the typed sentinel
//     instead of silently defaulting to the
//     legacy 3-retry safety net (which is
//     REMOVED in this PR — godlike/07
//     no-fake-availability).
//
//   - ErrUniqueConstraintViolation   — typed probe for the
//     (job.type, job.correlation_id)
//     SQLite UNIQUE-constraint rescue path in
//     Enqueue(). Set when the typed
//     `errors.As(sqlite3.Error, &sqliteErr)`
//     && `sqliteErr.ExtendedCode==sqlite3.ErrConstraintUnique`
//     probe fires on the Create() error;
//     the rescue path then attempts to
//     surface the existing job before
//     classifying the error.
//
// godlike/07 NO-FAKE-AVAILABILITY rationale (against the pre-PR shape):
// the pre-PR enqueue_service.go used `strings.Contains(err.Error(),
// "UNIQUE constraint")` (String-compare trap). A future mattn/go-sqlite3
// driver version changing the error string (e.g. uppercasing or
// translating into a different locale) would silently disable the
// rescue path without any test surfacing the regression — the rescue
// would never fire, every concurrency-collision Enqueue would return
// a generic "failed to create job" 5xx, with no diagnostic signal
// pointing at the rescue logic. The typed probe is invariant against
// driver string changes — the rescue path is the canonical contract.
// Package jobs — errors.go (PR-jobs-retry-contract, July 2026).
//
// Canonical SSOT surface (godlike/06 one-canonical-owner-per-fact):
//
//   - ErrRegistryRequired            — composition-root fail-closed gate
//     for the *Service constructor; enforced
//     at NewService() startup so a nil-registry
//     wiring surfaces immediately as a typed
//     error rather than failing silently on
//     first Enqueue (godlike/07).
//
//   - ErrMaxRetriesUnknown           — strict typed lookup error returned by
//     Registry.GetMaxRetries when the jobType
//     is not registered. Propagates through
//     resolveMaxRetries() so Enqueue rejects
//     the request with the typed sentinel
//     instead of silently defaulting to the
//     legacy 3-retry safety net (which is
//     REMOVED in this PR — godlike/07
//     no-fake-availability).
//
//   - ErrUniqueConstraintViolation   — typed probe for the
//     (job.type, job.correlation_id)
//     SQLite UNIQUE-constraint rescue path in
//     Enqueue(). Set when the typed
//     `errors.As(sqlite3.Error, &sqliteErr)`
//     && `sqliteErr.ExtendedCode==sqlite3.ErrConstraintUnique`
//     probe fires on the Create() error;
//     the rescue path then attempts to
//     surface the existing job before
//     classifying the error.
//
// godlike/07 NO-FAKE-AVAILABILITY rationale (against the pre-PR shape):
// the pre-PR enqueue_service.go used `strings.Contains(err.Error(),
// "UNIQUE constraint")` (String-compare trap). A future mattn/go-sqlite3
// driver version changing the error string (e.g. uppercasing or
// translating into a different locale) would silently disable the
// rescue path without any test surfacing the regression — the rescue
// would never fire, every concurrency-collision Enqueue would return
// a generic "failed to create job" 5xx, with no diagnostic signal
// pointing at the rescue logic. The typed probe is invariant against
// driver string changes — the rescue path is the canonical contract.
//
// Fase 5(b) cutover (July 2026): the application-layer
// `appjobs.ErrLeaseLost` re-export alias was removed (its only purpose
// was a back-compat shim for the pre-Fase-5(a) assignment chain
// appjobs ← sqljobs ← domjob). The canonical home is
// `internal/domain/job/errors.go`; callers probe via
// `errors.Is(err, kerneljob.ErrLeaseLost)`.
package jobs

import "errors"

// ErrRegistryRequired is returned by NewService(repo, dispatcher, log, reg)
// when reg is nil. Composition-root contract — fail-closed at startup.
//
// Errors.Is(err, ErrRegistryRequired) is the canonical probe.
var ErrRegistryRequired = errors.New("appjobs.Service: registry is required (constructor enforces fail-closed registry attachment; pass appjobs.Compose() at composition time)")

// ErrRepoRequired is returned by NewService when repo is nil. Mirrors
// ErrRegistryRequired's fail-closed contract — every constructor
// pre-condition failure MUST be a typed sentinel so callers can branch
// reactively without string-compare gymnastics (godlike/07 typed-error
// contract discipline; symmetric with ErrRegistryRequired).
//
// Errors.Is(err, ErrRepoRequired) is the canonical probe.
var ErrRepoRequired = errors.New("appjobs.Service: repo is required (constructor enforces fail-closed canonical-job.JobBroker-port attachment; pass *sqljobs.SQLiteStore at composition time)")

// ErrLogRequired is returned by NewService when log is nil. Mirrors
// ErrRegistryRequired's fail-closed contract.
//
// Errors.Is(err, ErrLogRequired) is the canonical probe.
var ErrLogRequired = errors.New("appjobs.Service: log is required (constructor enforces fail-closed *zap.Logger attachment; pass the composition-root logger at composition time)")

// ErrMaxRetriesUnknown is returned by Registry.GetMaxRetries(jobType)
// when the jobType is not registered. Propagates through
// resolveMaxRetries() so Enqueue rejects the request with the typed
// sentinel instead of silently defaulting to the legacy 3-retry safety
// net (which is REMOVED in this PR — godlike/07 no-fake-availability).
//
// Errors.Is(err, ErrMaxRetriesUnknown) is the canonical probe.
var ErrMaxRetriesUnknown = errors.New("appjobs.Registry: no entry for jobType (GetMaxRetries rejects unknown types; resolve via appjobs.Compose().Register(...) at the composition root)")

// ErrUniqueConstraintViolation marks a SQLite UNIQUE-constraint failure
// on (job.type, job.correlation_id) that the Enqueue() rescue path
// attempts to surface as "return existing job" idempotency response.
// Set when the typed sqlite3.Error probe (Code == ErrConstraintUnique)
// fires on the Create() error. Wrap with %w so callers can errors.Is
// the sentinel.
//
// Errors.Is(err, ErrUniqueConstraintViolation) is the canonical probe.
//
// godlike/07 NO-FAKE-AVAILABILITY (string-trap rationale): the pre-PR
// strings.Contains(err.Error(), "UNIQUE constraint") heuristic was
// string-compare-fragile — a future SQLite/mattn driver version
// changing the error string format (uppercasing, locale, prefix
// restructuring) would silently disable the rescue path without any
// test surfacing the regression. The typed probe is invariant against
// driver string changes — `sqlite3.Error.Code` is an int-backed enum,
// not a string.
var ErrUniqueConstraintViolation = errors.New("appjobs.Service.Enqueue: SQLite UNIQUE constraint violation (typed probe via sqlite3.Error.Code()==SQLITE_CONSTRAINT_UNIQUE)")

// ErrNoHandlerForJobType is returned by Service.Enqueue when a job is
// submitted for a type that has no handler registered in the dispatcher.
// The gate prevents silent queue buildup: without this check, jobs for
// unregistered types would be accepted into the queue and sit there
// forever (no handler = no consumer). The gate only fires when the
// dispatcher is wired (non-nil); nil-dispatcher compositions (tests,
// minimal deployments) let the enqueue proceed to avoid breaking
// idempotency-key / correlation-id flows that don't involve handlers.
//
// godlike/07 NO-FAKE-AVAILABILITY: the pre-PR enqueue_service.go
// accepted ANY job type without checking handler presence. Operators
// could enqueue 1000 jobs for "typoed.job.type", receive HTTP 200,
// and never see a consumer pick them up — the only diagnostic was
// the /ready handlers check (which only covers 2 registered types).
// This sentinel closes that gap: the typed error surfaces immediately
// at enqueue time, so callers see "no handler for this type" inline.
//
// Errors.Is(err, ErrNoHandlerForJobType) is the canonical probe.
var ErrNoHandlerForJobType = errors.New("appjobs.Service.Enqueue: no handler registered for the requested job type (the job would never be consumed; register a handler first or correct the job type)")

// ErrJobsSvcRequiredAtRegistration marks a nil jobs.Service detected at
// handler-registration time (e.g. by NonOpsHandler.RegisterJobHandlers).
// Symmetric with ErrMissingDeps's composition-root contract but scoped to
// the registration surface specifically (the registration step is where
// no-handler-registered symptoms surface first).
//
// godlike/07 NO-FAKE-AVAILABILITY rationale: the pre-PR nonops path silently
// returned nil from RegisterJobHandlers when JobsSvc was nil; an operator
// wiring bulkUploadWorker WITHOUT wiring JobsSvc would see a successful
// boot and a runtime "no handler registered for bulk_upload_youtube_clips"
// at first enqueue, with no diagnostic pointing at the missing dep.
// The typed sentinel surfaces the composition bug immediately at the
// Registration step so the chain
//
//	ClipsDescriptor.RegisterJobHandlers
//
// → Handler.RegisterJobHandlers
// → NonOpsHandler.RegisterJobHandlers
//
// fails closed at boot (or any first-register invocation) rather than
// silently accepting a partial wiring. Production composition roots MUST
// use NewNonOpsHandlerStrict to reject nil JobsSvc / nil BulkUploadWorker
// at construction time; this sentinel fires only when the strict guard
// was bypassed via the legacy nil-tolerant NewNonOpsHandler.
//
// Errors.Is(err, ErrJobsSvcRequiredAtRegistration) is the canonical probe.
var ErrJobsSvcRequiredAtRegistration = errors.New("appjobs: JobsSvc is required at handler-registration time (composition bug; NewNonOpsHandlerStrict must reject nil JobsSvc at construction; the legacy NewNonOpsHandler is nil-tolerant for test fixtures only)")

// ErrMissingDeps marks a nil dependency detected at registration time
// (typically RegisterHandler / RegisterJobHandler). Surfaced as a typed
// sentinel so callers (composition root, batch wiring, clipindexer
// batch reindex, generate-item handler) can branch reactively without
// string-compare gymnastics. Symmetric with ErrRepoRequired +
// ErrLogRequired's fail-closed contract.
//
// Carry-forward attribution note (verified via git pickaxe 2026-07-04):
// `ErrMissingDeps` was NEVER previously defined — neither the original
// `errors.go` (pre-`faa2a55a`) nor `faa2a55a`'s PR-jobs-retry-contract
// (which replaced the file wholesale with 5 sentinels: ErrRegistryRequired,
// ErrRepoRequired, ErrLogRequired, ErrMaxRetriesUnknown,
// ErrUniqueConstraintViolation) declared a sentinel of this name.
// This declaration is the FIRST introduction on the canonical SHA
// `d6767631` (PR-ERROR-SURFACING commit-8 / refactor(app): narrow
// assetindex import path).
//
// Pre-commit-8 Build failure: ~10 callsites
// (`internal/infrastructure/indexing/clipindexer/batch.go:113`,
// `register_wiring_test.go`, `generate_item_handler.go`,
// `clipindexer_enqueue.go` + others) referenced `appjobs.ErrMissingDeps`
// but the symbol did not exist in the package; `go build` rejected
// with `undefined: appjobs.ErrMissingDeps`. Commit-8 ships this sentinel
// to satisfy godlike/07 typed-error contract (callsites by IDENTITY via
// `errors.Is`, not by string).
//
// Errors.Is(err, ErrMissingDeps) is the canonical probe.
//
// godlike/07 NO-FAKE-AVAILABILITY rationale: the surface was previously a
// raw `fmt.Errorf("...: jobsSvc is nil")` which mixed component-namespace
// context with dependency nil — a future cue "register before nil" would
// silently miscategorise on a future refactor. The typed sentinel
// preserves the canonical contract for `errors.Is` across all callers.
var ErrMissingDeps = errors.New("appjobs: required dependency is nil (composition root must wire the dependency before calling Register)")
