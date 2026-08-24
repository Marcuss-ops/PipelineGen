// Package script — handler.go defines shared types consumed by ScriptFlowHandler.
// All route registrations and endpoint implementations live directly
// on ScriptFlowHandler (handler_flow.go).
package script

import (
	"errors"
	"net/http"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// mapErrorToHTTP maps domain-level script errors to HTTP status codes.
// Per the P0 verdetto (July 2026) error classification contract:
//
//	400 — payload or source.type not valid
//	409 — idempotency conflict with different payload
//	422 — scene formally valid but not processable
//	502 — Gemma or Docs invalid response
//	503 — provider not configured or temporarily unavailable
//	504 — timeout provider
//	500 — unexpected internal error
func mapErrorToHTTP(err error) int {
	switch {
	// 400 — client payload errors
	case errors.Is(err, scriptpkg.ErrInvalidPayload):
		return http.StatusBadRequest
	case errors.Is(err, scriptpkg.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, scriptpkg.ErrPlanInvalid):
		return http.StatusBadRequest

	// 409 — idempotency conflict
	case errors.Is(err, scriptpkg.ErrConflict):
		return http.StatusConflict

	// 422 — unprocessable entity (formally valid but not processable)
	case errors.Is(err, scriptpkg.ErrUnprocessable):
		return http.StatusUnprocessableEntity

	// 502 — provider returned invalid response
	case errors.Is(err, scriptpkg.ErrProviderBadResponse):
		return http.StatusBadGateway

	// 503 — provider not configured or temporarily unavailable
	case errors.Is(err, scriptpkg.ErrUnavailable):
		return http.StatusServiceUnavailable

	// 504 — provider timeout
	case errors.Is(err, scriptpkg.ErrProviderTimeout):
		return http.StatusGatewayTimeout

	default:
		return http.StatusInternalServerError
	}
}
