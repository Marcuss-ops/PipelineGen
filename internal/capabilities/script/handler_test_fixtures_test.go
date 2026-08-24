// Package script — handler_test_fixtures_test.go: shared test infrastructure.
//
// 2026-07-06 (Phase 2 decomposition): extracted from handler_test.go per
// the god-object decomposition plan. Zero behavior changes. Same-package
// visibility preserves all caller paths.
//
// Contains:
//   - fakeJobsService (testing double for job.Service)
//   - stubJobStatsReader (testing stub)
//   - newTestJobsService, newMinimalScriptFlowDepsForTest (constructors)
package script

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	sqliteops "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
)

func init() { gin.SetMode(gin.TestMode) }

// fakeJobsService is a testing double for job.Service. Only Enqueue is
// functional (records lastReq and returns a canned Job so async-enqueue
// HTTP paths return 200); the remaining methods return errors so the
// handler's nil/503 short-circuits are exercised without touching real
// infrastructure.
//
// P0 idempotency: Enqueue also mimics the real job broker's
// FindActiveByKey dedup by returning the same job.ID for repeated
// calls with the same non-empty ActiveKey.
type fakeJobsService struct {
	mu           sync.Mutex
	lastReq      *job.EnqueueRequest
	nextJobID    string
	enqueueCount int
	activeKeys   map[string]string
}

// fakeSubmissionService is the test double for the canonical
// application-layer submission service. It records the last request
// and reproduces the idempotency / force-refresh semantics used by the
// HTTP handler tests.
type fakeSubmissionService struct {
	mu           sync.Mutex
	lastReq      *opsapp.SubmitRequest
	submitCount  int
	nextJobIndex int
	records      map[string]*opsapp.SubmitResult
}

var _ interface {
	Submit(context.Context, opsapp.SubmitRequest) (*opsapp.SubmitResult, error)
} = (*fakeSubmissionService)(nil)

func (f *fakeSubmissionService) Submit(ctx context.Context, req opsapp.SubmitRequest) (*opsapp.SubmitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	copyReq := req
	f.lastReq = &copyReq
	f.submitCount++

	if f.records == nil {
		f.records = make(map[string]*opsapp.SubmitResult)
	}
	key := string(req.Scope) + "|" + req.IdempotencyKey
	if existing, ok := f.records[key]; ok {
		if !req.ForceRefresh {
			if existing.Operation != nil && existing.Operation.RequestHash == req.RequestHash {
				return &opsapp.SubmitResult{Operation: existing.Operation, IsIdempotencyHit: true}, nil
			}
			return nil, domainops.ErrIdempotencyConflict
		}
	}

	f.nextJobIndex++
	jobID := req.JobID
	if jobID == "" {
		jobID = fmt.Sprintf("job-fake-%d", f.nextJobIndex)
	}
	opID := req.OperationID
	if opID == "" {
		opID = fmt.Sprintf("op-fake-%d", f.nextJobIndex)
	}
	op := &domainops.Operation{
		OperationID:           opID,
		Scope:                 req.Scope,
		IdempotencyKey:        req.IdempotencyKey,
		RequestHash:           req.RequestHash,
		JobID:                 jobID,
		State:                 domainops.StateQueued,
		CreatedAt:             time.Unix(0, int64(f.nextJobIndex)),
		UpdatedAt:             time.Unix(0, int64(f.nextJobIndex)),
		SupersedesOperationID: "",
	}
	// FASE 2 close-out: stub the canonical Job so the handler's
	// canonical-status branch (handler_generate_handler.go)
	// reads a non-nil job.Status on both replay and fresh-
	// submit. The fake's canonical contract is "always QUEUED"
	// — the canonical-state-on-replay contract is locked by
	// generation_submission_service_test.go (which uses a
	// real jobs.SQLiteStore-backed JobGetter and observes a
	// worker-style UPDATE).
	fakeJob := &job.Job{
		ID:     jobID,
		Type:   req.JobType,
		Status: job.StatusQueued,
	}
	res := &opsapp.SubmitResult{
		Operation:   op,
		Job:         fakeJob,
		IsSupersede: req.ForceRefresh && existingResultHasOperation(f.records[key]),
	}
	if req.ForceRefresh {
		if prior := f.records[key]; prior != nil && prior.Operation != nil {
			op.SupersedesOperationID = prior.Operation.OperationID
			res.IsSupersede = true
		}
	}
	f.records[key] = res
	return res, nil
}

