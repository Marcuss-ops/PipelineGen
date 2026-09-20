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
//	Processed == 0 && Failed == Requested    → per-item retryable verdict
//	    any item typed-retryable OR matching the legacy marker taxonomy
//	                                          → ErrExtractionRetryable
//	    otherwise                             → ErrExtractionTerminal
//
// Per-item verdicts are TYPED-FIRST (Sept 2026): the canonical
// per-segment pipeline projects the extraction verdict onto the item
// (dto.ExtractItem.FailureCode + .Retryable), and that typed verdict wins
// over any string heuristic. The marker taxonomy below is only the LEGACY
// FALLBACK for items produced before those fields existed, and it mirrors
// the `internal/capabilities/youtube/dto.IsTransientDownloadError`
// predicate (also used inside the canonical use case for inner download
// retries).
//
// Broker wiring (Sept 2026): the retryable verdict must reach the
// worker's retry gate — `retry.IsTransient(dispatchErr)`, a PURE TYPED
// PROBE since FASE 6 Cut 6.1.D. ErrExtractionRetryable and
// *PartialSuccessError are therefore implemented as typed retryable
// errors (IsRetryable() bool) instead of plain sentinels; otherwise a
// transient all-failed/partial run was dead-lettered on the first
// attempt regardless of the registry DefaultMaxRetries budget.
// ErrExtractionTerminal deliberately stays a plain non-retryable
// sentinel.
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

// extractionRetryableError is the TYPED retryable sentinel.
//
// It is a concrete type instead of `errors.New(...)` so it satisfies
// pkg/retry.RetryableError (IsRetryable() bool). The broker's retry
// gate is `retry.IsTransient(dispatchErr)` — a PURE TYPED PROBE with no
// substring fallback since FASE 6 Cut 6.1.D — so a plain sentinel made
// the "retryable" verdict INVISIBLE to the broker: an all-failed run
// with transient yt-dlp/Drive errors (429, 5xx, timeout) was
// dead-lettered on the first attempt despite the registry
// DefaultMaxRetries budget. err.Error() is byte-identical to the legacy
// sentinel text, so operator-facing logs/diagnostics do not drift.
type extractionRetryableError struct{}

func (extractionRetryableError) Error() string { return "youtube extraction: retryable failure" }

// IsRetryable satisfies pkg/retry.RetryableError. Value receiver: the
// sentinel satisfies the interface both bare and through the
// fmt.Errorf("...: %w", ...) envelopes the job handler builds.
func (extractionRetryableError) IsRetryable() bool { return true }

// ErrExtractionRetryable is the sentinel for transient extraction
// failures (ytdlp 429/503, network timeouts, Drive rate-limit). The job
// broker retries on this error using its exponential backoff policy.
var ErrExtractionRetryable = extractionRetryableError{}

// PartialSuccessError is the typed marker for "at least one segment
// processed AND at least one segment failed".
//
// The failed segments are typically transient and MUST be re-driven, so
// the job handler surfaces this as a RETRYABLE dispatch error: the
// broker schedules a retry, and the already-committed segments are
// replayed as cache hits (deterministic yt_<videoID>_<start>_<end>_vN
// clip identity → Step 2 cache lookup skips them).
//
// The error wraps ErrExtractionRetryable so callers that only check
// errors.Is(ErrExtractionRetryable) see a retryable disposition, and
// implements IsRetryable() so the broker's typed probe
// (retry.IsTransient) reaches the same verdict through
// fmt.Errorf("...: %w", ...) envelopes.
type PartialSuccessError struct {
	Processed int
	Failed    int
	Err       error
}

// IsRetryable satisfies pkg/retry.RetryableError: a partial success is
// retryable by construction (the failed segments are re-driven on the
// next attempt).
func (e *PartialSuccessError) IsRetryable() bool { return true }

func (e *PartialSuccessError) Error() string {
	return fmt.Sprintf("youtube extraction: partial success (%d processed, %d failed): %v",
		e.Processed, e.Failed, e.Err)
}

func (e *PartialSuccessError) Unwrap() error { return e.Err }

// classifyItemError returns true if a failed item is a TRANSIENT failure.
//
// TYPED-FIRST (Sept 2026): the canonical per-segment pipeline already
// knows the verdict — it projects ExtractionError.Code/Retryable onto the
// item (dto.ExtractItem.FailureCode + .Retryable). When that typed verdict
// is present it is AUTHORITATIVE: no string parsing, and a terminal item
// whose message happens to contain a transient-looking word (e.g. a
// duration_out_of_range item mentioning "timeout") is correctly terminal.
// This is the item-level twin of the Correttezza #9 property already
// enforced for the top-level error (typed classification beats heuristics).
//
// LEGACY FALLBACK: items produced before the typed fields existed (older
// server, replayed payload, cached result row) carry only Error, so the
// historical transient-marker taxonomy is still applied — removing it
// would silently reclassify every pre-existing job result as terminal.
// That substring scan is deliberately the youtube-extractor-specific
// allowlist (pre-FASE-6 it lived in pkg/retry.IsTransientString; FASE 6
// Cut 6.1 removed the shared version from pkg/retry, so any
// product-internal substring matching MUST be inlined at the call site).
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
func classifyItemError(item youtubetypes.ExtractItem) bool {
	if item.Retryable != nil {
		return *item.Retryable
	}
	s := strings.ToLower(item.Error)
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
		if classifyItemError(item) {
			hasRetryable = true
		}
	}
	if hasRetryable {
		return ErrExtractionRetryable
	}
	return ErrExtractionTerminal
}
