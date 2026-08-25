// Package mediamemory (api) — errors.go is the canonical SSOT for
// the typed-sentinel → HTTP status mapping.
//
// godlike/07 NO-FAKE-AVAILABILITY: a typed sentinel in the
// application layer translates to a stable (status, code) tuple
// in the wire layer. Callers branch on `code` (not on
// `message`, which is for humans). Unknown errors fall through to
// a generic 500 + logged ID — the wire only says "internal_error"
// (no internal detail leaks).
//
// godlike/06 SSOT: this file owns the canonical mapping. A future
// worker that wants to add a sentinel MUST also add its mapping
// here; the `MapError` function is the single arbiter.
package mediamemory

import (
	"errors"
	"net/http"
)

// MappedError is the typed-sentinel → HTTP translation product.
type MappedError struct {
	Status  int    // HTTP status code (matches apiutil.Error convention)
	Code    string // machine-readable enum (callers branch on this)
	Message string // human-readable detail
}

// MapError matches a typed-sentinel envelope against the canonical
// mapping table. Unknown errors return (500, "internal_error",
// "internal server error") and the caller logs the underlying
// cause separately (godlike/07: never leak stack/internal detail).
//
// godlike/06 SSOT: the mapping is built deliberately — a NEW
// sentinel in the application layer MUST be added here BEFORE
// shipping. The compile-time `_ = allMappingsCovered` pin in
// tests/the service enum catches drift.
func MapError(err error) MappedError {
	if err == nil {
		return MappedError{
			Status:  http.StatusOK,
			Code:    "ok",
			Message: "",
		}
	}
	switch {
	case errors.Is(err, ErrInvalidSlotKind):
		return MappedError{
			Status:  http.StatusBadRequest,
			Code:    "invalid_slot_kind",
			Message: err.Error(),
		}
	case errors.Is(err, ErrInvalidFeedbackAction):
		return MappedError{
			Status:  http.StatusBadRequest,
			Code:    "invalid_feedback_action",
			Message: err.Error(),
		}
	case errors.Is(err, ErrInvalidPhrase):
		return MappedError{
			Status:  http.StatusBadRequest,
			Code:    "invalid_phrase",
			Message: err.Error(),
		}
	case errors.Is(err, ErrInvalidBindingInput):
		return MappedError{
			Status:  http.StatusBadRequest,
			Code:    "invalid_binding",
			Message: err.Error(),
		}
	case errors.Is(err, ErrConceptNotFound):
		return MappedError{
			Status:  http.StatusBadRequest,
			Code:    "concept_not_found",
			Message: "concept_id does not reference an existing media_concepts row",
		}
	case errors.Is(err, ErrBindingNotFound):
		return MappedError{
			Status:  http.StatusNotFound,
			Code:    "binding_not_found",
			Message: err.Error(),
		}
	case errors.Is(err, ErrCandidateNotFound):
		return MappedError{
			Status:  http.StatusNotFound,
			Code:    "candidate_not_found",
			Message: err.Error(),
		}
	case errors.Is(err, ErrCandidateMaterializationFailed):
		return MappedError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "candidate_materialization_failed",
			Message: err.Error(),
		}
	case errors.Is(err, ErrDuplicateBinding):
		return MappedError{
			Status:  http.StatusConflict,
			Code:    "duplicate_binding",
			Message: err.Error(),
		}
	case errors.Is(err, ErrApprovalRequired):
		return MappedError{
			Status:  http.StatusPreconditionRequired,
			Code:    "approval_required",
			Message: err.Error(),
		}
	case errors.Is(err, ErrBatchNotFound):
		return MappedError{
			Status:  http.StatusNotFound,
			Code:    "batch_not_found",
			Message: err.Error(),
		}
	case errors.Is(err, ErrBatchNotReconcilable):
		return MappedError{
			Status:  http.StatusConflict,
			Code:    "batch_not_reconcilable",
			Message: err.Error(),
		}
	default:
		return MappedError{
			Status:  http.StatusInternalServerError,
			Code:    "internal_error",
			Message: "internal server error",
		}
	}
}
