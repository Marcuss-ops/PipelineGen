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
//   - buildGenerateCommand       — assembles the submission command
//     from the bound envelope + idempotency key
//
// PR-SUBMISSION-FACTORY (July 2026): buildRequestHash and
// buildGenerateSubmitRequest were moved to
// internal/capabilities/scripts/submission. The transport layer now
// only extracts the command; the application factory builds the
// SubmitRequest (scope, job type, policy, hash).
//
// All helpers are transport-side per AGENTS.md rule:
//
//   - bindGenerateEnvelope CALLS the application validator
//     (usecase.PayloadValidator) but does NOT own SQL/FFmpeg/Drive.
//   - validateIdempotencyKey + the canonical printable-ASCII rule live
//     in handler_generate_helpers.go (enqueueTimeout + isValid*).
//
// P0.A gate (handler_validation_contract_test.go): every validation
// failure path MUST (1) return HTTP 400, (2) emit the structured
// envelope {"ok":false,"error":{"code","message","stage","retryable"}},
// (3) NOT reach the submission service (submitCount == 0). All
// helpers preserve that contract byte-for-byte.
package script

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
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
//   - unknown fields (removed contract keys such as assemble_final) →
//     HTTP 400 with {"ok":false,"error":"invalid payload: json: unknown field ..."}
//     — the decoder runs with DisallowUnknownFields so removed contract
//     surface fails closed instead of being silently ignored.
//   - typed *PayloadValidationError → HTTP 400 with the typed
//     {code, message, stage, retryable, extra} envelope
//   - non-typed validator error       → HTTP 400 (via mapErrorToHTTP)
//     with {"ok":false,"error":{"code":"INVALID_PAYLOAD","message":..,"stage":"request.validation","retryable":false}}
//
// Validator runs BEFORE the submitter is invoked — P0.A invariant 3.
func bindGenerateEnvelope(c *gin.Context, validator *usecase.PayloadValidator) (*scriptpkg.GenerationEnvelopeV2, bool) {
	var env scriptpkg.GenerationEnvelopeV2
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return nil, false
	}
	// godlike/07 fail-closed: removed contract fields (e.g. assemble_final)
	// must be rejected at the request boundary, never silently ignored.
	// The kernel decoders are deliberately lenient (OutputSpec and the
	// audio intents carry custom UnmarshalJSON that swallow unknown keys),
	// so retired keys are detected against the RAW body, not the decoded
	// struct.
	if reason := removedFieldReason(body); reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + reason})
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
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

// removedGenerateFields maps retired script.generate contract keys to the
// reason they were removed. Any payload still carrying one fails closed at
// the request boundary instead of being silently ignored.
var removedGenerateFields = map[string]string{
	"assemble_final": "removed from the script.generate contract: localized clip rendering is the only render mode",
}

// removedFieldReason walks the raw request JSON and returns a descriptive
// reason when the payload still carries a retired contract key. It returns
// an empty string for any well-formed document without one; malformed JSON
// is left to the structured bind below to report.
func removedFieldReason(body []byte) string {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	var walk func(any) string
	walk = func(v any) string {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				if reason, removed := removedGenerateFields[k]; removed {
					return fmt.Sprintf("%q: %s", k, reason)
				}
				if reason := walk(child); reason != "" {
					return reason
				}
			}
		case []any:
			for _, child := range node {
				if reason := walk(child); reason != "" {
					return reason
				}
			}
		}
		return ""
	}
	return walk(doc)
}

func bindGeneratePreflightError(c *gin.Context, err error) {
	var pve *scriptpkg.PayloadValidationError
	if errors.As(err, &pve) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": gin.H{
			"code": pve.Code, "message": pve.Message, "stage": pve.Stage,
			"retryable": pve.Retryable, "extra": pve.Extra,
		}})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": gin.H{
		"code": "INVALID_PAYLOAD", "message": err.Error(),
		"stage": "request.validation", "retryable": false,
	}})
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

// buildGenerateCommand binds the envelope, runs the validator, and
// validates the idempotency header. It returns a submission command
// and an success flag. On failure it writes the canonical HTTP error
// response and the caller must early-return.
//
// PR-SUBMISSION-FACTORY (July 2026): the SubmitRequest assembly was
// moved to internal/capabilities/scripts/submission. The transport
// layer now only extracts the command and delegates assembly to the
// application layer.
func buildGenerateCommand(
	c *gin.Context,
	validator *usecase.PayloadValidator,
) (submission.GenerateCommand, bool) {
	// Step 1: bind envelope + run validator (writes its own errors).
	env, ok := bindGenerateEnvelope(c, validator)
	if !ok {
		return submission.GenerateCommand{}, false
	}

	// Step 2: validate the idempotency header (writes its own 400).
	idempotencyKey, ok := validateIdempotencyKey(c)
	if !ok {
		return submission.GenerateCommand{}, false
	}

	return submission.GenerateCommand{
		Envelope:       env,
		IdempotencyKey: idempotencyKey,
	}, true
}
