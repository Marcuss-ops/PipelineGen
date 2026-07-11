// Package script — handler_generate_handler.go is the thin HTTP transport
// for POST /api/script/generate. It owns only the fields it needs
// (submissionSvc, log, caps, validator) - 4 fields instead of the
// 22-field ScriptFlowHandler God Object.
//
// AZIONE 1 (July 2026): extracted from ScriptFlowHandler per the
// ScriptFlowHandler God Object decomposition action plan. The
// Generate method binds the JSON body into a GenerationEnvelopeV2
// and delegates to the application submission service contract
// declared in internal/application/operations.
//
// FASE 2 (July 2026): the pre-FASE-2 package-level enqueueEnvelopeFn
// is REMOVED. HandlerGenerate now talks to the canonical
// GenerationSubmissionService via the generationSubmitter interface
// (declared in handler_deps.go); the adapter pattern keeps the
// HTTP-layer narrow port decoupled from the application concrete
// Service type.
//
// All business logic lives in the application submission service and
// the generation use cases; this handler is responsible only for:
//   - JSON binding
//   - error-to-HTTP mapping
//   - JSON serialisation
package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	jobpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// HandlerGenerate is the narrow HTTP handler for script generation.
// It owns exactly the 4 fields it needs - no more, no less.
// Constructed by NewScriptFlowHandler alongside the legacy
// ScriptFlowHandler; wired by RegisterRoutes as the handler for
// POST /api/script/generate.
//
// godlike/06 SSOT: the canonical `generationSubmitter` interface
// lives in handler_deps.go (the construction seam per its file
// comment); this file CONSUMES it via the `submitter` field.
// Defining the interface here would create a duplicate declaration
// and a build error in the same package.
type HandlerGenerate struct {
	submitter generationSubmitter
	log       *zap.Logger
	caps      PreflightCaps
	validator *usecase.PayloadValidator
}

// NewHandlerGenerate constructs the handler from the canonical deps.
// All fields except the submitter are nil-tolerant at construction
// time; the Generate method's nil-guard on submitter returns 503 at
// request time.
func NewHandlerGenerate(
	submitter generationSubmitter,
	log *zap.Logger,
	caps PreflightCaps,
	validator *usecase.PayloadValidator,
) *HandlerGenerate {
	if log == nil {
		log = zap.NewNop()
	}
	if validator == nil {
		validator = usecase.NewDefaultPayloadValidator()
	}
	return &HandlerGenerate{
		submitter: submitter,
		log:       log,
		caps:      caps,
		validator: validator,
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
//   - Single item  → async submission
//   - Multiple items → async submission (batch)
//
// Response:
//   - Async: {"ok":true, "job_id":"...", "status":"QUEUED", "status_url":"..."}
func (h *HandlerGenerate) Generate(c *gin.Context) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := c.ShouldBindJSON(&env); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}

	// Structural + config-aware validation before enqueue.
	if err := h.validator.ValidateEnvelope(&env); err != nil {
		var pve *scriptpkg.PayloadValidationError
		if errors.As(err, &pve) {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok": false,
				"error": gin.H{
					"code":      pve.Code,
					"message":   pve.Message,
					"stage":     pve.Stage,
					"retryable": pve.Retryable,
					"extra":     pve.Extra,
				},
			})
			return
		}
		status := mapErrorToHTTP(err)
		c.JSON(status, gin.H{
			"ok": false,
			"error": gin.H{
				"code":      "INVALID_PAYLOAD",
				"message":   err.Error(),
				"stage":     "request.validation",
				"retryable": false,
			},
		})
		return
	}

	if h.submitter == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "operations service not initialized"})
		return
	}

	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Idempotency-Key header is required",
			"code":  "IDEMPOTENCY_KEY_REQUIRED",
		})
		return
	}
	if !isValidIdempotencyKey(idempotencyKey) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Idempotency-Key must be printable ASCII and at most 255 characters",
			"code":  "INVALID_IDEMPOTENCY_KEY",
		})
		return
	}

	payload, err := json.Marshal(env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "failed to marshal generation payload"})
		return
	}
	fingerprint := adapters.BuildEnvelopeIdentity(&env)
	if fingerprint == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "invalid generation payload identity",
			"code":  "INVALID_PAYLOAD",
		})
		return
	}
	sum := sha256.Sum256([]byte(fingerprint))
	requestHash := hex.EncodeToString(sum[:])

	submitCtx, cancel := context.WithTimeout(c.Request.Context(), enqueueTimeout)
	defer cancel()

	res, err := h.submitter.Submit(submitCtx, opsapp.SubmitRequest{
		Scope:          domainops.ScopeScriptGenerate,
		IdempotencyKey: idempotencyKey,
		RequestHash:    requestHash,
		ForceRefresh:   env.ForceRefresh,
		JobType:        jobpkg.TypeScriptGenerate,
		JobPayload:     payload,
		JobPriority:    0,
		JobMaxRetries:  3,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "JOB_ENQUEUE_TIMEOUT"})
			return
		}
		if errors.Is(err, domainops.ErrIdempotencyConflict) {
			c.JSON(http.StatusConflict, gin.H{
				"ok":    false,
				"error": "Idempotency-Key reused with different payload",
				"code":  "IDEMPOTENCY_KEY_CONFLICT",
			})
			return
		}
		status := mapErrorToHTTP(err)
		c.JSON(status, gin.H{
			"ok":    false,
			"error": "operations submission failed",
		})
		return
	}

	status := "PENDING"
	if res != nil && res.IsIdempotencyHit {
		c.Writer.Header().Set("X-Idempotency-Replay", "true")
	}
	// FASE 2 close-out: surface the canonical live Job.Status
	// on replay and on fresh-submit alike (the spec wants
	// "lo stato del job canonico, non più una copia HTTP 202").
	// The Job field is populated by the submission service via
	// JobGetter on replay; on fresh-submit it carries the
	// freshly-INSERTed Job in QUEUED state.
	if res != nil && res.Job != nil && res.Job.Status != "" {
		status = string(res.Job.Status)
	}
	jobID := ""
	if res != nil && res.Operation != nil {
		jobID = res.Operation.JobID
	}
	if jobID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "operations submission returned empty job_id"})
		return
	}

	resp := GenerateResponse{}
	resp.async(jobID, status, "/api/jobs/"+jobID+"/full", "")
	c.JSON(http.StatusAccepted, resp)
}
