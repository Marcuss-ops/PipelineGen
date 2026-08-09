// Package script — handler_generate_handler.go is the thin HTTP transport
// for POST /api/script/generate. It owns only the fields it needs
// (submitter, scriptgenSvc, factory, log, validator) - 5 fields
// instead of the 22-field ScriptFlowHandler God Object.
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
// 3-file split (July 2026): the JSON-bind, validator, idempotency-key,
// payload-marshal, fingerprint, hash, SubmitRequest-assembly, and
// timeout-wrap logic previously inlined inside Generate has been
// extracted to handler_generate_request.go. The error-to-HTTP mapping
// and 202 submission-response shaping has been extracted to
// handler_generate_response.go. This file now owns ONLY the
// handler surface:
//
//   - HandlerGenerate struct
//   - NewHandlerGenerate (ctor + nil-tolerant defaults)
//   - GenerateRoute (registers POST /generate on the router group)
//   - Generate (the per-request orchestrator: ~5 logical steps)
//
// All business logic still lives in the application submission service
// and the generation use cases; this handler is responsible only for:
//   - delegating to the request-side helpers (request.go) — bind
//     envelope, validator, nil-submitter 503, idempotency key, hash,
//     SubmitRequest assembly, timeout-wrapped ctx + cancel
//   - calling h.submitter.Submit (the single application-layer
//     surface owned by this handler)
//   - deferring the cancel() returned by the request helper so the
//     enqueueTimeout timer is released as soon as Submit returns
//   - delegating to the response-side helpers (response.go) — 503/409/
//     mapped/500 error mapping OR 202 success with replay-header
//
// godlike/06 SSOT: the canonical `generationSubmitter` interface
// lives in handler_deps.go (the construction seam per its file
// comment); this file CONSUMES it via the `submitter` field.
// Defining the interface here would create a duplicate declaration
// and a build error in the same package.
//
// godlike/07 fail-closed: the empty-jobID success branch in
// writeGenerateSubmitSuccess (response.go) returns 500 with
// "operations submission returned empty job_id" — the handler
// never silently accepts a missing job_id.
package script

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// HandlerGenerate is the narrow HTTP handler for script generation.
// It owns exactly the 5 fields it needs - no more, no less.
// Constructed by NewScriptFlowHandler alongside the legacy
// ScriptFlowHandler; wired by RegisterRoutes as the handler for
// POST /api/script/generate.
//
// P0 verdetto (July 2026): the scriptgenSubmitter field wraps the
// existing generationSubmitter into a scriptgeneration.Submitter
// so StartAndSubmit creates the GenerationRun BEFORE any external
// I/O and returns the run's current_stage in the 202 response.
//
// PR-SUBMISSION-FACTORY (July 2026): the SubmitRequestFactory
// field builds the operations.SubmitRequest from the bound
// command, keeping policy/scope/job-type/hash decisions in the
// application layer.
type HandlerGenerate struct {
	submitter    generationSubmitter
	scriptgenSvc *scriptgen.GenerationRunStarter
	factory      *submission.SubmitRequestFactory
	log          *zap.Logger
	validator    *usecase.PayloadValidator
	preflight    usecase.ResearchPreflight
}

// NewHandlerGenerate constructs the handler from the canonical deps.
// All fields except the submitter are nil-tolerant at construction
// time; the Generate method's nil-guard on submitter returns 503 at
// request time.
//
// P0 verdetto (July 2026): scriptgenSvc is the GenerationRunStarter
// that creates the pipeline_run BEFORE submission. When nil, the
// handler falls back to the legacy direct-submit flow (no run tracking).
//
// PR-SUBMISSION-FACTORY (July 2026): factory builds the
// operations.SubmitRequest from the bound command.
func NewHandlerGenerate(
	submitter generationSubmitter,
	scriptgenSvc *scriptgen.GenerationRunStarter,
	factory *submission.SubmitRequestFactory,
	log *zap.Logger,
	validator *usecase.PayloadValidator,
	preflight ...usecase.ResearchPreflight,
) *HandlerGenerate {
	if log == nil {
		log = zap.NewNop()
	}
	if validator == nil {
		validator = usecase.NewDefaultPayloadValidator()
	}
	if factory == nil {
		factory = submission.NewSubmitRequestFactory()
	}
	var researchPreflight usecase.ResearchPreflight
	if len(preflight) > 0 {
		researchPreflight = preflight[0]
	}
	return &HandlerGenerate{
		submitter:    submitter,
		scriptgenSvc: scriptgenSvc,
		factory:      factory,
		log:          log,
		validator:    validator,
		preflight:    researchPreflight,
	}
}

