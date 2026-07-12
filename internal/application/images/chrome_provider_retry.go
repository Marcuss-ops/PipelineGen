// Package images — chrome_provider_retry.go (commit 5, 2026-07):
// retry-once orchestration for the ChromeImageProvider.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, July 2026): per godlike/06 SSOT,
// chrome_provider_retry.go is the SINGLE canonical owner of "the
// retry-once seam" for the provider. chrome_provider.go owns the
// top-level Generate orchestrator; this file owns the
// retry-once logic that Generate delegates to. The previous
// inline retry block in Generate was a godlike/07 violation —
// typed-error classification was interleaved with resetWorker +
// log emission + firstErr capture, making the seam impossible to
// test in isolation.
//
// Why resetWorker lives here (not in a separate service, per user
// spec): resetWorker is a private method on *ChromeImageProvider
// that operates on p.cmd, p.stdin, p.stdout, p.started. Lifting it
// into a separate service would force every recovery path through
// an interface boundary that adds zero observability value while
// introducing a parameter-explosion surface (the four fields the
// reset mutates would all become arguments). godlike/06 SSOT:
// single canonical ownership of "the worker's process state
// transitions" lives on the receiver itself.
package images

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

// shouldRetryWorkerFailure reports whether a failed generateOnce
// outcome should trigger the retry-once seam. The 4 sentinel
// triggers are:
//
//   - isDeadWorkerError(err): broken pipe / EOF / dead-process
//     recovery. Always retry-with-reset — the next attempt lands on
//     a fresh subprocess.
//   - ErrImageGenRatioNotSelected: P1.3 (July 2026). 16:9 mandatory
//     selection failures. The panel may be in a transient state
//     where the dropdown collapses before the verify selector
//     resolves; a fresh subprocess gives the panel a clean state.
//   - ErrImageGenTimeout: P0.4 (July 2026). Worker hit the 60s
//     polling ceiling with NO candidate passing the baseline-diff
//     filter. The panel is likely stale; fresh subprocess
//     rehydrates the gallery state.
//   - ErrImageGenBlankOrPlaceholder: P0.4 (July 2026).
//     visual_validate rejected the bytes — the panel may have
//     rendered a placeholder that passed the worker's
//     dim/complete filter but failed Go-side content invariants.
//
// godlike/07 observability: each of the four branches is logged
// with a boolean field so operators can pivot on which trigger
// caused a reset from the audit log.
func (p *ChromeImageProvider) shouldRetryWorkerFailure(err error) bool {
	if err == nil {
		return false
	}
	if p.isDeadWorkerError(err) {
		return true
	}
	return errors.Is(err, ErrImageGenRatioNotSelected) ||
		errors.Is(err, ErrImageGenTimeout) ||
		errors.Is(err, ErrImageGenBlankOrPlaceholder)
}

// retryGenerationOnce executes one Generate attempt and, on a
// retryable failure, resets the worker state and re-attempts
// exactly once.
//
// Per-call firstErr capture (July-29 review-feedback, preserved
// byte-byte): if the retry ALSO fails (e.g. the second
// ensureStarted cannot find slide_worker.py on a misconfigured
// host, or the second attempt's network flakes out), the FIRST
// typed error is preferred for propagation to the caller. The
// first error carries the original failure mode (timeout /
// ratio / blank); the second error is layered implementation
// noise that obscures useful operator diagnostics.
//
// Must be called while p.mu is held (caller locks).
func (p *ChromeImageProvider) retryGenerationOnce(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error) {
	result, err := p.generateOnce(ctx, req)
	if err == nil {
		return result, nil
	}
	firstErr := err
	if !p.shouldRetryWorkerFailure(err) {
		return nil, err
	}

	p.log.Warn("ChromeImageProvider: recoverable worker failure, resetting and retrying once",
		zap.Error(err),
		zap.Bool("is_dead_worker", p.isDeadWorkerError(err)),
		zap.Bool("is_ratio_error", errors.Is(err, ErrImageGenRatioNotSelected)),
		zap.Bool("is_timeout_error", errors.Is(err, ErrImageGenTimeout)),
		zap.Bool("is_blank_placeholder_error", errors.Is(err, ErrImageGenBlankOrPlaceholder)))
	p.resetWorker()
	result2, err2 := p.generateOnce(ctx, req)
	if err2 == nil {
		return result2, nil
	}
	// Prefer the FIRST typed error for diagnostic clarity (the retry's
	// error may be a downstream infrastructure issue — script-not-found,
	// double-dead worker, etc. — that masks the original failure mode).
	p.log.Warn("ChromeImageProvider: retry also failed; surfacing first error",
		zap.Error(firstErr),
		zap.NamedError("retry_err", err2))
	return nil, firstErr
}
