package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// QueueRenderEnqueuer adapts the central RenderingGen queue for the Chronon
// overlay render path. It submits the SEMANTIC OverlayPlan; RenderingGen is the
// sole owner of the semantic→chronon.render-plan.v2 lowering, and blocks until
// the render completes, returning the certified artifact reference. The
// removed video render enqueue path is no longer part of PipelineGen.
//
// The wire contract it speaks (RenderQueueJob and the client capabilities) lives
// in render_queue_contract.go, so this file describes only the adapter.
// DynamicMapGPUAdmission is the cross-process GPU budget shared with
// RenderingGen. PipelineGen depends only on this admission contract, not the
// concrete lock implementation.
type DynamicMapGPUAdmission interface {
	Acquire(context.Context) (func(), error)
}

type QueueRenderEnqueuer struct {
	client       RenderQueueClient
	pollInterval time.Duration
	publisher    OverlayArtifactPublisher
	// freshRender forces each submission through this enqueuer to receive a
	// new queue identity. It is used for overlay artifacts: input assets are
	// reusable, but the rendered video must always be produced by a new
	// Chronon call. The flag is per-instance: only the enqueuer wired to the
	// overlay render path enables it; idempotent callers keep the default
	// plan-id identity and the ErrJobExists recovery path.
	freshRender bool
	// separateItemRenders enables one short artifact per semantic overlay item
	// after nearby items have been fused into chain items. Ordinary overlay plans
	// must not become a single full-duration video with sparse layers.
	separateItemRenders bool
	// overlayWindowBatching enables costed fusion of compatible same-scene
	// windows inside the per-item path. Dynamic maps remain individual work.
	overlayWindowBatching bool
	// itemRenderPool bounds how many per-item overlay renders may be in
	// flight at once. It exists because the per-item path used to submit a
	// child plan and block on its terminal state before submitting the next
	// one, so exactly one render was ever in flight and the GPU lane could
	// not overlap the per-item pre/post chain (materialize, upload, probe,
	// publish). Zero selects defaultSeparateItemRenderWorkers. The worker
	// remains the sole authority on how many of those renders touch the GPU
	// at once (worker.gpu_lanes), so this raises pipelining, not GPU load.
	itemRenderPool int
	// freshSeq is the per-instance monotonic counter that disambiguates the
	// fresh queue identity. It replaces the former package-level global so
	// concurrent enqueuers (and tests) cannot interfere with one another.
	freshSeq atomic.Uint64
	// recorder optionally persists one analytics row per completed attempt.
	// Nil means analytics are not recorded (no-op, not a failure).
	recorder RenderAttemptRecorder
	// renderCache is the content-addressed index from a per-item RenderKey to
	// the certified artifact it produced. Nil means the cache is not wired
	// (or was killed by OverlayRenderCacheKillSwitch); every path then behaves
	// exactly as before.
	renderCache OverlayRenderCache
	// renderCacheProber verifies a cached locator still resolves in the object
	// store before it is served as a completed render. Nil trusts the index.
	renderCacheProber OverlayArtifactProber
	// mapVideoCacheDir is the durable local cache for the certified dynamic-map
	// MP4, rooted under the configured storage data directory.
	mapVideoCacheDir string
	// mapGPUGate shares the cross-process RenderingGen GPU admission budget.
	// It is acquired only for a GPU sampler render; cache hits need no GPU slot.
	mapGPUGate DynamicMapGPUAdmission
	// renderCacheEpoch pins the deployment epoch mixed into cache keys. Empty
	// falls back to OverlayRenderCacheEpoch() (env override, then build
	// identity).
	renderCacheEpoch string
	// asyncPublication moves Drive publication and its dependent analytics out
	// of the render caller. RenderingGen has already certified immutable bytes
	// at this point; keeping this work on a bounded pool releases the render
	// worker while preserving a join before the run is marked complete.
	asyncPublication   bool
	publicationSem     chan struct{}
	publicationWorkers int
	// publicationMu guards the batch registry below. The pool itself is
	// process-wide (the composition root wires ONE enqueuer for the whole
	// process), but a publication belongs to exactly one run: joining, and
	// failing a run, must be scoped to that run's batch. A single shared
	// WaitGroup + error slot used to make run A join run B's in-flight
	// publications and inherit B's failure — a live run was failed by
	// ANOTHER run's overlay error (`context canceled` on a different
	// video), which is unactionable for the operator whose run died.
	publicationMu      sync.Mutex
	publicationBatches map[*kernobs.Run]*publicationBatch
	publicationUnbound *publicationBatch
}

// marshalRenderingGenOverlayPlan projects canonical microsecond item timing
// onto RenderingGen's strict millisecond wire contract.
// The publication batch type and its join helpers live in
// overlay_publication.go, next to the publication port they serve; the pool
// fields above belong to this type.

// EnqueueChrononPlan submits the semantic OverlayPlan to RenderingGen. The
// worker is the sole owner of the semantic→Chronon v2 compilation and writes
// the concrete plan it actually executes. Keeping this boundary semantic is
// important: sending PipelineGen's old v1 concrete document makes the worker
// validate it against the v2 schema and fail before Chronon starts.
func (e *QueueRenderEnqueuer) EnqueueChrononPlan(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	manifestCandidates, dedicatedCandidates := partitionOverlayRenderWork(plan)
	if len(manifestCandidates) == 0 {
		if len(dedicatedCandidates) == 0 {
			// Un solo percorso (TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE
			// §9.3.3 punto 2): non esiste più il terzo rendito legacy che
			// inviava l'intero piano grezzo alla coda. Un piano le cui voci
			// sono tutta canvas di background (template BACKGROUND/
			// VIDEO_BACKGROUND) è già dichiarato dalla SSOT del piano:
			// il background si dichiara nel blocco Background, non come item.
			return RenderReference{}, fmt.Errorf(
				"overlay plan %q has no renderable candidates: declare the background in the plan Background block, not as a legacy BACKGROUND item",
				plan.PlanID)
		}
		return e.enqueueDedicatedOverlayItems(ctx, plan, dedicatedCandidates)
	}
	return e.enqueueOverlayManifestTimeline(ctx, plan, manifestCandidates, dedicatedCandidates)
}
