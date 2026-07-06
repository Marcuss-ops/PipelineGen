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
	"testing"

	"github.com/gin-gonic/gin"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
)

func init() { gin.SetMode(gin.TestMode) }

// fakeJobsService is a testing double for job.Service. Only Enqueue is
// functional (records lastReq and returns a canned Job so async-enqueue
// HTTP paths return 200); the remaining methods return errors so the
// handler's nil/503 short-circuits are exercised without touching real
// infrastructure.
type fakeJobsService struct {
	lastReq   *job.EnqueueRequest
	nextJobID string
}

// Compile-time assertion: fakeJobsService satisfies job.Service.
var _ job.Service = (*fakeJobsService)(nil)

func (f *fakeJobsService) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	f.lastReq = req
	if f.nextJobID == "" {
		f.nextJobID = "job-123"
	}
	return &job.Job{ID: f.nextJobID, Status: job.StatusQueued, Type: req.Type}, nil
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
// the slim 5-field bag with only Jobs populated. Other fields
// (Generate, Legacy, ClipsSearcher, AdminToken) default to zero
// values — tests that need a populated dep supply it explicitly.
func newMinimalScriptFlowDepsForTest(jobs job.Service) ScriptFlowDeps {
	return ScriptFlowDeps{
		Jobs: JobsDeps{Jobs: jobs},
	}
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
