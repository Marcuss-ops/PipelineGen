// Package jobs — classify.go: extraction-result classification.
//
// Commit C (PR-C-YouTube-Cutover, June 2026): the previous job handler
// returned `result, nil` whenever the extraction returned without a Go
// error, even when the response carried all-failed items with retryable
// error messages (typo: 429/503 timeouts). The classifier fixes the
// silent-success hole.
//
// Contract:
//
//	resp == nil | resp.Stats.Requested == 0 → ErrExtractionTerminal
//	resp.Stats.Failed == 0 && Processed > 0 → nil                  (full success)
//	Processed > 0 && Failed > 0               → *PartialSuccessError (nil but typed)
//	Processed == 0 && Failed == Requested     → retryable substring check
//	    any item.Error matches → ErrExtractionRetryable
//	    otherwise                → ErrExtractionTerminal
//
// The retryable substring taxonomy mirrors the existing
// `internal/application/youtube/dto.IsTransientDownloadError` predicate
// (also used inside the canonical use case for inner download retries).
package jobs

import (
	"errors"
	"fmt"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
)

// ErrExtractionTerminal is the sentinel for non-retryable extraction
// failures (invalid URL, invalid timestamp, FFmpeg corrupted input,
// Drive permanently gone, etc). The job broker marks the job as
// terminal on this error.
var ErrExtractionTerminal = errors.New("youtube extraction: terminal failure")

// ErrExtractionRetryable is the sentinel for transient extraction
// failures (ytdlp 429/503, network timeouts, Drive rate-limit). The job
// broker retries on this error using its exponential backoff policy.
var ErrExtractionRetryable = errors.New("youtube extraction: retryable failure")

// PartialSuccessError is the typed marker for "at least one segment
// processed AND at least one segment failed". The job handler can
// errors.As against it to log + persist a partial-success aggregate
// without surfacing a hard error to the broker.
//
// The error wraps ErrExtractionRetryable so callers that only check
// errors.Is(ErrExtractionRetryable) see a retryable disposition (the
// failed segments are typically transient and worth re-running on the
// next tick).
type PartialSuccessError struct {
	Processed int
	Failed    int
	Err       error
}

func (e *PartialSuccessError) Error() string {
	return fmt.Sprintf("youtube extraction: partial success (%d processed, %d failed): %v",
		e.Processed, e.Failed, e.Err)
}

func (e *PartialSuccessError) Unwrap() error { return e.Err }

// ClassifyExtractionResult returns nil for full success, *PartialSuccessError
// for at-least-one Processed + at-least-one Failed, ErrExtractionRetryable
// for all-failed-retryable, ErrExtractionTerminal for all-failed-terminal.
// Nil/invalid payload returns ErrExtractionTerminal.
//
// The classifier is a pure function: it does not read response fields
// beyond Stats and Items, so a panic-free log+drop fallback is possible
// even when the response shape drifts in upstream PRs.
func ClassifyExtractionResult(resp *youtubetypes.ExtractResponse) error {
	if resp == nil {
		return ErrExtractionTerminal
	}
	if resp.Stats == nil || resp.Stats.Requested == 0 {
		return ErrExtractionTerminal
	}

	// Full success.
	if resp.Stats.Failed == 0 && resp.Stats.Processed > 0 {
		return nil
	}

	// Partial success: at least one Processed AND at least one Failed.
	// Wraps ErrExtractionRetryable so errors.Is(ErrExtractionRetryable)
	// still hits for callers that don't know about PartialSuccess.
	if resp.Stats.Processed > 0 && resp.Stats.Failed > 0 {
		return &PartialSuccessError{
			Processed: resp.Stats.Processed,
			Failed:    resp.Stats.Failed,
			Err:       ErrExtractionRetryable,
		}
	}

	// All-failed path. Walk the items to classify which items are
	// retryable vs terminal; if ANY item is retryable, the parent
	// response is retryable (the others will succeed on the next retry).
	hasRetryable := false
	for _, item := range resp.Items {
		if item.Status != "failed" || item.Error == "" {
			continue
		}
		if isRetryableItemError(item.Error) {
			hasRetryable = true
		}
	}
	if hasRetryable {
		return ErrExtractionRetryable
	}
	return ErrExtractionTerminal
}

// isRetryableItemError applies the canonical retryable-error substring
// taxonomy to a single item's Error string. The taxonomy is:
//
//   - Network / IO: timeout, connection refused/reset, EOF
//   - ytdlp HTTP transient: 429, 503, 502, 504, "http error 5"
//   - Drive transient: rate limit, quota exceeded, temporarily unavailable
//
// Everything else (invalid URL, bad timestamp, FFmpeg corrupted input,
// Drive 404/permanently gone, etc.) is treated as terminal.
func isRetryableItemError(errStr string) bool {
	lower := strings.ToLower(errStr)
	for _, s := range retryableItemErrorSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// retryableItemErrorSubstrings is the canonical list. Keep this list in
// sync with isTransientExtractionError at the use case level.
var retryableItemErrorSubstrings = []string{
	"timeout",
	"connection refused",
	"connection reset",
	"eof",
	"429",
	"503",
	"502",
	"504",
	"http error 5",
	"rate limit",
	"quota exceeded",
	"temporarily unavailable",
}
