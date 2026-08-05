// Package script — handler_generate_response.go is the response-side
// helper file for HandlerGenerate.Generate (handler_generate_handler.go).
//
// godlike/06 SSOT split (July 2026): after the split every response-side
// concern lives in one canonical place:
//
//   - writeGenerateSubmitError   — error → HTTP mapping: 503 (timeout
//     /cancel), 409 (idempotency-conflict),
//     mapErrorToHTTP (typed → http status),
//     or 500 ("operations submission failed").
//   - writeGenerateSubmitSuccess — 202 Accepted with GenerateResponse.async
//     shape; X-Idempotency-Replay: true when
//     res.IsIdempotencyHit; canonical-status
//     surfaced from res.Job.Status.
//   - extractJobID               — res.Operation.JobID (the canonical
//     worker-assigned job_id); empty when
//     the application contract is empty
//     (fail-closed → 500).
//   - resolveJobStatus           — PENDING default OR res.Job.Status
//     (the canonical live Job state
//     surfaced on both replay and fresh-
//     submit alike per FASE 2 close-out).
//
// Wire contract (godlike/07): all 4 helpers are pure HTTP concerns.
// They do NOT own SQL/FFmpeg/Drive/provider orchestration —
// AGENTS.md rule "internal/api owns transport only" is preserved.
package script

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
)

// writeGenerateSubmitError maps an opsapp.Submit error to the canonical
// HTTP response shape.
//
// P0 verdetto error classification (July 2026):
//   - context.DeadlineExceeded | context.Canceled → 504 {"ok":false,"error":"JOB_ENQUEUE_TIMEOUT"}
//   - domainops.ErrIdempotencyConflict            → 409 {"ok":false,"error":"Idempotency-Key reused with different payload","code":"IDEMPOTENCY_KEY_CONFLICT"}
//   - opsapp.ErrSubmitQueueFull / ErrUnavailable  → 503 via mapErrorToHTTP
//   - any other error                              → mapErrorToHTTP(err) which
//     returns 400/422/502/504/500 per the typed error contract
//
// godlike/07 fail-closed: a context timeout is a gateway timeout (504),
// not a service-unavailable (503). The submitter may be congested but
// infrastructure-level retry logic (enqueue retry loop in the submitter)
// has already exhausted local retries; the caller should retry with
// backoff on 504. Idempotency conflict is a deterministic client fault
// (409). All other errors propagate through the canonical mapErrorToHTTP
// table which understands the full P0 classification.
func writeGenerateSubmitError(c *gin.Context, err error) {
	// 504 — provider timeout (changed from 503 per P0 verdetto).
	// Canceled is treated as timeout because the request context was
	// cancelled by the WithTimeout deadline, which is a timeout condition.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		c.JSON(http.StatusGatewayTimeout, gin.H{"ok": false, "error": "JOB_ENQUEUE_TIMEOUT"})
		return
	}

	// 409 — idempotency conflict.
	if errors.Is(err, domainops.ErrIdempotencyConflict) {
		c.JSON(http.StatusConflict, gin.H{
			"ok":    false,
			"error": "Idempotency-Key reused with different payload",
			"code":  "IDEMPOTENCY_KEY_CONFLICT",
		})
		return
	}

	// All other errors: map through the canonical typed-error table
	// (400 for invalid payload, 422 for unprocessable, 502 for provider
	// bad response, 503 for unavailable, 504 for timeout, 500 for
	// unexpected).
	status := mapErrorToHTTP(err)
	c.JSON(status, gin.H{
		"ok":    false,
		"error": "operations submission failed",
	})
}

