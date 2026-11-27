package renderinggen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

// The queue seam for clip rendering is the CANONICAL capability port
// (renderqueue.RenderQueueClient, with renderqueue.RenderQueueWaiter and
// renderqueue.RenderQueueRetrier as its optional capabilities). This file used to
// declare a second, narrower port (ClipRenderQueue: Submit + Get + Retry) for
// the same remote service — one fact with two owners, so "what the queue can
// do" had two answers that could drift, and the retry-recovery rule had a third
// copy at each call site. The port is owned by the capability that consumes it;
// this package only implements it (*Client satisfies all three).

// ClipRenderExecutor submits one complete clip segment to RenderingGen. The
// RenderingGen worker is the sole clip-rendering boundary; it lowers the
// semantic plan and executes it with Chronon. This client never invokes a
// local renderer and fails closed on missing or non-Chronon artifacts.
type ClipRenderExecutor struct {
	queue          renderqueue.RenderQueueClient
	capabilities   RenderCapabilitySource
	interval       time.Duration
	chunkDuration  time.Duration
	chunkAlignment int64
	chunkMax       int
}

// RenderCapabilitySource is the live worker inventory used by the producer
// preflight. *Client satisfies it; a composition root must attach it so a
// clip render is refused before enqueue when no worker proves the GPU lane.
// The type is deliberately narrow: the executor must not reach through the
// queue client for anything else.
type RenderCapabilitySource interface {
	Capabilities(ctx context.Context) ([]string, error)
}

// SetCapabilitySource attaches the live inventory used by the GPU preflight.
// Nil disables the preflight (unit-test seams); production wiring must set it.
func (e *ClipRenderExecutor) SetCapabilitySource(source RenderCapabilitySource) *ClipRenderExecutor {
	if e != nil {
		e.capabilities = source
	}
	return e
}

// requireGPUInventory fails closed when the source is attached and no live
// worker advertises the GPU capability. clip.render is Chronon-Vulkan only
// (the completion gate rejects every other backend), so enqueueing it as
// gpu_preferred only wastes a claim on a lane that cannot finish it.
func (e *ClipRenderExecutor) requireGPUInventory(ctx context.Context) error {
	if e == nil || e.capabilities == nil {
		return nil
	}
	available, err := e.capabilities.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("EXECUTION_POLICY_INCOMPATIBLE: read live RenderingGen capabilities: %w", err)
	}
	for _, capability := range available {
		if strings.TrimSpace(capability) == renderqueue.CapabilityGPU {
			return nil
		}
	}
	return fmt.Errorf("EXECUTION_POLICY_INCOMPATIBLE: clip render requires the %q capability but no live RenderingGen worker advertises it", renderqueue.CapabilityGPU)
}

func NewClipRenderExecutor(queue renderqueue.RenderQueueClient) (*ClipRenderExecutor, error) {
	if queue == nil {
		return nil, fmt.Errorf("renderinggen clip executor: queue client is required")
	}
	return &ClipRenderExecutor{queue: queue, interval: 500 * time.Millisecond}, nil
}

func (e *ClipRenderExecutor) SetPollInterval(interval time.Duration) *ClipRenderExecutor {
	if e != nil && interval > 0 {
		e.interval = interval
	}
	return e
}

// SetChunking enables the conservative production chunk policy for long,
// source-only clips. Composition plans remain exclusive.
//
// AUDIO-AWARE ASSEMBLY: a family splits the video across video-only children
// and keeps ONE audio owner — the anchor muxes the source stream exactly once
// over the assembled video — so audio-carrying clip plans are chunkable. See
// chunkPolicy and docs/operations/TICKET-CHUNK-AUDIO-AWARE-ASSEMBLY.md.
func (e *ClipRenderExecutor) SetChunking(chunkDuration time.Duration, alignmentFrames int64, chunkMax int) *ClipRenderExecutor {
	if e == nil {
		return e
	}
	if chunkDuration > 0 && alignmentFrames > 0 && chunkMax > 1 {
		e.chunkDuration = chunkDuration
		e.chunkAlignment = alignmentFrames
		e.chunkMax = chunkMax
	}
	return e
}

// RenderJobID returns the durable remote address for this plan. Plain renders
// use RunID; chunk families use their content-addressed assembly anchor.
func (e *ClipRenderExecutor) RenderJobID(plan cliprender.ClipRenderPlanV1) string {
	if requested, alignment, ok := e.chunkPolicy(plan); ok {
		if set, err := cliprender.BuildChunkSet(plan, requested, alignment); err == nil {
			return set.AnchorJobID()
		}
	}
	return plan.RunID
}

// ClipRenderExecutor implements cliprender.RenderExecutor +
// RenderExecutorWithJobID + the OPTIONAL RenderExecutorDeferring, which is what
// lets the settle continuation be re-dispatched instead of pinned.
var (
	_ cliprender.RenderExecutor          = (*ClipRenderExecutor)(nil)
	_ cliprender.RenderExecutorWithJobID = (*ClipRenderExecutor)(nil)
	_ cliprender.RenderExecutorDeferring = (*ClipRenderExecutor)(nil)
)

// jsonEquivalent compares two JSON documents semantically. The queue may
// re-serialise a stored plan (jsonb key order/whitespace), so a byte compare
// would reject a rerun whose bytes are identical in meaning. Malformed JSON is
// never equivalent: the recovery must not admit what it cannot prove.
func jsonEquivalent(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}
