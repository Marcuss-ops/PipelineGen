// Package script — handler_jobs.go extracts the job-observation
// cluster (RegisterJobRoutes + EnqueueEnvelope + GetJobStatus) from
// ScriptFlowHandler (22-field God Object) into a dedicated JobsHandler
// per architecture/current.yaml#SCRIPT-FLOW-SPLIT.linked_issues[PR-SCRIPT-JOBS-EXTRACT].
//
// Pattern 5 (AGENTS.md): one capability per file, one struct per
// capability. JobsHandler replaces 3 methods that previously lived
// co-located with the orchestrator (handler_flow.go::registerJobRoutes,
// handler_flow.go::enqueueEnvelope, handler_flow.go::GetJobStatus).
//
// Delegation pattern (godlike/07 minimum-blast-radius):
// ScriptFlowHandler retains thin delegator methods (jobsRegisterRoutes
// + enqueueEnvelope) so active call sites keep calling
// h.enqueueEnvelope(c, env) unchanged. The delegation hops once
// through JobsHandler to the canonical impl.
//
// Future forward-pointer: PR-JOBS-OBSERVATION-CONSOLIDATE consolidates
// this file with internal/api/jobs/handler_workers.go (the Jobs module's
// canonical worker-observation handler). For now the new file lives here
// per SCRIPT-FLOW-SPLIT scope discipline (one capability per file).

package script

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// JobsHandler owns the canonical script-side job-observation surface:
//
//   - RegisterJobRoutes: mounts GET /api/script/jobs/:id under
//     RequireAdminToken(auth). `auth` is the AdminTokenProvider
//     (the caller — typically ScriptFlowHandler — supplies it
//     because JobsHandler does not carry an admin token itself).
//   - EnqueueEnvelope: validates the envelope and enqueues a
//     script.generate job. Invoked by both the unified /generate
//     endpoint (via HandlerGenerate calling enqueueEnvelopeFn
//     directly) and all legacy adapter routes (via ScriptFlowHandler
//     enqueueEnvelope thin delegator).
//   - GetJobStatus: canonical handler for GET /api/script/jobs/:id.
//
// It is a separate type from ScriptFlowHandler (22-field struct) per
// AGENTS.md Pattern 5: one capability per file, one struct per
// capability. godlike/06 SSOT (one canonical owner per fact) means
// JobsHandler owns ONLY these 3 methods — nothing else.
//
// SCRIPTCONTRACT-2026-07-08 PR-2: JobsHandler does NOT have a caps
// field. The preflight surface is passed as a per-call parameter
// (EnqueueEnvelope takes `caps PreflightCaps` from the caller —
// ScriptFlowHandler.enqueueEnvelope threads h.caps through). This
// keeps the canonical PreflightCaps instance on ScriptFlowHandler
// alone (one owner per fact) and avoids per-handler struct
// duplication of the same value.
type JobsHandler struct {
	jobsSvc  jobservice.Service
	log      *zap.Logger
	registry *appjobs.Registry
}

// NewJobsHandler constructs the canonical JobsHandler.
//
// The same 3-field shape as the pre-PR-2 JobsHandler (job-observation
// + enqueue share the same underlying job broker + logger + retry-
// policy registry). Kept as a separate struct per godlike/06 SSOT
// so each capability owns its reconstruction seam. The
// preflight surface is threaded per-call by the caller, NOT stored
// on the struct (canonical SOLE-owner discipline).
func NewJobsHandler(jobsSvc jobservice.Service, log *zap.Logger, registry *appjobs.Registry) *JobsHandler {
	return &JobsHandler{
		jobsSvc:  jobsSvc,
		log:      log,
		registry: registry,
	}
}

// RegisterJobRoutes mounts the canonical script job-status route.
// Blocco B (June 2026): /api/script/jobs/:id/full alias removed —
// the canonical route is /api/jobs/:id/full (mounted by the Jobs module).
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

// EnqueueEnvelope validates the envelope, runs the
// SCRIPTCONTRACT-2026-07-08 PR-2 preflight (using the `caps`
// parameter threaded by the caller — the canonical PreflightCaps
// instance lives on ScriptFlowHandler), reads the Idempotency-Key
// header, enqueues a script.generate job, and writes the async
// response. Canonical enqueue path shared with HandlerGenerate
// (4-field struct invoked directly from enqueueEnvelopeFn) and
// ScriptFlowHandler (legacy adapters via thin delegator).
//
// Delegates to the package-level enqueueEnvelopeFn so the async
// path stays single-implementation (godlike/06 SSOT).
func (jh *JobsHandler) EnqueueEnvelope(c *gin.Context, env domainScript.GenerationEnvelopeV2, caps PreflightCaps) {
	enqueueEnvelopeFn(c, env, jh.jobsSvc, jh.log, jh.registry, caps, nil)
}

// JobSha256Hex is a small helper used by test fixtures that
// compute the FASE 2 request_hash from the envelope identity.
// Local to the package to avoid cross-file helper imports in
// the slim per-handler struct pattern.

// jobEnvelopeIdentity is a thin wrapper around
// adapters.BuildEnvelopeIdentity used by EnqueueEnvelope.
// Kept local so the import surface stays minimal.
func jobEnvelopeIdentity(env *domainScript.GenerationEnvelopeV2) string {
	return adapters.BuildEnvelopeIdentity(env)
}

// compile-time guards
var (
	_ jobservice.Service = jobservice.Service(nil)
	_ *appjobs.Registry  = (*appjobs.Registry)(nil)
)

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
