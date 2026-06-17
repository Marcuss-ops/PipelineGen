// Package veloxclient is a minimal HTTP client for submitting jobs to a
// pipelinegen server from any worker (creator sidecar, sister microservice,
// CI job, dev script). Its surface mirrors the script generation endpoints
// exposed by PipelineGen's REST API and is designed to be paired with the
// X-Request-ID → UNIQUE(type, correlation_id) idempotency layer added in
// PR1 / migrations/sqlite/036_job_idempotency.sql.
//
// Two purpose-built artifacts are exported from this module:
//   - The Go client (this package), importable from any Go worker.
//   - A stdlib-only Python client (scripts/velox_client.py) for Python
//     workers — same authentication, retry, and idempotency semantics.
//
// Auth uses the standard `Authorization: Bearer <token>` header. Pair the
// token with a server-side `VELOX_WORKER_TOKEN` configured on the
// pipelinegen host — admin tokens should NOT be shared with workers, as
// the dedicated worker role isolates blast radius if a token leaks.
package veloxclient

import (
	"errors"
	"time"
)

// AsyncResponse is what /api/script/generate-with-images (and the sister
// endpoints) return after a successful enqueue. The HTTP POST completes
// the moment the job is durably persisted in the queue; the actual work
// happens asynchronously on the worker pool. Callers should poll
// GET /api/jobs/{ID}/full with GetJobStatus to track progress.
type AsyncResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// JobStatusResponse mirrors the payload of GET /api/jobs/{ID}/full for
// the fields workers typically need. Result is decoded as a generic map
// because the per-job-type payload varies (script.image_generation result
// has different fields than script.voiceover.batch).
type JobStatusResponse struct {
	ID       string         `json:"id"`
	Status   string         `json:"status"`
	Type     string         `json:"type"`
	Progress int            `json:"progress"`
	Error    string         `json:"error,omitempty"`
	Result   map[string]any `json:"result,omitempty"`
}

// JobStatus enum values, mirrored from internal/media/models/job_types.go.
// Terminal statuses (completed, failed, cancelled) are the natural polling
// exit conditions; non-terminal are queued/running.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// IsTerminal returns true if the status will not transition further.
func IsTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Error taxonomy — sentinel errors that wrap the underlying HTTP result so
// callers can branch on a category without re-parsing status codes. All
// three are returned via errors.Is checks.
var (
	// ErrUnauthorized covers 401/403. Action: rotate the worker token on
	// the server, redeploy the worker, retry.
	ErrUnauthorized = errors.New("veloxclient: unauthorized (rotate token)")

	// ErrBadRequest covers non-auth 4xx (validation, payload too large,
	// required field missing). Action: fix the request body; do NOT retry
	// blindly — it will fail the same way.
	ErrBadRequest = errors.New("veloxclient: bad request (do not retry)")

	// ErrServer covers 5xx and any network-level error after retries are
	// exhausted. Action: surface to the operator; same X-Request-ID can be
	// retried later once the server is healthy.
	ErrServer = errors.New("veloxclient: server error (surface to operator)")

	// ErrNotFound is returned when a job_id cannot be found by the server
	// (typically a typo or a job that finished and was pruned). Distinct
	// from ErrBadRequest because the caller might want to retry with a
	// fresh submission rather than crashing on the bad lookup.
	ErrNotFound = errors.New("veloxclient: job not found")
)

// DefaultMaxAttempts is 3 attempts: initial + 2 retries. The same
// X-Request-ID is reused across all attempts so the server-side idempotency
// guarantees ON DUPLICATE dedup naturally — even on partial network
// failures where the server queued the job but the response was lost.
const DefaultMaxAttempts = 3

// DefaultRetryBase is the first exponential backoff base. With base 200ms
// the schedule is roughly 200ms → 400ms → 800ms, well under the typical
// 30-second script generation worker window.
const DefaultRetryBase = 200 * time.Millisecond
