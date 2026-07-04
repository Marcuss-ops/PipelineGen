// Package script — handler_enqueue.go is the canonical enqueue path
// for all script-generation routes (both the unified /generate endpoint
// and all legacy adapters). It is extracted as a package-level function
// so HandlerGenerate (3-field struct) and ScriptFlowHandler (legacy
// adapter methods) share a single implementation without coupling
// through a 22-field God Object.
//
// AZIONE 1 (July 2026): extracted from ScriptFlowHandler.enqueueEnvelope
// per the ScriptFlowHandler God Object decomposition action plan.
package script

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// enqueueEnvelopeFn is the canonical enqueue path for all script generation
// routes. It validates the envelope, reads the Idempotency-Key header for
// retry-safe dedup, enqueues a script.generate job, and writes the
// async response.
//
// Parameters are explicit — no struct coupling. Both HandlerGenerate
// (3-field) and ScriptFlowHandler (legacy adapters) call this function
// with their respective fields.
func enqueueEnvelopeFn(
	c *gin.Context,
	env domainScript.GenerationEnvelopeV2,
	jobsSvc jobservice.Service,
	log *zap.Logger,
	registry *appjobs.Registry,
) {
	if err := env.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid envelope: " + err.Error()})
		return
	}

	if jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "jobs service not initialized"})
		return
	}

	req := jobs.NewGenerateEnqueueRequest(env)
	// P0 #4 (June 2026): read Idempotency-Key header for retry-safe
	// dedup — same logic as the canonical /generate handler. Header
	// wins over any body field; trim is defensive so whitespace-only
	// headers don't produce phantom dedup keys.
	if idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key")); idempotencyKey != "" {
		req.ActiveKey = idempotencyKey
	}
	// Issue 4 (June 2026, P1): pass registry so MaxRetries is sourced
	// from registry.DefaultMaxRetries(script.generate) instead of the
	// pre-Issue-4 hard-coded 3-retry fallback.
	enqueuedJob, err := jobs.EnqueueGenerationJob(c.Request.Context(), jobsSvc, req, log, registry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	resp := GenerateResponse{}
	resp.async(enqueuedJob.ID, string(enqueuedJob.Status), "/api/jobs/"+enqueuedJob.ID+"/full", "")
	c.JSON(http.StatusOK, resp)
}
