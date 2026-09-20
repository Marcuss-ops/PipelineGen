package renderinggen

// chunk_producer.go owns the submit half of I1. The deterministic partition
// lives in cliprender; this adapter lowers one sealed plan into an atomic
// RenderingGen anchor+children family. It is opt-in until Chronon's parallel
// admission policy is certified on the target GPU.

import (
	"context"
	"fmt"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// ChunkProducer creates a queue family in one transaction. The anchor is
// assembly-only: it owns the children and is never claimable by a render
// worker; the worker-side finalizer claims it after all chunks certify.
type ChunkProducer struct {
	submitter scriptgen.RenderQueueBatchSubmitter
}

func NewChunkProducer(submitter scriptgen.RenderQueueBatchSubmitter) (*ChunkProducer, error) {
	if submitter == nil {
		return nil, fmt.Errorf("chunk producer: atomic batch submitter is required")
	}
	return &ChunkProducer{submitter: submitter}, nil
}

// Submit partitions and submits a plan. alignmentFrames must be the certified
// output GOP/keyframe interval; the planner rejects invalid alignment rather
// than creating assembly-unsafe boundaries.
func (p *ChunkProducer) Submit(ctx context.Context, plan cliprender.ClipRenderPlanV1, requestedChunks int, alignmentFrames int64) (cliprender.ChunkSet, error) {
	if p == nil || p.submitter == nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: not configured")
	}
	if err := plan.Validate(); err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: validate plan: %w", err)
	}
	set, err := cliprender.BuildChunkSet(plan, requestedChunks, alignmentFrames)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: plan chunks: %w", err)
	}
	rawPlan, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: map plan: %w", err)
	}
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: asset refs: %w", err)
	}
	if err := prefetchClipAssets(ctx, plan, refs); err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: prefetch assets: %w", err)
	}
	assets := make([]scriptgen.RenderQueueAsset, len(refs))
	for i, ref := range refs {
		assets[i] = scriptgen.NewRenderQueueAsset(
			kernelasset.Ref{SHA256: ref.Hash, AssetID: ref.LogicalPath},
			ref.LogicalPath, "")
	}
	anchorID := set.AnchorJobID()
	family := make([]scriptgen.RenderQueueJob, 0, len(set.Chunks)+1)
	family = append(family, scriptgen.RenderQueueJob{
		ID: anchorID, JobType: queueclient.JobTypeRenderSegment,
		OverlaySpec: rawPlan, Assets: assets,
	})
	for _, chunk := range set.Chunks {
		family = append(family, scriptgen.RenderQueueJob{
			ID: chunk.JobID, JobType: queueclient.JobTypeRenderSegment,
			ParentJobID: anchorID, ChunkIndex: chunk.Index,
			FrameRange:  &scriptgen.RenderFrameRange{Start: chunk.StartFrame, End: chunk.EndFrame},
			OverlaySpec: rawPlan, Assets: assets,
		})
	}
	if err := p.submitter.SubmitBatch(ctx, family); err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: submit family: %w", err)
	}
	return set, nil
}
