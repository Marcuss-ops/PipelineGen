package domain

import "errors"

// Sentinel errors surfaced across the scriptgen module. The transport
// layer (owned by Agent 1) maps these to HTTP status codes; consumers
// MUST use errors.Is to compare.
//
// Keep this list stable — binary‑compat with the legacy
// application/scriptflow errors is intentional so PR 2C (thin API) does
// not have to redo the request map.
var (
	ErrValidation         = errors.New("scriptgen: validation error")
	ErrInvalidPayload     = errors.New("scriptgen: invalid payload")
	ErrUnsupportedVersion = errors.New("scriptgen: unsupported prompt version")
	ErrUnavailable        = errors.New("scriptgen: dependency unavailable")
	ErrConflict           = errors.New("scriptgen: conflicting state")
	ErrTimeout            = errors.New("scriptgen: operation timed out")
	ErrEmptySceneList     = errors.New("scriptgen: empty scene list")
	ErrMissingPlan        = errors.New("scriptgen: missing plan/outline")
	ErrSearchFailed       = errors.New("scriptgen: semantic search failed")
)
