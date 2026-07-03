// Package finalization — canonical error types for the transactional
// job-finalization spine (Spina Dorsale, Fase 1b, July 2026).
//
// Two families of errors are defined here:
//
//  1. Sentinel errors for the JobFinalizer contract (§ 4.3–4.5).
//     Callers use errors.Is to branch on these — no string matching.
//
//  2. VideoFailureCode + VideoError for YouTube extraction errors (§ 9.5).
//     Typed failure codes with a Retryable field so the worker's retry
//     policy can branch without string-matching on error messages.
//
// Canonical reference: Piano d'Azione Completo § 4.5, § 9.5.
package finalization

import (
	"errors"
	"fmt"
)

// ── Sentinel errors (JobFinalizer contract) ─────────────────────────

var (
	// ErrCompletionConflict is returned when CompleteWithArtifacts is
	// called on a job that is already SUCCEEDED with a DIFFERENT
	// result hash. The caller must not retry — the existing terminal
	// result is canonical.
	ErrCompletionConflict = errors.New("finalization: job already succeeded with a different result")

	// ErrStaleAttempt is returned when the request's attempt counter
	// is behind the job's current attempt. The caller must re-claim
	// the lease with the current attempt before retrying.
	ErrStaleAttempt = errors.New("finalization: stale attempt — lease attempt is behind current job attempt")

	// ErrRequiredArtifactMissing is returned when a required artifact
	// is not present in the FinalizationRequest. The job cannot
	// become SUCCEEDED until all required artifacts are published.
	ErrRequiredArtifactMissing = errors.New("finalization: required artifact missing from request")

	// ErrLeaseExpired is returned when the lease's ExpiresAt is in
	// the past at the time of the commit. The caller must re-claim
	// a fresh lease before retrying.
	ErrLeaseExpired = errors.New("finalization: lease expired")

	// ErrLeaseOwnerMismatch is returned when the lease's WorkerID
	// does not match the calling worker. Prevents a worker from
	// completing a job claimed by another worker.
	ErrLeaseOwnerMismatch = errors.New("finalization: lease owner mismatch — lease belongs to a different worker")

	// ErrDuplicateArtifact is returned when two artifacts in the
	// same FinalizationRequest have the same ArtifactID.
	ErrDuplicateArtifact = errors.New("finalization: duplicate artifact in request")

	// ErrInvalidIdempotencyKey is returned when an artifact's
	// IdempotencyKey is empty. Every artifact must carry a
	// deterministic key for idempotent finalisation.
	ErrInvalidIdempotencyKey = errors.New("finalization: artifact has empty idempotency key")

	// ErrChecksumMismatch is returned when an artifact's SHA-256
	// does not match the expected value from the result manifest.
	ErrChecksumMismatch = errors.New("finalization: artifact checksum mismatch")

	// ErrSizeMismatch is returned when an artifact's SizeBytes does
	// not match the expected value.
	ErrSizeMismatch = errors.New("finalization: artifact size mismatch")

	// ── P1.2 (July 2026) sentinels — Required/Optional artifact sidecar ───

	// ErrArtifactRequirementInvalid is returned when a VerifiedArtifact
	// or PublishedArtifact carries Requirement == ArtifactRequirementInvalid
	// (the typed-enum zero value). Callers MUST set Requirement to either
	// ArtifactRequirementRequired or ArtifactRequirementOptional
	// explicitly. The zero value is rejected by validateRequest so a
	// default-zero struct literal cannot silently pass as "Optional".
	ErrArtifactRequirementInvalid = errors.New("finalization: artifact Requirement is the zero value (ArtifactRequirementInvalid) — set explicitly to Required or Optional")

	// ErrOptionalDeclarationHasRequiredRequirement is returned when a
	// FinalizationRequest.OptionalDeclarations entry carries
	// Requirement == ArtifactRequirementRequired. OptionalDeclarations
	// is the audit sidecar dedicated to OPTIONAL artifacts only; a
	// required artifact belongs on the request's `Artifacts` list, not
	// on the declaration sidecar. Surfaces caller misclassification
	// loudly instead of letting the audit row silently misclassify.
	ErrOptionalDeclarationHasRequiredRequirement = errors.New("finalization: OptionalDeclarations entry has Requirement=Required — required artifacts belong on Artifacts, declarations are the optional sidecar only")

	// ErrOptionalArtifactFinalizedMismatch is returned when a
	// FinalizationRequest.OptionalDeclarations entry declares
	// Status=OptionalArtifactStatusFinalized but the matching
	// ArtifactID does NOT appear in the request's Artifacts list. The
	// worker promised the artifact was published, but the
	// cross-reference fails — likely a programming error (the worker
	// dropped the artifact on the way to BuildFinalizationRequest, or
	// set the wrong ArtifactID). Better surface loudly than emit a
	// misleading Finalized record.
	ErrOptionalArtifactFinalizedMismatch = errors.New("finalization: optional artifact declared Finalized but missing from Artifacts (cross-reference mismatch)")
)

