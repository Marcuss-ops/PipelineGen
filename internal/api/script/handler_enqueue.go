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
	"errors"
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
// routes. It validates the envelope, runs the SCRIPTCONTRACT-2026-07-08
// PR-2 composition-time preflight (requesting a processor that is not
// wired at composition time returns 503 BEFORE any enqueue), reads the
// Idempotency-Key header for retry-safe dedup, enqueues a
// script.generate job, and writes the async response.
//
// Parameters are explicit — no struct coupling. Both HandlerGenerate
// (3-field) and ScriptFlowHandler (legacy adapters) call this function
// with their respective fields.
//
// SCRIPTCONTRACT-2026-07-08 PR-2 (godlike/07 NO-FAKE-AVAILABILITY):
// the preflight gate runs BEFORE env-validate-to-enqueue. A user
// envelope that requests `generate_voiceover=true` while
// `caps.VoiceoverEnabled=false` is rejected with 503 + the typed
// preflight error envelope — silent skip is FORBIDDEN in this
// surface (the pre-PR-2 behavior was a silent graceful-degradation
// that produced SUCCEEDED-with-empty-envelope, a godlike/07 violation).
func enqueueEnvelopeFn(
	c *gin.Context,
	env domainScript.GenerationEnvelopeV2,
	jobsSvc jobservice.Service,
	log *zap.Logger,
	registry *appjobs.Registry,
	caps PreflightCaps,
) {
	if err := env.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid envelope: " + err.Error()})
		return
	}

	// SCRIPTCONTRACT-2026-07-08 PR-2: composition-time preflight.
	// Runs BEFORE enqueue so a user-requested but unwired processor
	// returns 503 (canonical typed-error contract) and never reaches
	// the broker. The preflight itself is purely deterministic; the
	// only side effect is log.Warn on the failure path (operationally
	// useful for the operator dashboard).
	if err := requireRequestedProcessors(caps, &env, log); err != nil {
		// Probe both sentinel (errors.Is) and typed envelope
		// (errors.As) — the canonical godlike/07 typed-error
		// contract per `domainScript.errors_preflight.go`. The
		// HTTP response shape mirrors the typed envelope: surface
		// processor + reason to the client for diagnosability
		// without leaking internals.
		var typedPreflightErr *domainScript.PreflightProcessorMissingError
		processor := "unknown"
		reason := err.Error()
		if errors.As(err, &typedPreflightErr) {
			processor = typedPreflightErr.Processor
			reason = typedPreflightErr.Reason
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":                 false,
			"error":              "preflight: requested postprocessor unavailable at composition",
			"error_class":        "preflight_processor_missing",
			"processor":          processor,
			"reason":             reason,
			"preflight_sentinel": domainScript.ErrPreflightProcessorMissing.Error(),
		})
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
		// RED-6 (SCRIPT-T03-001): route typed client-validation failures to
		// 4xx via canonical mapper (godlike/06 SSOT one-owner-per-fact in
		// canonical_errors.go). Stays 500 with obfuscated message for any
		// unrecognized error so we never leak stack/file paths to the wire.
		status := CanonicalHTTPStatus(err)
		msg := CanonicalErrorMessage(err)
		c.JSON(status, gin.H{"ok": false, "error": msg})
		return
	}

	resp := GenerateResponse{}
	resp.async(enqueuedJob.ID, string(enqueuedJob.Status), "/api/jobs/"+enqueuedJob.ID+"/full", "")
	c.JSON(http.StatusOK, resp)
}
