package cliprender

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

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

// enqueuedStatus reports the job's DURABLE status. Enqueue is idempotent on
// (type, correlation_id): an identical retry can return an existing job that is
// already RUNNING or even FAILED, and answering "QUEUED" for it would lie to
// the caller. A zero status (test doubles, legacy rows) keeps QUEUED.
func enqueuedStatus(j *job.Job) string {
	if j != nil && j.Status != "" {
		return string(j.Status)
	}
	return StatusQueued
}

// Canonical error codes.
const (
	ErrCodeInvalidPayload  = "INVALID_PAYLOAD"
	ErrCodeUnknownField    = "UNKNOWN_FIELD"
	ErrCodeJobsUnavailable = "JOBS_UNAVAILABLE"
	ErrCodeInternal        = "INTERNAL_ERROR"
	ErrCodeNotFound        = "NOT_FOUND"
	// ErrCodePayloadTooLarge is returned (HTTP 413) when the bounded request
	// body exceeds MaxClipRenderRequestBytes.
	ErrCodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
)
