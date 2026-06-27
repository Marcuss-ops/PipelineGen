// Package script — handler.go defines shared types consumed by ScriptFlowHandler.
// All route registrations and endpoint implementations live directly
// on ScriptFlowHandler (handler_flow.go).
package script

import (
	"errors"
	"net/http"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// mapErrorToHTTP maps domain-level script errors to HTTP status codes.
func mapErrorToHTTP(err error) int {
	switch {
	case errors.Is(err, scriptpkg.ErrInvalidPayload):
		return http.StatusBadRequest
	case errors.Is(err, scriptpkg.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, scriptpkg.ErrUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, scriptpkg.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
