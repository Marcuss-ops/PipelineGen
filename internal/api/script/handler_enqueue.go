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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	mw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// enqueueTimeout is the maximum time the handler waits for the job
// broker to accept a script.generate job. It is a package-level var so
// tests can temporarily shorten it without rebuilding the binary.
var enqueueTimeout = 10 * time.Second

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
	store mw.IdempotencyStore,
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

	// Strong idempotency contract for script.generate:
	//   - Idempotency-Key header is required.
	//   - ActiveKey is derived as "script.generate:<fingerprint>".
	//   - Same payload + same key → cached response (completed) or same job_id (active).
	//   - Same key + different payload → 409 IDEMPOTENCY_KEY_CONFLICT.
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

	fingerprint := adapters.BuildEnvelopeIdentity(&env)
	activeKey := "script.generate:" + fingerprint

	// P0 strong idempotency: force_refresh bypasses both the
	// idempotency-store replay and the active-key dedup, forcing a
	// brand-new job regardless of prior active or completed records.
	forceRefresh := env.ForceRefresh

	if store != nil && !forceRefresh {
		ctx := c.Request.Context()
		_, exists, err := store.TryInsert(ctx, idempotencyKey, fingerprint)
		if err != nil {
			log.Error("idempotency store unavailable", zap.String("key", idempotencyKey), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "idempotency store unavailable"})
			return
		}
		if exists {
			record, gerr := store.Get(ctx, idempotencyKey)
			if gerr != nil {
				log.Error("idempotency store lookup failed", zap.String("key", idempotencyKey), zap.Error(gerr))
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "idempotency store lookup failed"})
				return
			}
			if record.BodyHash != fingerprint {
				c.JSON(http.StatusConflict, gin.H{
					"ok":    false,
					"error": "Idempotency-Key reused with different payload",
					"code":  "IDEMPOTENCY_KEY_CONFLICT",
				})
				return
			}
			if record.Status == "completed" {
				c.Writer.Header().Set("X-Idempotency-Replay", "true")
				c.Data(record.ResponseStatus, record.ResponseCT, record.ResponseBody)
				return
			}
			// in_flight: fall through to enqueue; the job service's
			// FindActiveByKey on activeKey will return the same job_id.
		}
	}

	req := jobs.NewGenerateEnqueueRequest(env)
	if forceRefresh {
		req.ActiveKey = ""
	} else {
		req.ActiveKey = activeKey
	}
	// Issue 4 (June 2026, P1): pass registry so MaxRetries is sourced
	// from registry.DefaultMaxRetries(script.generate) instead of the
	// pre-Issue-4 hard-coded 3-retry fallback.
	//
	// P0 async contract: enqueue must return quickly. A short timeout
	// prevents the POST from blocking if the job broker is congested.
	enqueueCtx, cancel := context.WithTimeout(c.Request.Context(), enqueueTimeout)
	defer cancel()

	enqueuedJob, err := jobs.EnqueueGenerationJob(enqueueCtx, jobsSvc, req, log, registry)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"ok":    false,
				"error": "JOB_ENQUEUE_TIMEOUT",
			})
			return
		}
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
	// P0 async contract: the client-facing status for a freshly accepted
	// job is "PENDING" (the job is persisted and will be picked up by the
	// worker queue). The canonical job-store status remains QUEUED.
	resp.async(enqueuedJob.ID, "PENDING", "/api/jobs/"+enqueuedJob.ID+"/full", "")

	// P0: force_refresh jobs are intentionally not cached; do not
	// overwrite any existing idempotency record.
	if store != nil && !forceRefresh {
		respBytes, _ := json.Marshal(resp)
		if cerr := store.Complete(c.Request.Context(), idempotencyKey, http.StatusAccepted, respBytes, "application/json"); cerr != nil {
			log.Warn("idempotency store complete failed", zap.String("key", idempotencyKey), zap.Error(cerr))
		}
	}

	c.JSON(http.StatusAccepted, resp)
}

// isValidIdempotencyKey mirrors the validation in the generic Gin
// idempotency middleware: printable ASCII only, max 255 characters.
func isValidIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > 255 {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r > 0x7E {
			return false
		}
	}
	return true
}
