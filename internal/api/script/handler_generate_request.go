// Package script — handler_generate_request.go is the request-side
// helper file for HandlerGenerate.Generate (handler_generate_handler.go).
//
// godlike/06 SSOT split (July 2026): the monolithic handler wrote
// JSON-binding, validator-running, idempotency-validating,
// payload-marshalling, identity-fingerprinting, hash-digesting,
// SubmitRequest-assembling, and timeout-wrapping LOGIC INLINE inside
// `Generate`. After the split every step lives in one canonical place:
//
//   - bindGenerateEnvelope       — JSON-binds + runs the application
//     validator + writes the canonical
//     structured error envelope
//   - validateIdempotencyKey     — header read + printable-ASCII check
//   - writes 400 on failure
//   - buildRequestHash           — payload marshal + identity
//     fingerprint + sha256 digest +
//     writes 500/400 on failure
//   - buildGenerateSubmitRequest — assembles + timeout-wraps; the
//     single public orchestrator called
//     by HandlerGenerate.Generate
//
// All four helpers are transport-side per AGENTS.md rule:
//
//   - bindGenerateEnvelope CALLS the application validator
//     (usecase.PayloadValidator) but does NOT own SQL/FFmpeg/Drive.
//   - buildRequestHash CALLS the application identity adapter
//     (adapters.BuildEnvelopeIdentity) but does NOT own hashing as
//     business logic — sha256 over the canonical identity fingerprint
//     is intrinsic to deriving a request-hash for the application
//     submission service (godlike/07 this is not a transport-policy
//     decision).
//   - validateIdempotencyKey + the canonical printable-ASCII rule live
//     in handler_generate_helpers.go (enqueueTimeout + isValid*).
//
// P0.A gate (handler_validation_contract_test.go): every validation
// failure path MUST (1) return HTTP 400, (2) emit the structured
// envelope {"ok":false,"error":{"code","message","stage","retryable"}},
// (3) NOT reach the submission service (submitCount == 0). All 4
// helpers preserve that contract byte-for-byte.
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

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// bindGenerateEnvelope JSON-binds the request body into a
// GenerationEnvelopeV2, then runs the application-side
// PayloadValidator. On either failure it writes the canonical
// structured error envelope and returns ok = false (the caller MUST
// early-return).
//
// Wire contract (godlike/07 fail-closed):
//   - malformed JSON / wrong top-level shape → HTTP 400 with
//     {"ok":false,"error":"invalid payload: <gin error>"}
//   - typed *PayloadValidationError → HTTP 400 with the typed
//     {code, message, stage, retryable, extra} envelope
//   - non-typed validator error       → HTTP 400 (via mapErrorToHTTP)
//     with {"ok":false,"error":{"code":"INVALID_PAYLOAD","message":..,"stage":"request.validation","retryable":false}}
//
// Validator runs BEFORE the submitter is invoked — P0.A invariant 3.
func bindGenerateEnvelope(c *gin.Context, validator *usecase.PayloadValidator) (*scriptpkg.GenerationEnvelopeV2, bool) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := c.ShouldBindJSON(&env); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return nil, false
	}

	if err := validator.ValidateEnvelope(&env); err != nil {
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
			return nil, false
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
		return nil, false
	}

	return &env, true
}

// validateIdempotencyKey reads the Idempotency-Key header, normalises
// whitespace, and applies the printable-ASCII + max-255 rule
// (delegated to handler_generate_helpers.go::isValidIdempotencyKey).
//
// godlike/07 fail-closed invariant: a missing or invalid key returns
// HTTP 400 with structured "error" + "code" (the legacy flat-string
// fallback was removed in handler_validation_contract_test.go's
// wire-contract pin). The validator matches the canonical
// middleware-level Idempotency middleware so a key accepted at the
// middleware boundary cannot be rejected here.
func validateIdempotencyKey(c *gin.Context) (string, bool) {
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Idempotency-Key header is required",
			"code":  "IDEMPOTENCY_KEY_REQUIRED",
		})
		return "", false
	}
	if !isValidIdempotencyKey(idempotencyKey) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Idempotency-Key must be printable ASCII and at most 255 characters",
			"code":  "INVALID_IDEMPOTENCY_KEY",
		})
		return "", false
	}
	return idempotencyKey, true
}

