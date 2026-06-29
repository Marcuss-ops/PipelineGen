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

	"github.com/gin-gonic/gin"

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

	// P0 #4 (June 2026): delegate to the centralized enqueue path
	// which reads Idempotency-Key, validates, checks jobsSvc, and
	// handles the full enqueue→response flow. Both canonical and
	// legacy routes share this method.
	h.enqueueEnvelope(c, env)
}
