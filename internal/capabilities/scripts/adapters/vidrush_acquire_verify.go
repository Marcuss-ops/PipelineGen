package adapters

import (
	"context"

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