// ── Structured error (JobFinalizer) ──────────────────────────────────

// FinalizationError is a structured error carrying a machine-readable
// code and additional diagnostic context. It satisfies the error
// interface and can be unwrapped via errors.Is against the sentinel
// errors above.
type FinalizationError struct {
	// Code is the machine-readable discriminator (e.g. "STALE_ATTEMPT").
	Code string `json:"code"`

	// Message is the human-readable description.
	Message string `json:"message"`

	// JobID is the canonical job identifier, when known.
	JobID string `json:"job_id,omitempty"`

	// ArtifactID is the artifact that triggered the error, when
	// applicable.
	ArtifactID string `json:"artifact_id,omitempty"`

	// Attempt is the attempt counter at the time of the error.
	Attempt int `json:"attempt,omitempty"`

	// Err is the wrapped sentinel error.
	Err error `json:"-"`
}

// Error implements the error interface.
func (e *FinalizationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("finalization: %s: %s (job=%s attempt=%d): %v", e.Code, e.Message, e.JobID, e.Attempt, e.Err)
	}
	return fmt.Sprintf("finalization: %s: %s (job=%s attempt=%d)", e.Code, e.Message, e.JobID, e.Attempt)
}

// Unwrap returns the wrapped sentinel error for errors.Is matching.
func (e *FinalizationError) Unwrap() error {
	return e.Err
}

// NewFinalizationError constructs a structured FinalizationError
// wrapping a sentinel error.
func NewFinalizationError(code, message, jobID string, attempt int, sentinel error) *FinalizationError {
	return &FinalizationError{
		Code:    code,
		Message: message,
		JobID:   jobID,
		Attempt: attempt,
		Err:     sentinel,
	}
}

// ── YouTube video failure codes (§ 9.5) ─────────────────────────────

// VideoFailureCode is a machine-readable discriminator for YouTube
// extraction failures. The worker's retry policy branches on this
// code — no string-matching on error messages.
type VideoFailureCode string

const (
	// VideoPrivate — the video is private or unlisted and the
	// current credentials cannot access it. Permanent: no retry.
	VideoPrivate VideoFailureCode = "VIDEO_PRIVATE"

	// VideoRemoved — the video has been deleted or taken down.
	// Permanent: no retry.
	VideoRemoved VideoFailureCode = "VIDEO_REMOVED"

	// VideoGeoBlocked — the video is not available in the current
	// region. Permanent unless the proxy or policy changes.
	VideoGeoBlocked VideoFailureCode = "GEO_BLOCKED"

	// VideoAuthRequired — the video requires authentication that
	// the current session does not provide. No automatic retry;
	// requires operator intervention to refresh cookies/tokens.
	VideoAuthRequired VideoFailureCode = "AUTH_REQUIRED"

	// VideoRateLimited — the source (YouTube, yt-dlp) is
	// rate-limiting the request. Retryable with backoff.
	VideoRateLimited VideoFailureCode = "RATE_LIMITED"

	// VideoNetworkTimeout — the network request timed out.
	// Retryable with backoff.
	VideoNetworkTimeout VideoFailureCode = "NETWORK_TIMEOUT"

	// VideoFormatUnavailable — the requested format is not
	// available for this video. Permanent unless the format
	// policy changes (e.g. a different format ID is selected).
	VideoFormatUnavailable VideoFailureCode = "FORMAT_UNAVAILABLE"

	// VideoOutputEmpty — the download or processing produced an
	// empty or zero-byte output. Retryable once with a fallback
	// path; terminal if the fallback also fails.
	VideoOutputEmpty VideoFailureCode = "OUTPUT_EMPTY"

	// VideoDiskFull — the local disk is full. No retry until the
	// resource is freed by an operator or cleanup job.
	VideoDiskFull VideoFailureCode = "DISK_FULL"
)

