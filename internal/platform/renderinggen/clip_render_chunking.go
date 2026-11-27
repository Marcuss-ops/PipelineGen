package renderinggen

import (
	"context"
	"errors"
	"fmt"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// chunkPolicy is transport optimisation only: a single source-video graph is
// safe for packet-copy assembly. Watermarks, burned subtitles, backgrounds,
// overlays and non-full-frame scaling require FullGraph and remain exclusive.
func (e *ClipRenderExecutor) chunkPolicy(plan cliprender.ClipRenderPlanV1) (int, int64, bool) {
	if e == nil || e.chunkDuration <= 0 || e.chunkAlignment <= 0 || e.chunkMax < 2 {
		return 0, 0, false
	}
	// AUDIO-AWARE ASSEMBLY (pinned by TestChunkPolicyChunksAudioPlans and
	// TestChunkProducerChildrenAreVideoOnlyAndAnchorOwnsAudio): audio is NOT a
	// policy blocker. Children render the plan's video-only mapping — silent,
	// copy-eligible segments the packet-copy assembly concatenates at
	// keyframe-aligned boundaries — and the family anchor carries the full
	// plan document so the finalizer muxes the source audio exactly once over
	// the assembled video. Audio packet boundaries therefore never meet a
	// window boundary; the audio contract travels in the anchor's plan, not in
	// every child's timeline. (Historical note: this guard used to refuse every
	// plan with audio, which — with audio mandatory on every sealed plan — made
	// the whole chunk path dormant; see
	// docs/operations/TICKET-CHUNK-AUDIO-AWARE-ASSEMBLY.md.)
	if plan.Background != nil && plan.Background.Mode != "" && plan.Background.Mode != cliprender.BackgroundModeNone {
		return 0, 0, false
	}
	if plan.Watermark != nil || plan.Overlay != nil {
		return 0, 0, false
	}
	if plan.Subtitles != nil && plan.Subtitles.Mode != "sidecar" {
		return 0, 0, false
	}
	if plan.Output.ForegroundScalePercent != 0 && plan.Output.ForegroundScalePercent != 100 {
		return 0, 0, false
	}
	chunkMS := e.chunkDuration.Milliseconds()
	if chunkMS <= 0 || plan.DurationMS <= chunkMS {
		return 0, 0, false
	}
	requested := int((plan.DurationMS + chunkMS - 1) / chunkMS)
	if requested > e.chunkMax {
		requested = e.chunkMax
	}
	if requested < 2 {
		return 0, 0, false
	}
	return requested, e.chunkAlignment, true
}

// SubmitChunked is the explicit I1 opt-in. It requires the queue's atomic
// batch capability and therefore cannot silently downgrade to N independent
// submissions (which would reintroduce the anchor-claim race).
func (e *ClipRenderExecutor) SubmitChunked(ctx context.Context, plan cliprender.ClipRenderPlanV1, requestedChunks int, alignmentFrames int64) (cliprender.ChunkSet, error) {
	if e == nil || e.queue == nil {
		return cliprender.ChunkSet{}, fmt.Errorf("%w: RenderingGen queue is not configured", cliprender.ErrBackendUnavailable)
	}
	batch, ok := e.queue.(renderqueue.RenderQueueBatchSubmitter)
	if !ok {
		return cliprender.ChunkSet{}, fmt.Errorf("renderinggen clip executor: queue does not support atomic chunk families")
	}
	producer, err := NewChunkProducer(batch)
	if err != nil {
		return cliprender.ChunkSet{}, err
	}
	set, err := cliprender.BuildChunkSet(plan, requestedChunks, alignmentFrames)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("renderinggen clip executor: plan chunks: %w", err)
	}
	if _, err = producer.Submit(ctx, plan, requestedChunks, alignmentFrames); err == nil {
		return set, nil
	}
	if !errors.Is(err, renderqueue.ErrJobExists) {
		return cliprender.ChunkSet{}, err
	}
	if rearmErr := rearmChunkFamily(ctx, e.queue, set.AnchorJobID(), set.Chunks); rearmErr != nil {
		return cliprender.ChunkSet{}, rearmErr
	}
	return set, nil
}

func rearmChunkFamily(ctx context.Context, queue renderqueue.RenderQueueClient, anchorID string, chunks []cliprender.Chunk) error {
	childrenReader, ok := queue.(renderqueue.RenderQueueChildrenReader)
	if !ok {
		return fmt.Errorf("renderinggen chunk family already exists but queue cannot inspect children")
	}
	retrier, ok := queue.(renderqueue.RenderQueueRetrier)
	if !ok {
		return fmt.Errorf("renderinggen chunk family already exists but queue cannot retry failed members")
	}
	parent, err := queue.Get(ctx, anchorID)
	if err != nil {
		return fmt.Errorf("renderinggen chunk family parent: %w", err)
	}
	if parent.State == string(queueclient.StateFailed) {
		return fmt.Errorf("renderinggen chunk family anchor %s is failed; finalizer retry requires operator action", parent.ID)
	}
	children, err := childrenReader.Children(ctx, anchorID)
	if err != nil {
		return err
	}
	if len(children) != len(chunks) {
		return fmt.Errorf("renderinggen chunk family has %d children, want %d", len(children), len(chunks))
	}
	for _, child := range children {
		if child.State != string(queueclient.StateFailed) {
			continue
		}
		if err := retrier.Retry(ctx, child.ID); err != nil {
			return fmt.Errorf("renderinggen retry chunk %s: %w", child.ID, err)
		}
	}
	return nil
}
