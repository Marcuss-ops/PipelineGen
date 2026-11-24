package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderqueue"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// The queue contract and its pure projections now live in scripts/renderqueue.
// The aliases below keep the capability's public surface source-compatible.
type (
	RenderQueueAsset              = renderqueue.RenderQueueAsset
	RenderQueueJob                = renderqueue.RenderQueueJob
	RenderQueuePreparedPackageRef = renderqueue.RenderQueuePreparedPackageRef
	RenderFrameRange              = renderqueue.RenderFrameRange
	RenderQueueClient             = renderqueue.RenderQueueClient
	RenderQueueBatchSubmitter     = renderqueue.RenderQueueBatchSubmitter
	RenderQueueChildrenReader     = renderqueue.RenderQueueChildrenReader
	RenderQueueWaiter             = renderqueue.RenderQueueWaiter
	RenderQueueRetrier            = renderqueue.RenderQueueRetrier
	RenderCompletionMetrics       = renderqueue.RenderCompletionMetrics
)

var ErrJobExists = renderqueue.ErrJobExists
var ErrRenderRequestConflict = renderqueue.ErrRenderRequestConflict

const defaultQueuePollInterval = renderqueue.DefaultQueuePollInterval

func NewRenderQueueAsset(ref kernelasset.Ref, logicalPath, sourceURL string) RenderQueueAsset {
	return renderqueue.NewRenderQueueAsset(ref, logicalPath, sourceURL)
}

func marshalRenderingGenOverlayPlan(plan capmodel.OverlayPlan) ([]byte, error) {
	return renderqueue.MarshalRenderingGenOverlayPlan(plan)
}

func runtimeFontAssets(plan capmodel.OverlayPlan) ([]RenderQueueAsset, error) {
	return renderqueue.RuntimeFontAssets(plan)
}

type RenderAttemptAnalytics = renderqueue.RenderAttemptAnalytics
type RenderAttemptRecorder = renderqueue.RenderAttemptRecorder

func RenderRunID(jobID string) string {
	return renderqueue.RenderRunID(jobID)
}

func BuildRenderAttemptAnalytics(attemptID string, plan capmodel.OverlayPlan, artifact *contract.RenderArtifact) RenderAttemptAnalytics {
	return renderqueue.BuildRenderAttemptAnalytics(attemptID, plan, artifact)
}

func BuildRenderAttemptAnalyticsWithWait(attemptID string, plan capmodel.OverlayPlan, artifact *contract.RenderArtifact, wait RenderCompletionMetrics) RenderAttemptAnalytics {
	return renderqueue.BuildRenderAttemptAnalyticsWithWait(attemptID, plan, artifact, wait)
}