// GenerateRoute registers the POST /generate route on the given
// router group. Called by ScriptFlowHandler.RegisterRoutes so the
// 22-field God Object doesn't own the route closure — the 5-field
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
// Response (P0 verdetto, July 2026):
//   - When h.scriptgenSvc is wired → 202 with current_stage
//     (pipeline_run created BEFORE the I/O call)
//   - When h.scriptgenSvc is nil  → fallback to legacy flow
//     (no current_stage in response)
//
// The orchestrator is intentionally ~4 logical steps:
//  1. buildGenerateCommand (request.go) → bind envelope,
//     validator, idempotency key. The nil-submitter guard lives
//     in the handler so a malformed body still returns 400 before
//     the 503 (matches the original Generate's order).
//  2. h.factory.Build(command) → application-layer SubmitRequest.
//  3. Create GenerationRun via scriptgenSvc.Start (BEFORE Submit)
//  4. h.submitter.Submit → application transaction commit.
//  5. writeGenerationRunSuccess (response.go) → 202 with current_stage
//     OR writeGenerateSubmitSuccess → legacy 202
//
// godlike/07 fail-closed: every step short-circuits with a structured
// HTTP error and never reaches the next step on failure.
func (h *HandlerGenerate) Generate(c *gin.Context) {
	cmd, ok := buildGenerateCommand(c, h.validator)
	if !ok {
		return
	}

	// nil-submitter fail-closed (godlike/07). Tested only via
	// composition-time fixtures; when the application service is not
	// wired (e.g. minimal test wiring), the request still gets the
	// canonical 503 from this guard.
	if h.submitter == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "operations service not initialized"})
		return
	}
	if h.preflight != nil {
		for _, item := range cmd.Envelope.Items {
			if err := h.preflight.Validate(c.Request.Context(), item); err != nil {
				bindGeneratePreflightError(c, err)
				return
			}
		}
	}

	// Application-layer SubmitRequest assembly (policy, scope,
	// job type, hash). Transport does not decide these values.
	submitReq, err := h.factory.Build(cmd)
	if err != nil {
		// ErrInvalidEnvelopeIdentity is a client payload problem
		// (empty identity after validation). All other factory
		// errors are structural/internal.
		if errors.Is(err, submission.ErrInvalidEnvelopeIdentity) {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "invalid generation payload identity",
				"code":  "INVALID_PAYLOAD",
			})
			return
		}
		writeGenerateSubmitError(c, err)
		return
	}

	// Transport-side throughput timeout. The cancel MUST be deferred
	// so the WithTimeout timer is released as soon as Submit returns.
	submitCtx, cancel := context.WithTimeout(c.Request.Context(), enqueueTimeout)
	defer cancel()

	// P0 verdetto: create the GenerationRun BEFORE the external I/O
	// and launch the runner in a background goroutine.
	// When scriptgenSvc is nil, fall back to the legacy flow.
	// After submission, set the JobID on the run so GET /full can
	// correlate the job with its generation run.
	if h.scriptgenSvc != nil {
		runReq := scriptgen.GenerateRequest{
			IdempotencyKey: submitReq.IdempotencyKey,
			ForceRefresh:   submitReq.ForceRefresh,
		}
		run, startErr := h.scriptgenSvc.Start(c.Request.Context(), runReq)
		if startErr != nil {
			writeGenerateSubmitError(c, startErr)
			return
		}

		res, err := h.submitter.Submit(submitCtx, submitReq)
		if err != nil {
			_ = h.scriptgenSvc.Fail(c.Request.Context(), run.ID, err)
			writeGenerateSubmitError(c, err)
			return
		}

		jobID := extractJobID(res)
		// Set the JobID on the run for GET /full correlation.
		run.JobID = jobID
		if err := h.scriptgenSvc.SetJobID(c.Request.Context(), run.ID, jobID); err != nil {
			_ = h.scriptgenSvc.Fail(c.Request.Context(), run.ID, err)
			writeGenerateSubmitError(c, err)
			return
		}
		writeGenerationRunSuccess(c, run, jobID, res, res != nil && res.IsIdempotencyHit)
		return
	}

	// Legacy fallback (no stage tracking).
	res, err := h.submitter.Submit(submitCtx, submitReq)
	if err != nil {
		writeGenerateSubmitError(c, err)
		return
	}
	writeGenerateSubmitSuccess(c, res)
}
