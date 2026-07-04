// Package script — handler_generate_handler.go is the thin HTTP transport
// for POST /api/script/generate. It owns only the fields it needs
// (jobsSvc, log, registry) — 3 fields instead of the 22-field
// ScriptFlowHandler God Object.
//
// AZIONE 1 (July 2026): extracted from ScriptFlowHandler per the
// ScriptFlowHandler God Object decomposition action plan. The
// Generate method binds the JSON body into a GenerationEnvelopeV2
// and delegates to the package-level enqueueEnvelopeFn.
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
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// HandlerGenerate is the narrow HTTP handler for script generation.
// It owns exactly the 3 fields it needs — no more, no less.
// Constructed by NewScriptFlowHandler alongside the legacy
// ScriptFlowHandler; wired by RegisterRoutes as the handler for
// POST /api/script/generate.
type HandlerGenerate struct {
	jobsSvc  jobservice.Service
	log      *zap.Logger
	registry *appjobs.Registry
}

// NewHandlerGenerate constructs the handler from the canonical deps.
// All three fields are nil-tolerant at construction time; the
// Generate method's nil-guards on jobsSvc return 503 at request time.
func NewHandlerGenerate(
	jobsSvc jobservice.Service,
	log *zap.Logger,
	registry *appjobs.Registry,
) *HandlerGenerate {
	if log == nil {
		log = zap.NewNop()
	}
	return &HandlerGenerate{
		jobsSvc:  jobsSvc,
		log:      log,
		registry: registry,
	}
}

// GenerateRoute registers the POST /generate route on the given
// router group. Called by ScriptFlowHandler.RegisterRoutes so the
// 22-field God Object doesn't own the route closure — the 3-field
// HandlerGenerate does.
//
// Nil-safe: when h is nil (bare struct construction in test fixtures),
// the route is silently skipped — no /generate endpoint is mounted.
func (h *HandlerGenerate) GenerateRoute(r *gin.RouterGroup) {
	if h == nil {
		return
	}
	r.POST("/generate", h.Generate)
}

// Generate handles POST /api/script/generate.
//
// Body: a GenerationEnvelopeV2 JSON object.
//   - Single item  → async enqueue
//   - Multiple items → async enqueue (batch)
//
// Response:
//   - Async: {"ok":true, "job_id":"...", "status":"QUEUED", "status_url":"..."}
func (h *HandlerGenerate) Generate(c *gin.Context) {
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

	// P0 #4 (June 2026): delegate to the centralized enqueue path.
	// Uses the package-level enqueueEnvelopeFn shared with legacy adapters.
	enqueueEnvelopeFn(c, env, h.jobsSvc, h.log, h.registry)
}
