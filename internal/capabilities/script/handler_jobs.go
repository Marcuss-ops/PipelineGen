// Package script — handler_jobs.go owns the canonical script-side
// job-observation surface (RegisterJobRoutes + GetJobStatus +
// GetFullJobRun) per architecture/current.yaml#SCRIPT-FLOW-SPLIT
// .linked_issues[PR-SCRIPT-JOBS-EXTRACT].
//
// FASE 2 (July 2026): the pre-FASE-2 EnqueueEnvelope entrypoint that
// delegated to the package-level enqueueEnvelopeFn is REMOVED. POST
// /api/script/generate now reaches the canonical operations
// GenerationSubmissionService directly through HandlerGenerate
// (handler_generate_handler.go). The legacy adapter routes that
// invoked enqueueEnvelopeFn were never mounted (RegisterJobRoutes
// only mounts GET /api/script/jobs/:id per SSOT — see
// architecture/ownership/modules.yaml:WAVE-22-C2-E — so the
// removal is dead-code, no active call sites remain.
//
// P1 verdetto (July 2026): the enriched /jobs/:id/full endpoint was
// initially added to RegisterJobRoutes; per WAVE-22-C2-E SSOT
// alignment (commit 6ec3e95b6 + the strengthened route test in
// handler_test.go), the /jobs/:id/full mount was RETIRED from this
// ScriptFlow surface because it duplicates the canonical
// /api/jobs/:id/full endpoint owned by the Jobs module
// (godlike/06 SSOT: one canonical owner per fact). GetFullJobRun
// + the runRepo field are RETAINED as reference implementations —
// see handler_run_full.go doc on GetFullJobRun for the unmount
// audit narrative and re-mount conditions.
//
// Pattern 5 (AGENTS.md): one capability per file, one struct per
// capability. JobsHandler replaces the 2 methods that previously
// lived co-located with the orchestrator (handler_flow.go::registerJobRoutes,
// handler_flow.go::GetJobStatus).
package script

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// JobsHandler owns the canonical ScriptFlow-side job-observation
// surface:
//
//   - RegisterJobRoutes: mounts GET /api/script/jobs/:id under
//     RequireAdminToken(auth). The enriched /api/script/jobs/:id/full
//     endpoint is NOT mounted here per WAVE-22-C2-E SSOT — its
//     canonical owner is the Jobs module under /api/jobs/:id/full
//     (godlike/06: one canonical owner per fact; the duplicate
//     mount was retired by the strengthened route test in
//     handler_test.go).
//   - GetJobStatus: canonical handler for GET /api/script/jobs/:id.
//
// It is a separate type from ScriptFlowHandler per AGENTS.md Pattern 5:
// one capability per file, one struct per capability. GetFullJobRun
// (defined in handler_run_full.go) remains as a REFERENCE-only
// implementation retained for future re-mount if the SSOT policy
// changes — see its in-file doc for the unmount audit narrative.
type JobsHandler struct {
	jobsSvc job.Service
	runRepo scriptgen.RunRepository // optional, used by GetFullJobRun
	log     *zap.Logger
}

// NewJobsHandler constructs the canonical JobsHandler.
// runRepo is optional — when nil, GetFullJobRun returns basic
// job info without enriched generation-run data.
func NewJobsHandler(jobsSvc job.Service, runRepo scriptgen.RunRepository, log *zap.Logger) *JobsHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &JobsHandler{
		jobsSvc: jobsSvc,
		runRepo: runRepo,
		log:     log,
	}
}

// RegisterJobRoutes mounts the canonical ScriptFlow-side job-status
// route:
//
//   - GET /api/script/jobs/:id — basic job status (canonical mount
//     under the ScriptFlow module per architecture/ownership/
//     modules.yaml:WAVE-22-C2-E route list).
//
// The enriched /api/script/jobs/:id/full endpoint is INTENTIONALLY
// NOT mounted here: its canonical owner is the Jobs module under
// /api/jobs/:id/full (see internal/api/jobs/impl.go::GetFull).
// Mounting both copies would violate godlike/06 SSOT (one canonical
// owner per fact) — the strengthened route test in handler_test.go
// guards against accidental re-introduction.
//
// `auth` is the AdminTokenProvider (godlike/07 minimum-blast-radius
// — interface stays in package script per middleware_auth.go header).
// ScriptFlowHandler passes itself (`h`) since it carries the wired
// adminToken; the route lives here so the auth contract is preserved
// without coupling JobsHandler to an adminToken field.
func (jh *JobsHandler) RegisterJobRoutes(r *gin.RouterGroup, auth AdminTokenProvider) {
	jobs := r.Group("")
	jobs.Use(RequireAdminToken(auth))
	jobs.GET("/jobs/:id", jh.GetJobStatus)
}

// compile-time guard
var _ job.Service = job.Service(nil)

// GetJobStatus is the canonical handler for GET /api/script/jobs/:id.
// Returns the current job snapshot (status + progress + error + result).
func (jh *JobsHandler) GetJobStatus(c *gin.Context) {
	if jh.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}
	jobID := strings.TrimSpace(c.Param("id"))
	if jobID == "" {
		apiutil.BadRequest(c, "job id is required")
		return
	}
	job, err := jh.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		apiutil.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"job_id":   job.ID,
		"status":   job.Status,
		"progress": job.Progress,
		"error":    job.Error,
		"result":   job.Result,
	})
}