func existingResultHasOperation(res *opsapp.SubmitResult) bool {
	return res != nil && res.Operation != nil
}

// Compile-time assertion: fakeJobsService satisfies job.Service.
var _ job.Service = (*fakeJobsService)(nil)

func (f *fakeJobsService) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastReq = req
	f.enqueueCount++

	// P0 idempotency: mimic FindActiveByKey dedup for non-empty ActiveKey.
	if req.ActiveKey != "" {
		if f.activeKeys == nil {
			f.activeKeys = make(map[string]string)
		}
		if existingID, ok := f.activeKeys[req.ActiveKey]; ok {
			return &job.Job{ID: existingID, Status: job.StatusQueued, Type: req.Type}, nil
		}
	}

	if f.nextJobID == "" {
		f.nextJobID = "job-123"
	}
	id := f.nextJobID
	if req.ActiveKey != "" {
		f.activeKeys[req.ActiveKey] = id
	}
	return &job.Job{ID: id, Status: job.StatusQueued, Type: req.Type}, nil
}

func (f *fakeJobsService) Get(ctx context.Context, id string) (*job.Job, error) {
	return nil, errors.New("fakeJobsService: Get not implemented")
}

func (f *fakeJobsService) Cancel(ctx context.Context, id string) error {
	return errors.New("fakeJobsService: Cancel not implemented")
}

func (f *fakeJobsService) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	return nil, errors.New("fakeJobsService: List not implemented")
}

func (f *fakeJobsService) IsTerminal(status job.Status) bool {
	return status.IsTerminal()
}

func (f *fakeJobsService) RegisterHandler(jobType string, handler any) error {
	return errors.New("fakeJobsService: RegisterHandler not implemented")
}

func (f *fakeJobsService) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	return nil, errors.New("fakeJobsService: ListEvents not implemented")
}

func (f *fakeJobsService) Retry(ctx context.Context, id string) (*job.Job, error) {
	return nil, errors.New("fakeJobsService: Retry not implemented")
}

// newTestJobsService returns the same struct value as both job.Service
// (the iface placeholder for ScriptFlowDeps.Jobs) and *fakeJobsService
// (for tests that need to introspect the captured lastReq after Enqueue).
func newTestJobsService(t *testing.T) (job.Service, *fakeJobsService) {
	t.Helper()
	fake := &fakeJobsService{}
	return fake, fake
}

// newMinimalScriptFlowDepsForTest returns the canonical minimal
// ScriptFlowDeps for unit tests (PR-script-deps-slim, July 2026):
// the slim bag with Jobs + Generate populated. Generate is wired to
// a fake submission service that reproduces the canonical submission
// semantics (fresh submit, replay, conflict, force-refresh) without
// touching SQLite.
//
// godlike/07 NO-FAKE-AVAILABILITY: this is the canonical test
// fixture; weakening the preflight gate by zero-value caps would
// silently accept voiceover/document requests on a deployment that
// hasn't wired the corresponding services. The 3-bool true
// surface HERE means "the test deployment has all 3 services
// wired" — production wiring is the SOLE owner of the real
// composition-time contract (see internal/app/wire_script.go).
//
// FASE 2 (July 2026): the fixture now constructs a real
// `*opsapp.Service` backed by an in-memory SQLite + the 3
// canonical FASE 2 schemas (operations, jobs, outbox_events).
// This lets handler-level tests exercise the full
// Operations.Submit path (atomic-TX commit, idempotency hit,
// force_refresh supersede) without pulling the full
// composition root. Tests that want a nil operations service
// (to verify the 503 "operations service not initialised"
// fail-closed path) set `Generate.Operations` to nil after
// calling the fixture.
//
// Other fields (Legacy, ClipsSearcher, AdminToken) default to zero
// values — tests that need a populated dep supply it explicitly.
func newMinimalScriptFlowDepsForTest(jobs job.Service) (ScriptFlowDeps, *fakeSubmissionService) {
	submitter := &fakeSubmissionService{}
	deps := ScriptFlowDeps{
		Jobs: JobsDeps{Jobs: jobs},
		Generate: GenerateDeps{
			Submission: submitter,
			Log:        zap.NewNop(),
			// PR-SUBMISSION-FACTORY (July 2026): the factory builds the
			// SubmitRequest from the bound command.
			Factory: submission.NewSubmitRequestFactory(),
			// PR-COMMIT3 (July 2026): the legacy `Caps PreflightCaps`
			// field is physically removed alongside the preflight
			// module. Tests now exercise the canonical envelope path
			// directly (the bindGenerateEnvelope DisallowUnknownFields
			// check rejects deleted-flag callers with HTTP 400).
			Validator: usecase.NewDefaultPayloadValidator(),
		},
	}
	return deps, submitter
}

