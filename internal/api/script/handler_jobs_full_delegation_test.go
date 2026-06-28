// Package script — handler_jobs_full_delegation_test.go pins the
// Issue 9 / P2 (June 2026) refactor that collapses
// /api/script/jobs/:job_id/full into the canonical Jobs module
// handler via a narrow port delegator.
//
// What this test exercises:
//
//  1. The script route `/api/script/jobs/:job_id/full` MUST
//     delegate (not duplicate) the JobsHandler.GetFull logic.
//     The pre-Issue-9 script handler had its own
//     GetJobFullStatus body that produced a different response
//     shape (with `job_id`, `priority`, `retry_count`,
//     `correlation_id`, `created_at`, `started_at`,
//     `completed_at`, `updated_at` instead of the canonical
//     `id` + `current_step` + `retryable` + `job` shape).
//
//  2. After the collapse, the two routes MUST return the
//     SAME body — the delegator contract.
//
//  3. The delegator preserves the admin-token gate (the
//     script route group runs `RequireAdminToken(h)` before
//     the handler call); the test exercises the no-token
//     path (adminToken field empty → EnableAuth=false → gate
//     bypassed), matching the other handler tests in
//     handler_test.go.

package script

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Test-double service ──────────────────────────────────────

// delegatorFakeService is a minimal domainjob.Service test
// double that returns a single canned Job + empty event list
// from Get / ListEvents. Other methods (Enqueue, Cancel,
// etc.) are inherited from the embedded *fakeJobsService
// from handler_test.go — those return errors which is the
// expected behaviour for a Get/ListEvents-only test
// surface. The service also satisfies appjobs.JobStatsReader
// structurally via the embedded fakeJobsService (it does
// NOT implement GetStats; the test uses a real *appjobs
// service for the stats reader — see TestRegisterJobs_* for
// the JobStatsReader wiring pattern). The JobsHandler ctor
// takes (service, stats, log); here we pass `svc` for both
// service + stats — the JobsHandler tolerates a non-Stats
// reader because the GetFull path doesn't call GetStats.
type delegatorFakeService struct {
	*fakeJobsService
	cannedJob *jobservice.Job
}

// Compile-time assertion: delegatorFakeService satisfies
// job.Service (the iface used by JobsHandler ctor + the
// script handler deps).
var _ jobservice.Service = (*delegatorFakeService)(nil)

// Get returns the canned job when id is non-empty;
// the empty-id path returns an error matching the
// pre-Issue-9 fakeJobsService contract.
func (d *delegatorFakeService) Get(_ context.Context, id string) (*jobservice.Job, error) {
	if id == "" {
		return nil, errors.New("delegatorFakeService: empty id")
	}
	if d.cannedJob == nil {
		return nil, errors.New("delegatorFakeService: no canned job configured")
	}
	return d.cannedJob, nil
}

// ListEvents returns an empty (non-nil) event slice —
// JobsHandler.GetFull treats events != nil as the happy
// path (events == nil would also work because the
// response shape is "events: []" either way, but an
// empty slice keeps the JSON encoder output stable).
func (d *delegatorFakeService) ListEvents(_ context.Context, _ string) ([]jobservice.Event, error) {
	return []jobservice.Event{}, nil
}

// ── Test ──────────────────────────────────────────────────────

// TestScriptJobsFullEndpoint_DelegatesToJobsFull pins the
// Issue 9 / P2 (June 2026) delegator contract:
//
//	GET /api/script/jobs/:job_id/full  ==
//	GET /api/jobs/:job_id/full
//
// (byte-equal response bodies). The two routes are mounted
// on the same test server with the SAME backing service;
// the assertion is the strongest possible pin — if the
// script handler ever reverts to its own response shape
// (or returns 503 / 404 / anything other than the canonical
// JobsHandler.GetFull output), this test fails loudly.
func TestScriptJobsFullEndpoint_DelegatesToJobsFull(t *testing.T) {
	t.Parallel()

	// ── Arrange: minimal canned job ────────────────────────────
	canned := &jobservice.Job{
		ID:        "job-delegator-test",
		Type:      "script.generate",
		Status:    jobservice.StatusSucceeded,
		Progress:  100,
		Result:    json.RawMessage(`{"ok":true,"items":[]}`),
		CreatedAt: time.Now().Add(-2 * time.Second),
		UpdatedAt: time.Now(),
	}

	// ── Arrange: one fake service, shared by both routes ─────
	_, base := newTestJobsService(t)
	svc := &delegatorFakeService{
		fakeJobsService: base,
		cannedJob:       canned,
	}

	// ── Arrange: JobsHandler (canonical) + ScriptFlowHandler (delegator) ──
	log := zap.NewNop()
	jobsHandler := jobsapi.NewJobsHandler(svc, svc, log)
	handler := NewScriptFlowHandler(ScriptFlowDeps{
		Jobs:          svc,
		JobFullStatus: jobsHandler,
		Log:           log,
	})

	// ── Arrange: mount BOTH routes on the same test server ───
	router := gin.New()
	scriptRg := router.Group("/api/script")
	handler.RegisterRoutes(scriptRg)
	jobsRg := router.Group("/api/jobs")
	jobsHandler.RegisterRoutes(jobsRg)

	server := httptest.NewServer(router)
	defer server.Close()

	// ── Act 1: GET /api/script/jobs/job-delegator-test/full ──
	scriptResp, err := http.Get(server.URL + "/api/script/jobs/job-delegator-test/full")
	require.NoError(t, err)
	defer scriptResp.Body.Close()
	require.Equal(t, http.StatusOK, scriptResp.StatusCode)
	scriptBody, err := io.ReadAll(scriptResp.Body)
	require.NoError(t, err)

	// ── Act 2: GET /api/jobs/job-delegator-test/full ─────────
	jobsResp, err := http.Get(server.URL + "/api/jobs/job-delegator-test/full")
	require.NoError(t, err)
	defer jobsResp.Body.Close()
	require.Equal(t, http.StatusOK, jobsResp.StatusCode)
	jobsBody, err := io.ReadAll(jobsResp.Body)
	require.NoError(t, err)

	// ── Assert 1: byte-equality (delegator contract) ────────
	// The strongest possible pin: the script route's body
	// MUST be byte-equal to the canonical JobsHandler.GetFull
	// body for the same id. Pre-Issue-9 this assertion fails
	// because the script shape (job_id / priority /
	// retry_count / ...) differs from the jobs shape (id /
	// current_step / retryable / job).
	assert.Equal(t, string(jobsBody), string(scriptBody),
		"Issue 9 / P2: /api/script/jobs/:job_id/full MUST delegate to JobsHandler.GetFull — response bodies must be byte-equal")

	// ── Assert 2: canonical JobsHandler shape fields ─────────
	// Spot-check the canonical fields so a future refactor
	// that breaks the JobsHandler.GetFull shape itself also
	// surfaces (the byte-equality above would silently pass
	// if BOTH routes regressed to the same wrong shape).
	var shell struct {
		OK     bool            `json:"ok"`
		ID     string          `json:"id"`
		Type   string          `json:"type"`
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(scriptBody, &shell))
	assert.True(t, shell.OK, "JobsHandler.GetFull must return ok=true")
	assert.Equal(t, "job-delegator-test", shell.ID, "canonical id field (NOT job_id)")
	assert.Equal(t, "script.generate", shell.Type)
	assert.Equal(t, "SUCCEEDED", shell.Status, "StatusSucceeded verbatim from job.Status")
	assert.JSONEq(t, `{"ok":true,"items":[]}`, string(shell.Result), "result payload round-trips through JobsHandler.GetFull")
}
