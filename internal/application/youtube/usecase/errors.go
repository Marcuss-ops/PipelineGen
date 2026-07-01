// Package usecase — errors.go: typed error taxonomy for ProcessYouTubeSegmentUseCase.
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza): the
// verdict's P1 #14 explicitly bans the legacy `strings.Contains`-based
// retryable-error classifier in the job handler. This file ships the
// canonical typed alternative: every fail-closed step in
// process_segment.go wraps its error in `*ExtractionError` carrying
// a `FailureCode` + a `Retryable` bool, and the job classifier
// (jobs/classify.go) switches on `errors.As(err, &ee)` instead of
// string matching.
//
// Azione 3/8 di Step 7 (July 2026): the substring-match fallback
// delegates to `pkg/retry.IsTransient` (the canonical repository-
// wide "should I retry this?" predicate). The typed `*ExtractionError`
// path stays authoritative: a terminal `*ExtractionError{
// Retryable: false }` whose inner Cause happens to match the
// transient substring taxonomy (e.g. "503" appearing inside a
// terminal Cause) MUST classify as non-retryable — that is the
// Correttezza #9 property. The fallback is therefore only invoked
// after `errors.As` returns false: it covers raw port errors that
// bubble up through `retry.Do` before the use case can wrap them.
package usecase

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// FailureCode is the typed enum the use case returns on the canonical
// fail-closed path. Job handlers and Prometheus counters can switch
// on Code without parsing error strings.
type FailureCode string

const (
	// FailureCodeEmptyLocalPath is returned when the VideoPipeline
	// download/cut step produces a nil dlResult or an empty
	// LocalPath. Pre-Commit-2 the use case silently produced
	// `processed` with empty LocalPath; the post-Commit-2 path
	// fail-closes with this code.
	FailureCodeEmptyLocalPath FailureCode = "empty_local_path"

	// FailureCodeInvalidLocalArtifact is returned when the local
	// file is missing or has zero size. Pre-Commit-2 this slipped
	// through with fileHash == ""; post-Commit-2 fail-closed.
	FailureCodeInvalidLocalArtifact FailureCode = "invalid_local_artifact"

	// FailureCodeHashFailed is returned when hash.MD5File returns
	// an error or an empty hash on a non-empty local file. Pre-Commit-2
	// the error was silently swallowed.
	FailureCodeHashFailed FailureCode = "hash_failed"

	// FailureCodeDurationOutOfRange is returned when the segment
	// duration falls outside the SegmentPolicy [Min, Max] window.
	// Replaces the legacy 60s hardcoded upper bound (verdict P1 #9).
	FailureCodeDurationOutOfRange FailureCode = "duration_out_of_range"

	// FailureCodeInvalidTimestamp is returned when the segment's
	// Start/End timestamp strings fail textutil.ParseTimestamp or
	// when start >= end.
	FailureCodeInvalidTimestamp FailureCode = "invalid_timestamp"

	// FailureCodeVideoProcessingFailed is returned when the
	// VideoPipeline port returns a non-retryable error. The
	// `Retryable` field carries the transient classification
	// (transient = retry budget; terminal = no retry).
	FailureCodeVideoProcessingFailed FailureCode = "video_processing_failed"

	// FailureCodeDriveUploadFailed is returned when the Drive
	// upload step fails (terminal or transient — see Retryable).
	FailureCodeDriveUploadFailed FailureCode = "drive_upload_failed"

	// FailureCodeWriterFailed is returned when the ClipAtomicWriter
	// CommitClipAndIndexEvent returns an error. The error is
	// always terminal (the tx already failed or the schema
	// doesn't match); the job handler should mark the segment
	// as FAILED, not retry.
	FailureCodeWriterFailed FailureCode = "writer_failed"
)

// ExtractionError is the typed error the canonical use case returns
// on every fail-closed step. The job handler (jobs/classify.go) and
// the use case's own isTransientExtractionError fallback use
// `errors.As(err, &ee)` to read Code + Retryable without string
// matching.
type ExtractionError struct {
	Code      FailureCode
	Retryable bool
	Cause     error
	Message   string // operator-facing detail; not parsed by callers
}

// Error implements the error interface. Format:
//
//	"<Code>: <Message>: <Cause>"
//
// e.g. "hash_failed: writer: writer failed: open :memory: no such file".
// The cause is the underlying error (may be nil when the typed error
// is the terminal point).
func (e *ExtractionError) Error() string {
	switch {
	case e == nil:
		return "<nil ExtractionError>"
	case e.Cause != nil:
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	default:
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
}

// Unwrap returns the underlying cause for errors.Is / errors.As
// traversal. Returns nil when Cause is nil.
func (e *ExtractionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewExtractionError constructs a typed error. Cause may be nil
// (e.g. for the empty_local_path case where there's no underlying
// syscall error). Retryable is the canonical classification — the
// job handler reads it directly without re-deriving from the error
// string.
func NewExtractionError(code FailureCode, retryable bool, message string, cause error) *ExtractionError {
	return &ExtractionError{
		Code:      code,
		Retryable: retryable,
		Message:   message,
		Cause:     cause,
	}
}

// IsTransientExtractionError is the canonical retryable classifier.
//
// The typed path is authoritative: `errors.As(err, &ee)` reads the
// `Retryable` bool from `*ExtractionError` exactly as the use case's
// `NewExtractionError(code, retryable, ...)` constructor set it.
// A terminal `*ExtractionError{Retryable: false}` whose inner Cause
// happens to contain a transient substring (e.g. "503" inside a
// terminal syscall message) MUST classify as non-retryable — that
// is the Correttezza #9 property (the typed classification wins
// over heuristic substring matching).
//
// The substring fallback delegates to `pkg/retry.IsTransient` and
// is only invoked when `errors.As` returns false: it covers raw
// port errors bubbling up through `retry.Do` before the use case
// can wrap them (e.g. a transient 503 from yt-dlp wrapped as
// `fmt.Errorf("video processing failed: %w", err)`).
//
// Returns true when the typed path says retryable OR the substring
// fallback matches the canonical taxonomy. Returns false for nil
// errors, for terminal `*ExtractionError`, and for unknown shapes
// that match no transient substring.
func IsTransientExtractionError(err error) bool {
	if err == nil {
		return false
	}
	var ee *ExtractionError
	if errors.As(err, &ee) {
		return ee.Retryable
	}
	return retry.IsTransient(err)
}