// newFASE2OperationsServiceForTest constructs a real
// `*opsapp.Service` backed by an in-memory SQLite + the 3
// canonical FASE 2 schemas. The test fixture is the canonical
// SOLE owner of the in-memory test DB lifecycle (the helper
// uses t.Cleanup to close the DB at test exit).
//
// godlike/06 SSOT: the 3 schemas are the SOLE canonical shapes
// the Service touches. Drift between this schema and the
// production migrations would surface as SQL errors at INSERT
// time (NOT a silent mismatch).
func newFASE2OperationsServiceForTest() *opsapp.Service {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		panic("newFASE2OperationsServiceForTest: open in-memory SQLite: " + err.Error())
	}
	if _, err := db.Exec(schemasFASE2ForTest); err != nil {
		panic("newFASE2OperationsServiceForTest: apply FASE 2 schemas: " + err.Error())
	}
	opsRepo := sqliteops.NewSQLiteRepository(db)
	jobsStore := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
	outboxRepo := outboxevents.NewRepository(db)
	txMgr := &dbTxManagerForTest{db: db}
	// FASE 2 close-out: jobsStore satisfies the JobGetter port
	// natively (its Get(ctx, id) method matches the port shape).
	// Wired twice — once as JobEnqueuer (CreateInTx use) and
	// once as JobGetter (canonical-state-on-replay read).
	return opsapp.NewService(opsRepo, jobsStore, jobsStore, outboxRepo, txMgr, zap.NewNop())
}

// dbTxManagerForTest wraps *sql.DB to satisfy the operations.TxManager
// port. Mirrors the production sqlTxManager in
// internal/app/build_bundles_operations.go but is inlined here to
// avoid the test package importing the production composition root.
type dbTxManagerForTest struct {
	db *sql.DB
}

func (m *dbTxManagerForTest) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return m.db.BeginTx(ctx, nil)
}

// schemasFASE2ForTest is the inline mirror of migrations/sqlite/092
// (outbox_events), 145 (operations), and the canonical jobs
// table from `internal/platform/sqlite/jobs`. Kept
// in lockstep with the production migrations.
const schemasFASE2ForTest = `
CREATE TABLE operations (
    operation_id            TEXT PRIMARY KEY,
    scope                   TEXT NOT NULL,
    idempotency_key         TEXT NOT NULL,
    request_hash            TEXT NOT NULL,
    job_id                  TEXT NOT NULL,
    state                   TEXT NOT NULL,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    supersedes_operation_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_operations_idem_lookup
    ON operations(scope, idempotency_key, created_at DESC);

CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'QUEUED',
    priority INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    video_name TEXT NOT NULL DEFAULT '',
    active_key TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    progress INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 0,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    cancelled_at TEXT,
    revision INTEGER NOT NULL DEFAULT 1,
    parent_state_typed TEXT NOT NULL DEFAULT ''
);

CREATE TABLE outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX ux_outbox_events_event_key
    ON outbox_events(event_key);
`

// stubJobStatsReader satisfies appjobs.JobStatsReader with a
// no-op GetStats. Used in test wiring where the stats reader
// is required by the JobsHandler ctor signature (Issue 9 / P2
// delegator pattern, June 2026) but the test only exercises
// the Get/Events paths that don't touch stats. Production
// wiring passes *appjobs.Service which satisfies BOTH
// job.Service AND JobStatsReader structurally (see
// internal/app/registry_public_modules.go::registerJobs).
// Tests use this typed stub to avoid pulling a real SQLite
// stats reader into the script package (which would otherwise
// require a layered infrastructure import).
type stubJobStatsReader struct{}

// GetStats returns (nil, nil) so the JobsHandler ctor
// accepts the stub. The Stats endpoint is the only consumer
// of GetStats; tests that exercise the Stats endpoint pass
// a real *appjobs.Service and bypass this stub.
func (stubJobStatsReader) GetStats(_ context.Context) (*sqljobs.JobStats, error) {
	return nil, nil
}
