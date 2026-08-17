package cliprender

// ── Response contract for POST /api/clips/render ──────────────────────

// renderResponse is the JSON body returned by POST /api/clips/render.
// godlike/06 SSOT: status strings describe the endpoint acknowledgement
// (decoupled from the broker job.Status enum — clients poll
// /api/jobs/{id}/full for broker-level state).
type renderResponse struct {
	// JobID is the canonical Master job id (non-empty on the async
	// success path).
	JobID string `json:"job_id,omitempty"`
	// Status is the endpoint acknowledgement:
	//   - "QUEUED" — request accepted, clip.render job scheduled via
	//     the Master (HTTP 202).
	//   - "error"  — validation/enqueue rejection (HTTP 4xx/5xx); the
	//     error_code field carries the machine-readable subtype.
	Status string `json:"status"`
	// Error is the human-readable message on rejection.
	Error string `json:"error,omitempty"`
	// ErrorCode is the machine-readable rejection subtype.
	ErrorCode string `json:"error_code,omitempty"`
}

// Endpoint acknowledgement + error-code literals (godlike/06 SSOT).
const (
	// StatusQueued = request accepted, work scheduled via the Master.
	StatusQueued = "QUEUED"
	// StatusError = validation/enqueue rejection.
	StatusError = "error"

	// ErrCodeInvalidPayload = malformed JSON or failed request
	// validation.
	ErrCodeInvalidPayload = "INVALID_PAYLOAD"
	// ErrCodeUnknownField = JSON body contained an undeclared field.
	ErrCodeUnknownField = "UNKNOWN_FIELD"
	// ErrCodeJobsUnavailable = the Master job service is not
	// configured (503).
	ErrCodeJobsUnavailable = "JOBS_UNAVAILABLE"
)
