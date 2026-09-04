// Package script — handler_jobs.go owns the canonical script-side
// job-observation surface (RegisterJobRoutes + GetJobStatus) per
// architecture/current.yaml#SCRIPT-FLOW-SPLIT
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
// The enriched /api/script/jobs/:id/full endpoint was retired from
// ScriptFlow because it duplicated the canonical /api/jobs/:id/full
// endpoint owned by the Jobs module. The former reference-only
// GetFullJobRun implementation and its RunRepository dependency were
// removed once the duplicate route had no runtime consumer.
//
// Pattern 5 (AGENTS.md): one capability per file, one struct per
// capability. JobsHandler replaces the methods that previously lived
// co-located with the orchestrator.
package script

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// JobsHandler owns the canonical ScriptFlow-side job-observation surface:
//
//   - RegisterJobRoutes: mounts GET /api/script/jobs/:id under
//     RequireAdminToken(auth).
//   - GetJobStatus: canonical handler for GET /api/script/jobs/:id.
//
// The enriched /api/script/jobs/:id/full endpoint is intentionally absent;
// its canonical owner is the Jobs module under /api/jobs/:id/full.
type JobsHandler struct {
	jobsSvc job.Service
	log     *zap.Logger
}

// NewJobsHandler constructs the canonical JobsHandler.
func NewJobsHandler(jobsSvc job.Service, log *zap.Logger) *JobsHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &JobsHandler{
		jobsSvc: jobsSvc,
		log:     log,
	}
}

// RegisterJobRoutes mounts the canonical ScriptFlow-side job-status route:
//
//   - GET /api/script/jobs/:id — basic job status (canonical mount
//     under the ScriptFlow module per architecture/ownership/
//     modules.yaml:WAVE-22-C2-E route list).
//
// The enriched /api/script/jobs/:id/full endpoint is intentionally not
// mounted here: its canonical owner is the Jobs module under
// /api/jobs/:id/full (see internal/capabilities/jobs/impl.go::GetFull).
// Mounting both copies would violate godlike/06 SSOT.
//
// `auth` is the AdminTokenProvider. ScriptFlowHandler passes itself (`h`)
// since it carries the wired adminToken.
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
