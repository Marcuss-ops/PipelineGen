package scriptgeneration

import (
	"context"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderqueue"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"time"
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

// Queue adapter, cache and identity surfaces are phase-owned; the root keeps
// the public names used by wiring and by the runner.
type QueueRenderEnqueuer = renderqueue.QueueRenderEnqueuer
type QueuePrepareEnqueuer = renderqueue.QueuePrepareEnqueuer
type DynamicMapGPUAdmission = renderqueue.DynamicMapGPUAdmission
type OverlayRenderCache = renderqueue.OverlayRenderCache
type OverlayRenderCacheKey = renderqueue.OverlayRenderCacheKey
type OverlayArtifactProber = renderqueue.OverlayArtifactProber
type RenderRequestIdentity = renderqueue.RenderRequestIdentity

const RenderQueueStateCompleted = renderqueue.RenderQueueStateCompleted
const RenderQueueStateFailed = renderqueue.RenderQueueStateFailed
const RenderQueueStateCancelled = renderqueue.RenderQueueStateCancelled
const OverlayRenderCacheKillSwitch = renderqueue.OverlayRenderCacheKillSwitch
const OverlayRenderCacheEpochEnv = renderqueue.OverlayRenderCacheEpochEnv

var ErrRenderQueuePending = renderqueue.ErrRenderQueuePending

func NewQueueRenderEnqueuer(client RenderQueueClient) (*QueueRenderEnqueuer, error) {
	return renderqueue.NewQueueRenderEnqueuer(client)
}

func NewQueuePrepareEnqueuer(client RenderQueueClient) (*QueuePrepareEnqueuer, error) {
	return renderqueue.NewQueuePrepareEnqueuer(client)
}

func NewDynamicMapGPUAdmission() (DynamicMapGPUAdmission, error) {
	return renderqueue.NewDynamicMapGPUAdmission()
}

func WaitRenderQueueTerminal(ctx context.Context, client RenderQueueClient, id string, interval time.Duration) (RenderQueueJob, RenderCompletionMetrics, error) {
	return renderqueue.WaitRenderQueueTerminal(ctx, client, id, interval)
}

func WaitRenderQueueTerminalBounded(ctx context.Context, client RenderQueueClient, id string, interval, budget time.Duration) (RenderQueueJob, RenderCompletionMetrics, error) {
	return renderqueue.WaitRenderQueueTerminalBounded(ctx, client, id, interval, budget)
}

func RearmFailedRenderJob(ctx context.Context, client RenderQueueClient, id string) error {
	return renderqueue.RearmFailedRenderJob(ctx, client, id)
}

func RenderRequestIdentityFor(job RenderQueueJob) (RenderRequestIdentity, error) {
	return renderqueue.RenderRequestIdentityFor(job)
}

func OverlayRenderCacheEnabled() bool { return renderqueue.OverlayRenderCacheEnabled() }
func OverlayRenderCacheEpoch() string { return renderqueue.OverlayRenderCacheEpoch() }
