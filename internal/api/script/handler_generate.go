// Package script — handler_generate.go is the thin HTTP transport
// for POST /api/script/generate. It binds the JSON body into a
// GenerationEnvelopeV2, delegates to the unified generation use case,
// and serialises the typed result to JSON.
//
// This handler replaces the four legacy endpoints:
//   - /generate-from-clips
//   - /generate-with-images
//   - /generate-batch
//   - /generate-from-catalog
//
// All business logic lives in GenerateOneUseCase / GenerateManyUseCase;
// this handler is responsible only for:
//   - JSON binding
//   - error-to-HTTP mapping
//   - JSON serialisation
package script

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Generate handles POST /api/script/generate.
//
// Body: a GenerationEnvelopeV2 JSON object.
//   - Single item  → async enqueue or sync generation
//   - Multiple items → async enqueue (batch)
//
// Response:
//   - Async: {"ok":true, "job_id":"...", "status":"QUEUED", "status_url":"..."}
//   - Sync single: {"ok":true, "script":"...", "word_count":1500, ...}
//   - Sync batch: {"ok":true, "count":3, "total":3, "results":[...]}
func (h *ScriptFlowHandler) Generate(c *gin.Context) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := c.ShouldBindJSON(&env); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}

	// Structural validation before enqueue.
	if err := env.Validate(); err != nil {
		status := mapErrorToHTTP(err)
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Enqueue as async job. The worker decodes the envelope
	// and runs GenerateOneUseCase / GenerateManyUseCase.
	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "jobs service not initialized"})
		return
	}

	// Build a typed GenerateRequest so the generation service
	// enqueues a script.generate job with the envelope as payload.
	req := jobs.NewGenerateEnqueueRequest(env)
	// Issue 5 (June 2026, P1): Stripe / AWS-SQS-style Idempotency-Key
	// support. Header wins over any future body field — keeps a single
	// precedence rule for future PRs. Trim is defensive; EnqueueGenerationJob
	// also trims internally so the broker dedup path is deterministic no
	// matter where the value originated.
	if idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key")); idempotencyKey != "" {
		req.ActiveKey = idempotencyKey
	}
	// Issue 4 (June 2026, P1): pass h.registry so MaxRetries is sourced
	// from registry.DefaultMaxRetries(script.generate) instead of the
	// pre-Issue-4 hard-coded 3-retry fallback.
	enqueuedJob, err := jobs.EnqueueGenerationJob(c.Request.Context(), h.jobsSvc, req, h.log, h.registry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	resp := GenerateResponse{}
	resp.async(enqueuedJob.ID, string(enqueuedJob.Status), "/api/jobs/"+enqueuedJob.ID+"/full", "")
	c.JSON(http.StatusOK, resp)
}