// buildRequestHash serialises the envelope to JSON (the JobPayload
// blob sent to the application submission service) and computes the
// canonical sha256 digest over the application-layer envelope
// identity fingerprint.
//
// Returns (requestHash, marshalledPayload, ok). On JSON marshal
// failure → writes HTTP 500 ("failed to marshal generation payload").
// On empty identity fingerprint → writes HTTP 400 with
// code=INVALID_PAYLOAD ("invalid generation payload identity").
//
// Transport-only invariant (godlike/06): the hash derivation is a
// transport-style checksum over the canonical application identity
// (adapters.BuildEnvelopeIdentity). It is NOT a business-rule
// decision — the canonical identity derivation already lives in
// internal/application/scripts/adapters (the application layer),
// and this helper just hashes it. See AGENTS.md rule "internal/api
// owns transport only".
func buildRequestHash(c *gin.Context, env *scriptpkg.GenerationEnvelopeV2) (string, []byte, bool) {
	payload, err := json.Marshal(env)
	if err != nil {
		// HTTP 500 — the envelope was just JSON-bound successfully,
		// so a marshal failure is a structural Go error and not a
		// client fault.
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "failed to marshal generation payload"})
		return "", nil, false
	}
	fingerprint := adapters.BuildEnvelopeIdentity(env)
	if fingerprint == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "invalid generation payload identity",
			"code":  "INVALID_PAYLOAD",
		})
		return "", nil, false
	}
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:]), payload, true
}

// buildGenerateSubmitRequest is the single canonical orchestrator
// called by HandlerGenerate.Generate. It sequentially invokes the
// three request-side helpers above, the nil-submitter guard (godlike/07
// fail-closed at request time — see AGENTS.md "Never represent an
// unavailable backend as a successful no-op"), assembles the
// opsapp.SubmitRequest, and wraps the request context with the
// package-level enqueueTimeout (handler_generate_helpers.go).
//
// Order preserves the original Generate's wire precedence:
//  1. JSON-bind + validator run → fail-closed 400 BEFORE the
//     nil-submitter check (matches the original Generate which
//     validated the envelope before checking the broker).
//  2. Nil-submitter guard → 503 fail-closed (godlike/07).
//  3. Header + hash + SubmitRequest assembly.
//
// Returns ok=false (and an upstream helper has already written the
// appropriate HTTP error response) on any failure; the caller MUST
// early-return on false. The returned cancel MUST be deferred by the
// caller so the WithTimeout timer is released as soon as the request
// finishes (otherwise the timer stays alive until the deadline elapses
// or the parent ctx is cancelled — a bounded but real resource leak).
//
// godlike/06 single-tx-boundary: this helper owns the SubmitRequest
// shape — the application-layer operations.Service is the SOLE owner
// of the database transaction boundaries (BeginTx/Commit/Rollback).
// The submitCtx timeout is the script-side throughput window ONLY;
// it MUST NOT be confused with the SQL transaction timeout which
// lives in the application's TxManager port.
//
// Scope + JobType + JobPriority + JobMaxRetries + ForceRefresh
// derivation: the script envelope forces scope=ScopeScriptGenerate,
// job-type=TypeScriptGenerate, priority=0, max-retries=3 (the canonical
// SCRIPTCONTRACT defaults). A future PR can extract these into a
// SubmitRequestFactory if the script package grows additional
// submission paths; for now the literals are colocated with the
// SubmitRequest shape as the single canonical owner.
func buildGenerateSubmitRequest(
	c *gin.Context,
	validator *usecase.PayloadValidator,
	submitter generationSubmitter,
) (opsapp.SubmitRequest, context.Context, context.CancelFunc, bool) {
	// Step 1: bind envelope + run validator (writes its own errors).
	env, ok := bindGenerateEnvelope(c, validator)
	if !ok {
		return opsapp.SubmitRequest{}, nil, nil, false
	}

	// Step 2: nil-submitter fail-closed (godlike/07). Tested only via
	// composition-time fixtures; when the application service is not
	// wired (e.g. minimal test wiring), the request still gets the
	// canonical 503 from this guard.
	if submitter == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "operations service not initialized"})
		return opsapp.SubmitRequest{}, nil, nil, false
	}

	// Step 3: validate the idempotency header (writes its own 400).
	idempotencyKey, ok := validateIdempotencyKey(c)
	if !ok {
		return opsapp.SubmitRequest{}, nil, nil, false
	}

	// Step 4: build the canonical request hash + payload (writes its
	// own 500/400 errors on failure).
	requestHash, payload, ok := buildRequestHash(c, env)
	if !ok {
		return opsapp.SubmitRequest{}, nil, nil, false
	}

	// Step 5: assemble the SubmitRequest and wrap the context with
	// the package-level enqueueTimeout. The caller controls the
	// cancel() lifecycle (Generate defers cancel around Submit).
	submitCtx, cancel := context.WithTimeout(c.Request.Context(), enqueueTimeout)

	req := opsapp.SubmitRequest{
		Scope:          domainops.ScopeScriptGenerate,
		IdempotencyKey: idempotencyKey,
		RequestHash:    requestHash,
		ForceRefresh:   env.ForceRefresh,
		JobType:        scriptpkg.TypeGenerate,
		JobPayload:     payload,
		JobPriority:    0,
		JobMaxRetries:  3,
	}
	return req, submitCtx, cancel, true
}