// IsPermanent returns true for failure codes that should never be
// retried because the condition is unlikely to change without
// external intervention (operator, policy update, credential refresh).
//
// Retry policy (Piano d'Azione § 9.5):
//
//	VIDEO_PRIVATE        → permanent (no retry)
//	VIDEO_REMOVED        → permanent (no retry)
//	GEO_BLOCKED          → permanent (unless proxy/policy changes)
//	AUTH_REQUIRED        → permanent (no automatic retry)
//	RATE_LIMITED         → retryable
//	NETWORK_TIMEOUT      → retryable
//	FORMAT_UNAVAILABLE   → permanent (unless policy changes)
//	OUTPUT_EMPTY         → retryable once, then terminal
//	DISK_FULL            → permanent (until resource freed)
func (c VideoFailureCode) IsPermanent() bool {
	switch c {
	case VideoPrivate, VideoRemoved, VideoGeoBlocked,
		VideoAuthRequired, VideoFormatUnavailable, VideoDiskFull:
		return true
	}
	return false
}

// IsRetryable returns the inverse of IsPermanent. OUTPUT_EMPTY is
// treated as retryable at this level; the worker's retry policy
// enforces the "once, then terminal" rule.
func (c VideoFailureCode) IsRetryable() bool {
	return !c.IsPermanent()
}

// ── VideoError (§ 9.5) ─────────────────────────────────────────────

// VideoError is a structured error for YouTube extraction failures.
// It carries a machine-readable failure code and the underlying cause.
// The worker's retry policy branches on Code.IsRetryable() rather
// than string-matching on error messages.
type VideoError struct {
	// Code is the machine-readable failure discriminator.
	Code VideoFailureCode `json:"code"`

	// VideoID is the YouTube video ID that triggered the error.
	VideoID string `json:"video_id,omitempty"`

	// Retryable reports whether the worker should retry this error.
	// Derived from Code.IsRetryable() but may be overridden for
	// context-specific exceptions (e.g. OUTPUT_EMPTY with a
	// successful fallback path).
	Retryable bool `json:"retryable"`

	// Cause is the underlying error (yt-dlp exit code, network
	// timeout, etc.).
	Cause error `json:"-"`
}

// Error implements the error interface.
func (e *VideoError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("youtube: %s (video=%s retryable=%v): %v", e.Code, e.VideoID, e.Retryable, e.Cause)
	}
	return fmt.Sprintf("youtube: %s (video=%s retryable=%v)", e.Code, e.VideoID, e.Retryable)
}

// Unwrap returns the underlying cause for errors.Is / errors.As.
func (e *VideoError) Unwrap() error {
	return e.Cause
}

// NewVideoError constructs a VideoError. The Retryable field is
// derived from the failure code's IsRetryable() default but can
// be overridden by the caller for context-specific exceptions.
func NewVideoError(code VideoFailureCode, videoID string, cause error) *VideoError {
	return &VideoError{
		Code:      code,
		VideoID:   videoID,
		Retryable: code.IsRetryable(),
		Cause:     cause,
	}
}
