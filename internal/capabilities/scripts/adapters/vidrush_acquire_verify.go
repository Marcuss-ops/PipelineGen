package adapters

import (
	"context"
	"fmt"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type vidRushAcquireVerifyResult struct {
	candidate scriptpkg.SegmentAssetCandidate
	verified  scriptports.VerifiedArtifact
	err       error
	stage     string
}

// acquireAndVerify is the sole remote lifecycle boundary. It owns only
// provider calls, their timeouts, and canonical operation measurements; the
// caller owns warnings, catalog side effects, retries, and finalization.
func acquireAndVerify(ctx context.Context, provider scriptports.VidRushAssetProvider, candidate scriptpkg.SegmentAssetCandidate, providerName string, metrics VidRushTimingMetrics) vidRushAcquireVerifyResult {
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, vidRushProviderTimeout(providerName))
	var local scriptports.LocalArtifact
	err := measureVidRushProvider(acquireCtx, metrics, kernobs.OperationInfo{
		Stage: kernobs.StageAcquire, Component: "vidrush", Operation: "acquire", Provider: providerName,
	}, func(callCtx context.Context) error {
		var acquireErr error
		local, acquireErr = provider.Acquire(callCtx, candidate)
		return acquireErr
	})
	cancelAcquire()
	if err != nil {
		candidate.AcquisitionStatus = scriptpkg.VidRushStatusFailed
		return vidRushAcquireVerifyResult{candidate: candidate, err: err, stage: "acquire"}
	}

	verifyCtx, cancelVerify := context.WithTimeout(ctx, vidRushVerifyTimeout)
	var verified scriptports.VerifiedArtifact
	err = measureVidRushProvider(verifyCtx, metrics, kernobs.OperationInfo{
		Stage: kernobs.StageVerify, Component: "vidrush", Operation: "verify", Provider: providerName,
	}, func(callCtx context.Context) error {
		var verifyErr error
		verified, verifyErr = provider.Verify(callCtx, local)
		return verifyErr
	})
	cancelVerify()
	if err != nil {
		candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
		candidate.VerificationStatus = scriptpkg.VidRushStatusFailed
		return vidRushAcquireVerifyResult{candidate: candidate, err: err, stage: "verify"}
	}
	return vidRushAcquireVerifyResult{candidate: verified.Candidate, verified: verified}
}

const (
	// vidRushFinalizeAttempts bounds the synchronous persistence retry. A
	// transient Drive/SQLite/outbox failure must not defer an already
	// acquired+verified asset to the next run (ACC-9 class).
	vidRushFinalizeAttempts = 3
	// vidRushFinalizeRetryDelay is the base backoff; attempt N waits N×base.
	// Kept short: finalization runs inside the job's stage budget.
	vidRushFinalizeRetryDelay = 150 * time.Millisecond
)

// finalizeWithRetry is the synchronous persistence boundary: the finalizer is
// idempotent by content identity ("vidrush:<provider>:<asset>:<sha256>"), so
// retrying a transient failure in-run cannot duplicate an artifact, while a
// failure that exhausts the attempts is recorded with its structured cause
// instead of leaving persistence_status=failed without a reason.
func finalizeWithRetry(ctx context.Context, finalizer scriptports.VidRushArtifactFinalizer, verified scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	if finalizer == nil {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush finalizer: not configured")
	}
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= vidRushFinalizeAttempts; attempt++ {
		attempts = attempt
		persisted, err := finalizer.Finalize(ctx, verified)
		if err == nil {
			return persisted, nil
		}
		lastErr = err
		if ctx.Err() != nil || attempt == vidRushFinalizeAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt) * vidRushFinalizeRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("finalize retry aborted after %d attempt(s): %w", attempts, lastErr)
		case <-timer.C:
		}
	}
	return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("finalize failed after %d attempt(s): %w", attempts, lastErr)
}
