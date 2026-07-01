// Package voiceover — pipeline_error.go (P0.1 false-success fix, Fase 1b, July 2026).
//
// PipelineError is the canonical typed error for per-item voiceover
// pipeline stage failures. It carries:
//
//   - Retryable: true for transient failures (TTS, Drive, network,
//     SQLite busy) where the worker SHOULD retry (DefaultMaxRetries: 2).
//     false for permanent failures (validation, path traversal, missing
//     destination) where retries would be wasteful.
//
//   - Stage: the canonical pipeline stage name ("validate", "tts",
//     "audio_post", "upload", "tx_begin", "db_delete", "db_insert",
//     "outbox_enqueue", "tx_commit", "destination_resolve").
//
//   - Cause: the underlying error from the port adapter.
//
// The handler checks errors.As(err, &PipelineError) to decide whether
// to let the worker retry (Retryable=true → normal error propagation
// → CanRetry() → RETRY_WAIT) or to propagate a permanent error that
// short-circuits retries (Retryable=false). Pre-Fase-1b every stage
// failure returned (out, nil) with no error at all — the handler
// couldn't distinguish transient from permanent.
//
// Implements the godlike/07 contract: no fake availability — a
// permanent error must NOT cycle through retries and MUST reach the
// terminal FAILED state on the first attempt.
package voiceover

import "fmt"

// Stage is the canonical pipeline stage identifier.
type Stage string

const (
	StageValidate          Stage = "validate"
	StageDestinationResolve Stage = "destination_resolve"
	StageTTS               Stage = "tts"
	StageAudioPost         Stage = "audio_post"
	StageUpload            Stage = "upload"
	StageTxBegin           Stage = "tx_begin"
	StageDBDelete          Stage = "db_delete"
	StageDBInsert          Stage = "db_insert"
	StageOutboxEnqueue     Stage = "outbox_enqueue"
	StageTxCommit          Stage = "tx_commit"
)

// PipelineError is the canonical typed error for per-item pipeline
// stage failures. Carry it through the handler → dispatcher → worker
// so the worker can decide whether to retry (Retryable=true) or fail
// permanently (Retryable=false).
type PipelineError struct {
	// Retryable is true for transient failures that may succeed on
	// retry: TTS connection timeout, Drive upload network error,
	// SQLite busy, outbox write race. The worker SHOULD retry.
	Retryable bool

	// Stage is the canonical pipeline stage where the failure occurred.
	Stage Stage

	// Cause is the underlying error from the port adapter.
	Cause error
}

// Error implements the error interface. The message includes the
// stage and retryability so operators can grep logs for
// "pipeline_error stage=tts retryable=true".
func (e *PipelineError) Error() string {
	retryable := "permanent"
	if e.Retryable {
		retryable = "retryable"
	}
	if e.Cause != nil {
		return fmt.Sprintf("pipeline_error stage=%s retryable=%s: %v", e.Stage, retryable, e.Cause)
	}
	return fmt.Sprintf("pipeline_error stage=%s retryable=%s", e.Stage, retryable)
}

// Unwrap returns the underlying cause so errors.Is / errors.As can
// traverse the error chain.
func (e *PipelineError) Unwrap() error {
	return e.Cause
}

// IsRetryable returns the Retryable flag. Convenience accessor so
// callers don't need to type-assert.
func (e *PipelineError) IsRetryable() bool {
	return e.Retryable
}

// newPipelineError is the canonical unexported constructor used inside
// the voiceover package (process_voiceover_item.go).
func newPipelineError(stage Stage, retryable bool, cause error) *PipelineError {
	return &PipelineError{
		Stage:     stage,
		Retryable: retryable,
		Cause:     cause,
	}
}

// NewPipelineError is the exported constructor for tests and callers
// outside the voiceover package (e.g. voiceover/jobs test fixtures).
func NewPipelineError(stage Stage, retryable bool, cause error) *PipelineError {
	return newPipelineError(stage, retryable, cause)
}
