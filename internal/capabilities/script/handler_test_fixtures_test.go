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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
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
			return nil, opsapp.ErrIdempotencyConflict
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
	op := &opsapp.Operation{
		OperationID:           opID,
		Scope:                 req.Scope,
		IdempotencyKey:        req.IdempotencyKey,
		RequestHash:           req.RequestHash,
		JobID:                 jobID,
		State:                 opsapp.StateQueued,
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
			Validator: gencore.NewDefaultPayloadValidator(),
		},
	}
	return deps, submitter
}
