// Package jobs — classify.go: extraction-result classification.
//
// Commit C (PR-C-YouTube-Cutover, June 2026): the previous job handler
// returned `result, nil` whenever the extraction returned without a Go
// error, even when the response carried all-failed items with retryable
// error messages (typo: 429/503 timeouts). The classifier fixes the
// silent-success hole.
//
// Commit F (July 2026, P0-COMPL-2 follow-up): the previous full-success
// branch required resp.Stats.Processed > 0, which flagged 100% cache-hit
// re-runs (Processed=0, Skipped=Requested) as terminal failure. The job
// classifier now mirrors the usecase-level classifier (Correttezza #7,
// PR-C Commit 2/6): a cache-hit re-run is a full success.
//
// Contract:
//
//	resp == nil | resp.Stats.Requested == 0 → ErrExtractionTerminal
//	resp.Stats.Failed == 0 && (Processed + Skipped) == Requested
//	    && Requested > 0                    → nil               (full success)
//	Processed > 0 && Failed > 0              → *PartialSuccessError (typed)
//	Processed == 0 && Failed == Requested    → retryable substring check
//	    any item.Error matches → ErrExtractionRetryable
//	    otherwise                → ErrExtractionTerminal
//
// The retryable substring taxonomy mirrors the existing
// `internal/capabilities/youtube/dto.IsTransientDownloadError` predicate
// (also used inside the canonical use case for inner download retries).
package jobs

import (
	"errors"
	"fmt"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
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

// classifyItemError returns true if the youtube extractor's per-item
// error string matches a known TRANSIENT marker. The lookup uses
// strings.Contains (legacy substring pattern) because item.Error is a
// raw string field populated by the extractor at item-completion
// time; pre-FASE-6 this lived in pkg/retry.IsTransientString + the
// canonical transientSubstrings catalog. Per FASE 6 Cut 6.1 the
// substring classifier is REMOVED from pkg/retry (see
// pkg/retry/transient.go); any product-internal substring matching
// MUST be inlined at the call site. This is the youtube-extractor-
// specific allowlist.
//
// Marker taxonomy (extractor-pipeline observable; matches the
// youtube inner-download classifier patterns ytdlp + curl surface):
//
//   - "rate limit"           (HTTP 429 — retry-after may apply)
//   - "503"/"504"/"5xx"      (server transient)
//   - "timeout"             (network timeout)
//   - "eof"                 (network EOF)
//   - "connection refused"  (network fail)
//
// godlike/07 no-fake-availability: missing-mark-down-stream cases
// (e.g. "download failed: file deleted" — terminal because the
// file is gone) intentionally do NOT match → terminal classification,
// the caller observes the missing-item and re-queues the entire
// extract job instead of retrying.
//
// Forward-pointer Cut 6.1.F: a future 'youtube extractor must emit
// typed errors' cut will replace this substring-matcher with a
// typed classifier registered at init() (mirrors the Qdrant + SQLite
// distributed registries under internal/platform/).
func classifyItemError(itemError string) bool {
	s := strings.ToLower(itemError)
	for _, marker := range []string{
		"rate limit",
		"5xx",
		"503",
		"504",
		"timeout",
		"eof",
		"connection refused",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

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

	// Full success: zero failures AND (Processed + Skipped) accounts
	// for every Requested segment. Cache-hit re-runs (Processed=0,
	// Skipped=Requested) now legitimately classify as full success —
	// mirrors the usecase-level classifier (Correttezza #7).
	// The defensive `Requested > 0` guard is redundant with the early
	// `Stats.Requested == 0` check above but kept for clarity of the
	// success contract.
	if resp.Stats.Failed == 0 &&
		(resp.Stats.Processed+resp.Stats.Skipped == resp.Stats.Requested) &&
		resp.Stats.Requested > 0 {
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
		if classifyItemError(item.Error) {
			hasRetryable = true
		}
	}
	if hasRetryable {
		return ErrExtractionRetryable
	}
	return ErrExtractionTerminal
}
