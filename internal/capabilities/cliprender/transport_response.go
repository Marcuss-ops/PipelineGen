package cliprender

// transport_response.go owns the canonical HTTP wire envelope of
// POST /api/clips/render. Mirrors the stock/stockbatches transport
// convention: a single response struct + canonical status/error-code
// literals. The job payload (RenderRequest) is the same shape as the
// HTTP body — no transport↔worker drift.

// renderResponse is the canonical POST /clips/render response envelope.
// Success: {job_id, status: "QUEUED"}. Failure: {status: "error",
// error, error_code}.
type renderResponse struct {
	JobID     string `json:"job_id,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// Canonical status literals.
const (
	StatusQueued = "QUEUED"
	StatusError  = "error"
)

// Canonical error codes.
const (
	ErrCodeInvalidPayload  = "INVALID_PAYLOAD"
	ErrCodeUnknownField    = "UNKNOWN_FIELD"
	ErrCodeJobsUnavailable = "JOBS_UNAVAILABLE"
)