// writeGenerateSubmitSuccess writes the canonical 202 Accepted
// response for a successful submitter.Submit call.
//
// Wire shape (handler_validation_contract_test.go + e2e tests pin this):
//   - When res.IsIdempotencyHit is true → set header X-Idempotency-Replay: true
//   - When res.Job.Status is non-empty   → use it as the surface status
//     (fresh + replay both per FASE 2 close-out)
//   - Otherwise                          → default status "PENDING"
//   - When res.Operation.JobID is empty  → fail-closed 500 ("operations submission returned empty job_id")
//   - Otherwise                          → 202 with GenerateResponse.async($jobID, $status, "/api/jobs/$jobID/full", "")
//
// godlike/06 SSOT: GenerateResponse (defined in handler_generate_types.go)
// is the canonical wire shape; this helper owns the production of a successful
// submission response and is consumed solely by HandlerGenerate.Generate.
func writeGenerateSubmitSuccess(c *gin.Context, res *opsapp.SubmitResult) {
	status := resolveJobStatus(res)
	if res != nil && res.IsIdempotencyHit {
		c.Writer.Header().Set("X-Idempotency-Replay", "true")
	}
	jobID := extractJobID(res)
	if jobID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "operations submission returned empty job_id"})
		return
	}

	resp := GenerateResponse{}
	resp.async(jobID, status, "/api/jobs/"+jobID+"/full", "")
	c.JSON(http.StatusAccepted, resp)
}

// writeGenerationRunSuccess writes the canonical 202 Accepted response
// that includes the GenerationRun's current_stage. The GenerationRun
// is created BEFORE the submission (verdetto invariant) so the client
// immediately knows the pipeline phase.
//
// Wire shape:
//   - When isReplay is true → set header X-Idempotency-Replay: true
//   - When jobID is empty   → fail-closed 500
//   - Otherwise             → 202 with GenerateResponse.asyncWithStage
//
// The run's CurrentStage reflects the initial pipeline phase
// (NORMALIZING → GENERATING_SCENE_TEXT → ... → WORKER_QUEUED).
func writeGenerationRunSuccess(c *gin.Context, run *scriptgen.GenerationRun, jobID string, isReplay bool) {
	if isReplay {
		c.Writer.Header().Set("X-Idempotency-Replay", "true")
	}
	if jobID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "submission returned empty job_id"})
		return
	}

	resp := GenerateResponse{}
	resp.asyncWithStage(jobID, "PENDING", "/api/jobs/"+jobID+"/full", "", string(run.CurrentStage))
	c.JSON(http.StatusAccepted, resp)
}

// extractJobID returns the canonical job_id from the application
// submission result. The job_id originates from the application's
// operations.Service (which composes the worker-assigned job) and
// is bound to the Operation row in the same atomic transaction.
//
// Returns "" whenever the application contract is missing — the
// caller treats that as fail-closed (HTTP 500). This is the canonical
// handler-side mirror of the application-side SubmitResult invariant
// (Operation != nil && Operation.JobID != "" after a successful
// commit; otherwise Service.Submit returns an error and the success
// path is unreachable in practice — but the handler-side defensive
// check survives future application contract drift).
func extractJobID(res *opsapp.SubmitResult) string {
	if res == nil || res.Operation == nil {
		return ""
	}
	return res.Operation.JobID
}

// resolveJobStatus returns the canonical live Job.Status when
// populated, otherwise the canonical "PENDING" default. Per FASE 2
// close-out the application submission service now propagates the
// freshly-INSERTed Job on fresh-submit AND the worker-updated Job on
// replay (via JobGetter), so res.Job.Status is the canonical truth —
// the legacy "always PENDING" default is the safety fallback for the
// theoretical case where the application returns a SubmitResult
// without a populated Job.
//
// godlike/06: this is a pure HTTP response-shaping helper. It does
// NOT reach into the database to read Job state — the application
// layer is the sole owner of the Job aggregate; this helper just
// routes the live snapshot into the response body.
func resolveJobStatus(res *opsapp.SubmitResult) string {
	if res != nil && res.Job != nil && res.Job.Status != "" {
		return string(res.Job.Status)
	}
	return "PENDING"
}
