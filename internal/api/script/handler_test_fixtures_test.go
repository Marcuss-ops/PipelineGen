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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
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
	lastReq      *job.EnqueueRequest
	nextJobID    string
	enqueueCount int
	activeKeys   map[string]string
}

// Compile-time assertion: fakeJobsService satisfies job.Service.
var _ job.Service = (*fakeJobsService)(nil)

func (f *fakeJobsService) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
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
// the slim 5-field bag with Jobs + Generate populated. Generate is
// populated to the all-caps-enabled conservative default so the
// SCRIPTCONTRACT-2026-07-08 PR-2 preflight gate (handler_enqueue.go
// ::enqueueEnvelopeFn) short-circuits to the enqueue path instead
// of 503-fail-closing on any user envelope that doesn't request
// voiceover/document/images (the canonical 99% of test envelopes).
//
// godlike/07 NO-FAKE-AVAILABILITY: this is the canonical test
// fixture; weakening the preflight gate by zero-value caps would
// silently accept voiceover/document requests on a deployment that
// hasn't wired the corresponding services. The 3-bool true
// surface HERE means "the test deployment has all 3 services
// wired" — production wiring is the SOLE owner of the real
// composition-time contract (see internal/app/wire_script.go).
//
// Other fields (Legacy, ClipsSearcher, AdminToken) default to zero
// values — tests that need a populated dep supply it explicitly.
func newMinimalScriptFlowDepsForTest(jobs job.Service) ScriptFlowDeps {
	return ScriptFlowDeps{
		Jobs: JobsDeps{Jobs: jobs},
		Generate: GenerateDeps{
			Jobs: jobs,
			Log:  zap.NewNop(),
			// SCRIPTCONTRACT-2026-07-08 PR-2: zero-value caps would
			// make the preflight gate 503 on any user envelope that
			// requests voiceover/document/images. The canonical test
			// fixture wires all 3 caps = true so the preflight gate
			// passes for the canonical minimal-envelope test path
			// (e.g. handler_idempotency_test.go uses no output
			// flags, so the gate is a no-op; but other tests in the
			// package may exercise the same fixture with explicit
			// output flags).
			Caps: PreflightCaps{
				VoiceoverEnabled: true,
				ImagesEnabled:    true,
				DocumentEnabled:  true,
			},
			Validator: usecase.NewDefaultPayloadValidator(),
			Store:     newInMemoryIdempotencyStore(),
		},
	}
}

// inMemoryIdempotencyStore is a minimal in-memory implementation of
// middleware.IdempotencyStore for handler-level tests.
type inMemoryIdempotencyStore struct {
	mu      sync.RWMutex
	records map[string]*mw.IdempotencyRecord
}

func newInMemoryIdempotencyStore() mw.IdempotencyStore {
	return &inMemoryIdempotencyStore{records: make(map[string]*mw.IdempotencyRecord)}
}

func (s *inMemoryIdempotencyStore) TryInsert(ctx context.Context, key, bodyHash string) (*mw.IdempotencyRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[key]; ok {
		return nil, true, nil
	}
	rec := &mw.IdempotencyRecord{
		Key:       key,
		BodyHash:  bodyHash,
		Status:    "in_flight",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	s.records[key] = rec
	return rec, false, nil
}

func (s *inMemoryIdempotencyStore) Complete(ctx context.Context, key string, responseStatus int, responseBody []byte, responseContentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key]
	if !ok {
		return mw.ErrIdempotencyKeyNotFound
	}
	rec.Status = "completed"
	rec.ResponseStatus = responseStatus
	rec.ResponseBody = responseBody
	rec.ResponseCT = responseContentType
	return nil
}

func (s *inMemoryIdempotencyStore) Get(ctx context.Context, key string) (*mw.IdempotencyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[key]
	if !ok {
		return nil, mw.ErrIdempotencyKeyNotFound
	}
	return rec, nil
}

func (s *inMemoryIdempotencyStore) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	return 0, nil
}

// stubJobStatsReader satisfies appjobs.JobStatsReader with a
// no-op GetStats. Used in test wiring where the stats reader
// is required by the JobsHandler ctor signature (Issue 9 / P2
// delegator pattern, June 2026) but the test only exercises
// the Get/Events paths that don't touch stats. Production
// wiring passes *appjobs.Service which satisfies BOTH
// domainjob.Service AND JobStatsReader structurally (see
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
