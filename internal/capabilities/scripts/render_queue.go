package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/background"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// QueueRenderEnqueuer adapts the central RenderingGen queue for the Chronon
// overlay render path. It submits the SEMANTIC OverlayPlan; RenderingGen is the
// sole owner of the semantic→chronon.render-plan.v2 lowering, and blocks until
// the render completes, returning the certified artifact reference. The
// removed video render enqueue path is no longer part of PipelineGen.
//
// The wire contract it speaks (RenderQueueJob and the client capabilities) lives
// in render_queue_contract.go, so this file describes only the adapter.
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
	// separateItemRenders is enabled by production composition. It changes
	// the overlay output contract from one full-timeline movie to one short
	// render per semantic item. Tests keep the legacy default unless they
	// explicitly opt into the item contract.
	separateItemRenders bool
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

// The publication batch type and its join helpers live in
// overlay_publication.go, next to the publication port they serve; the pool
// fields above belong to this type.

// Drive publication is outside the Chronon/GPU critical path and each
// artifact is independently idempotent. Six workers keep a multilingual
// overlay drain from becoming a serial tail while remaining conservative
// toward Drive/API rate limits; render admission is still owned by
// RenderingGen's gpu_lanes, not this pool.
const defaultOverlayPublicationWorkers = 6
const maxOverlayPublicationWorkers = 8

// NewQueueRenderEnqueuer creates a queue-backed Chronon render enqueuer.
func NewQueueRenderEnqueuer(client RenderQueueClient) (*QueueRenderEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue render enqueuer requires a queue client")
	}
	return &QueueRenderEnqueuer{client: client, pollInterval: defaultQueuePollInterval}, nil
}

// SetPollInterval tunes the observation cadence independently from the
// RenderingGen worker. A short interval removes avoidable tail latency after
// Chronon finishes; a caller may leave it unset to retain the safe default.
func (e *QueueRenderEnqueuer) SetPollInterval(interval time.Duration) {
	if e != nil && interval > 0 {
		e.pollInterval = interval
	}
}

// SetRecorder attaches the optional analytics recorder. Production
// composition injects the SQLite-backed recorder; tests may inject a fake or
// leave it nil to skip analytics.
func (e *QueueRenderEnqueuer) SetRecorder(r RenderAttemptRecorder) {
	if e == nil {
		return
	}
	e.recorder = r
}

// SetArtifactPublisher attaches the required post-render publication side
// effect. It is optional for unit-test compositions and enabled by the
// production composition root when Drive is configured.
func (e *QueueRenderEnqueuer) SetArtifactPublisher(p OverlayArtifactPublisher) {
	if e != nil {
		e.publisher = p
	}
}

// SetAsyncPublication enables the production publication pool. It is opt-in so
// small unit-test compositions retain the historical synchronous fail-closed
// behaviour unless they explicitly install the pool.
func (e *QueueRenderEnqueuer) SetAsyncPublication(on bool) {
	if e == nil {
		return
	}
	e.asyncPublication = on
	if on && e.publicationSem == nil {
		e.publicationSem = make(chan struct{}, e.publicationWorkerCount())
	}
}

// SetPublicationWorkers configures the bounded Drive/analytics publication
// pool. Production wiring calls this before SetAsyncPublication; changing a
// live semaphore would strand goroutines that already hold the old budget.
func (e *QueueRenderEnqueuer) SetPublicationWorkers(workers int) {
	if e == nil {
		return
	}
	if workers <= 0 {
		workers = defaultOverlayPublicationWorkers
	}
	if workers > maxOverlayPublicationWorkers {
		workers = maxOverlayPublicationWorkers
	}
	e.publicationWorkers = workers
}

func (e *QueueRenderEnqueuer) publicationWorkerCount() int {
	if e == nil || e.publicationWorkers <= 0 {
		return defaultOverlayPublicationWorkers
	}
	return e.publicationWorkers
}

// SetFreshRender controls whether EnqueueChrononPlan creates a new queue job
// for every call. Overlay production enables this so a completed job from an
// older test or retry can never be returned as the current render artifact.
// The default remains false for callers that explicitly rely on queue-level
// idempotency and ErrJobExists recovery. The returned RenderReference.JobID
// and the analytics attempt_id always carry the real queue job id.
func (e *QueueRenderEnqueuer) SetFreshRender(on bool) {
	if e != nil {
		e.freshRender = on
	}
}

// SetSeparateItemRenders makes production overlay output one video per
// entity/phrase. Each child plan is rendered on a local zero-based timeline;
// the source TTS timestamps remain in the publication receipt.
func (e *QueueRenderEnqueuer) SetSeparateItemRenders(on bool) {
	if e != nil {
		e.separateItemRenders = on
	}
}

// SetItemRenderPool tunes how many per-item overlay renders may be in flight
// at once. It is a PIPELINING bound, not a GPU bound: the RenderingGen worker
// owns `worker.gpu_lanes` and is the only authority on concurrent GPU work.
// A non-positive value restores the default.
func (e *QueueRenderEnqueuer) SetItemRenderPool(workers int) {
	if e != nil {
		e.itemRenderPool = workers
	}
}

// itemRenderWorkers resolves the per-item render pool size. It never returns
// less than 1, so every item is rendered exactly once even when the caller
// never opted in.
func (e *QueueRenderEnqueuer) itemRenderWorkers() int {
	if e != nil && e.itemRenderPool > 0 {
		return e.itemRenderPool
	}
	return defaultSeparateItemRenderWorkers
}

// EnqueueChrononPlan submits the semantic OverlayPlan to RenderingGen. The
// worker is the sole owner of the semantic→Chronon v2 compilation and writes
// the concrete plan it actually executes. Keeping this boundary semantic is
// important: sending PipelineGen's old v1 concrete document makes the worker
// validate it against the v2 schema and fail before Chronon starts.
func (e *QueueRenderEnqueuer) EnqueueChrononPlan(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	if e != nil && e.separateItemRenders && len(plan.Items) > 0 {
		return e.enqueueSeparateOverlayItems(ctx, plan)
	}
	return e.enqueueChrononPlan(ctx, plan, nil)
}

// EnqueueFinalJobCompositePlan renders a complete opaque scene composition in
// one Chronon job even when ordinary overlay production is split per item.
// These artifacts are finished scene footage consumed by the Master worker.
func (e *QueueRenderEnqueuer) EnqueueFinalJobCompositePlan(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	if len(plan.Items) == 0 {
		return RenderReference{}, fmt.Errorf("final-job composite plan has no overlay items")
	}
	return e.enqueueChrononPlan(ctx, plan, nil)
}

// RenderFinalJobComposite waits for both Chronon and the run-scoped Drive
// publication, so callers receive an asset the Master can resolve immediately.
func (e *QueueRenderEnqueuer) RenderFinalJobComposite(ctx context.Context, plan capoverlay.OverlayPlan) (RenderArtifact, error) {
	normalizedPlan, cleanup, err := normalizeFinalJobVideoBackground(ctx, plan)
	if err != nil {
		return RenderArtifact{}, fmt.Errorf("normalize final-job video background: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	ref, err := e.EnqueueFinalJobCompositePlan(ctx, normalizedPlan)
	if err != nil {
		return RenderArtifact{}, err
	}
	if err := e.Wait(ctx); err != nil {
		return RenderArtifact{}, fmt.Errorf("publish final-job composite: %w", err)
	}
	if ref.Artifact == nil {
		return RenderArtifact{}, fmt.Errorf("final-job composite render returned no artifact")
	}
	return *ref.Artifact, nil
}

func (e *QueueRenderEnqueuer) enqueueChrononPlan(ctx context.Context, plan capoverlay.OverlayPlan, metadata *overlayItemPublicationMetadata) (RenderReference, error) {
	if e == nil || e.client == nil {
		return RenderReference{}, fmt.Errorf("queue render enqueuer is not configured")
	}
	semanticPlan := plan
	// Drive routing belongs to PipelineGen's publication boundary, not to
	// RenderingGen's strict semantic overlay-plan wire contract. Keep it on
	// the in-memory/persisted plan for the publisher, but omit it from the
	// queue payload so the worker does not reject an application-only field.
	wirePlan := semanticPlan
	wirePlan.DriveFolderID = ""
	// SSOT: backgrounds are declared only through the plan's first-class
	// Background block. The legacy "item with template BACKGROUND" spelling
	// was removed — producers must set Background explicitly; the enqueue
	// boundary no longer rewrites the plan's items.
	spec, err := marshalRenderingGenOverlayPlan(wirePlan)
	if err != nil {
		return RenderReference{}, fmt.Errorf("marshal semantic chronon plan: %w", err)
	}
	assets := make([]RenderQueueAsset, 0, len(plan.Items)+1)
	seen := make(map[string]struct{})
	addAsset := func(ref capoverlay.OverlayAssetRef) {
		// Identity comes from the canonical kernel type: the digest is
		// canonicalised exactly once, by its owner, instead of by every caller
		// that happens to compare hashes.
		identity := ref.Ref()
		if !identity.HasContentAddress() {
			return
		}
		key := identity.DedupKey()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		sourceURL := strings.TrimSpace(ref.URL)
		logicalPath := semanticAssetLogicalPath(ref)
		// RenderingGen's compiled Chronon plan addresses assets by its
		// canonical semantic path. Send that same path in the queue manifest
		// and preserve the provider URL separately for worker self-healing.
		// Without this identity match, a fresh Drive image can be downloaded
		// successfully but still be looked up under a different workspace path.
		asset := NewRenderQueueAsset(identity, logicalPath, "")
		asset.LocalPath = ref.LocalPath
		if strings.HasPrefix(sourceURL, "http") {
			asset.SourceURL = sourceURL
		}
		assets = append(assets, asset)
	}
	if semanticPlan.Background != nil {
		for _, ref := range semanticPlan.Background.AssetRefs {
			addAsset(ref)
		}
	}
	if semanticPlan.Source != nil {
		addAsset(capoverlay.OverlayAssetRef{
			AssetID: semanticPlan.Source.AssetID, URL: semanticPlan.Source.Path,
			LocalPath: semanticPlan.Source.LocalPath, SHA256: semanticPlan.Source.SHA256,
			MediaType: "video/mp4",
		})
	}
	for _, item := range semanticPlan.Items {
		for _, ref := range item.AssetRefs {
			addAsset(ref)
		}
	}
	// Chronon's VisualPresetRegistry preflights the preset-owned font before
	// applying a layer's explicit font_asset override. Keep both content-
	// addressed fonts in the queue payload: the explicit PipelineGen font
	// remains authoritative for the layer, while Poppins satisfies the
	// registry dependency during plan preparation. DejaVuSans is also staged
	// because RenderingGen selects it for Cyrillic semantic plans.
	for _, item := range semanticPlan.Items {
		if item.Text != "" || item.TemplateID == "IMPORTANT_WORD" || item.TemplateID == "IMPORTANT_PHRASE" || item.TemplateID == "lower_third" {
			if _, ok := seen[capoverlay.GoldenPresetFontHash]; !ok {
				assets = append(assets, NewRenderQueueAsset(
					kernelasset.Ref{AssetID: capoverlay.CanonicalPresetFontPath, SHA256: capoverlay.GoldenPresetFontHash},
					capoverlay.CanonicalPresetFontPath, ""))
			}
			if _, ok := seen[capoverlay.GoldenFontHash]; !ok {
				assets = append(assets, NewRenderQueueAsset(
					kernelasset.Ref{AssetID: capoverlay.CanonicalTextFontPath, SHA256: capoverlay.GoldenFontHash},
					capoverlay.CanonicalTextFontPath, ""))
			}
			break
		}
	}
	// A payload-selected runtime font is a real Chronon dependency: its
	// workspace-relative path must be staged alongside the preset fonts before
	// the worker mounts the plan. The runtime family vocabulary is closed at
	// OverlayStyleSpec.Validate, and this mapping is the matching asset owner.
	assets = append(assets, runtimeFontAssets(semanticPlan)...)
	jobID := plan.PlanID
	if e.freshRender {
		jobID = fmt.Sprintf("%s:render:%d:%d", plan.PlanID, time.Now().UTC().UnixNano(), e.freshSeq.Add(1))
	}
	// Keep the queue dispatch explicit. RenderingGen uses this discriminator to
	// route the job through the overlay renderer (and to apply the overlay media
	// contract/ffprobe checks); omitting it silently falls back to the legacy
	// render_segment path.
	job := RenderQueueJob{
		ID:      jobID,
		JobType: capoverlay.JobTypeRender,
		// ParentJobID is the correlation id of the run that produced this
		// render (pkg/corid). It is the ONLY join key between a master run
		// and the remote queue entry: without it the queue record cannot be
		// traced back to the job that asked for the render. Empty when no
		// correlation id was propagated.
		ParentJobID: corid.FromContext(ctx),
		OverlaySpec: spec,
		Assets:      assets,
	}

	if err := e.client.Submit(ctx, job); err != nil {
		if e.freshRender {
			return RenderReference{}, fmt.Errorf("chronon queue fresh render submit failed: %w", err)
		}
		if errors.Is(err, ErrJobExists) {
			// The re-arm decision is owned by RearmFailedRenderJob (the same
			// helper the clip.render executor uses), and it fails closed when the
			// job is FAILED and cannot be re-armed.
			if rearmErr := RearmFailedRenderJob(ctx, e.client, plan.PlanID); rearmErr != nil {
				return RenderReference{}, fmt.Errorf("chronon queue render: %w", rearmErr)
			}
		} else {
			return RenderReference{}, fmt.Errorf("chronon queue render submit failed: %w", err)
		}
	}

	done, wait, err := e.waitForCompletion(ctx, jobID)
	if err != nil {
		return RenderReference{}, err
	}
	postRender := func(postCtx context.Context) error {
		if e.publisher != nil {
			if done.Artifact == nil || done.Artifact.SHA256 == "" || done.Artifact.SizeBytes <= 0 || done.Artifact.URL == "" {
				return fmt.Errorf("render job %s completed without certified artifact", jobID)
			}
			publication := OverlayPublicationSpec{
				ScriptName:               plan.ScriptName,
				Language:                 plan.Language,
				ProjectID:                plan.ProjectID,
				JobID:                    firstNonEmpty(plan.DriveJobID, plan.PlanID),
				PlanID:                   plan.PlanID,
				DriveFolderID:            plan.DriveFolderID,
				RequireDriveBeforeReturn: plan.RequireDriveBeforeReturn,
				CompletionWait:           wait.CompletionWait,
				PollingSleep:             wait.PollingSleep,
				PollingInterval:          wait.PollInterval,
				PollCount:                wait.PollCount,
			}
			if metadata != nil {
				if metadata.JobID != "" {
					publication.JobID = metadata.JobID
				}
				publication.OverlayItemID = metadata.ItemID
				publication.OverlayItemKind = metadata.ItemKind
				publication.OverlayEntityID = metadata.EntityID
				publication.OverlayEntityIDs = append([]string(nil), metadata.EntityIDs...)
				publication.OverlayEntityLabels = append([]string(nil), metadata.EntityLabels...)
				publication.OverlayText = metadata.Text
				publication.SourceStartUS = metadata.SourceStartUS
				publication.SourceEndUS = metadata.SourceEndUS
				publication.TargetDurationUS = metadata.TargetDurationUS
			}
			if err := e.publisher.PublishOverlay(postCtx, publication, done.Artifact); err != nil {
				return fmt.Errorf("publish overlay artifact to Drive: %w", err)
			}
		}
		if e.recorder != nil {
			// attempt_id is the analytics idempotency key: it must be the real
			// queue job id. In fresh mode that is the unique per-attempt identity,
			// so two renders of the same plan record two rows instead of upsert-
			// colliding on the plan id.
			// For the production per-item pool the item correlation is the child
			// plan's single item: overwrite that field with the item-aware
			// metadata so the analytics row answers "which overlay" rather than
			// "which plan carried one item".
			attempt := BuildRenderAttemptAnalyticsWithWait(jobID, plan, done.Artifact, wait)
			if metadata != nil && metadata.ItemID != "" {
				attempt.ItemID = metadata.ItemID
			}
			if err := e.recorder.RecordAttempt(postCtx, attempt); err != nil {
				return fmt.Errorf("record render attempt analytics: %w", err)
			}
		}
		return nil
	}
	if e.asyncPublication && !plan.RequireDriveBeforeReturn && (e.publisher != nil || e.recorder != nil) {
		// The batch is resolved on the SUBMITTING goroutine: the publication
		// must land in the batch of the run that asked for the render, not in
		// whatever context happens to still be alive when it finishes.
		batch := e.publicationBatchFor(ctx)
		batch.wg.Add(1)
		go func() {
			defer batch.wg.Done()
			e.publicationSem <- struct{}{}
			defer func() { <-e.publicationSem }()
			// The render run may be finalized while this bounded publication
			// worker is still draining Drive/analytics. Detach only the
			// publication context; Wait still joins this goroutine before the
			// run can become COMPLETE, so this is not fire-and-forget.
			publicationCtx, cancel := background.DetachWithTimeout(ctx, "overlay-publication", 30*time.Minute)
			defer cancel()
			if err := postRender(publicationCtx); err != nil {
				batch.record(err)
			}
		}()
	} else if err := postRender(ctx); err != nil {
		return RenderReference{}, err
	}
	// Map the RenderingGen worker's own phase timings into the canonical
	// run model instead of a new timing family: each reported phase becomes
	// one owner-measured operation on the run bound to ctx (the kernel
	// never re-times a phase the worker already measured).
	recordRenderingGenPhases(ctx, done.Artifact)
	return RenderReference{JobID: jobID, Status: "COMPLETED", Artifact: done.Artifact}, nil
}

// marshalRenderingGenOverlayPlan projects PipelineGen's canonical microsecond
// timing onto RenderingGen's strict overlay-plan.v1 wire contract. The two
// runtimes share start_ms/end_ms on the wire; start_us/duration_us remain
// internal to PipelineGen's frozen plan and must not cross this boundary.
func marshalRenderingGenOverlayPlan(plan capoverlay.OverlayPlan) ([]byte, error) {
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(wire["items"], &items); err != nil {
		return nil, err
	}
	for i, item := range items {
		var durationUS int64
		if encoded := item["duration_us"]; len(encoded) > 0 {
			if err := json.Unmarshal(encoded, &durationUS); err != nil {
				return nil, fmt.Errorf("decode item %d duration_us: %w", i, err)
			}
		}
		delete(item, "start_us")
		delete(item, "duration_us")
		if durationUS > 0 {
			var startMS, endMS int64
			if err := json.Unmarshal(item["start_ms"], &startMS); err != nil {
				return nil, fmt.Errorf("decode item %d start_ms: %w", i, err)
			}
			if err := json.Unmarshal(item["end_ms"], &endMS); err != nil {
				return nil, fmt.Errorf("decode item %d end_ms: %w", i, err)
			}
			if endMS <= startMS {
				return nil, fmt.Errorf("item %d has invalid millisecond timing %d-%d", i, startMS, endMS)
			}
			encoded, err := json.Marshal(endMS - startMS)
			if err != nil {
				return nil, err
			}
			item["duration_ms"] = encoded
		}
	}
	wireItems, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	wire["items"] = wireItems
	return json.Marshal(wire)
}

func runtimeFontAssets(plan capoverlay.OverlayPlan) []RenderQueueAsset {
	for _, item := range plan.Items {
		family, _ := item.Params["font_family"].(string)
		if strings.TrimSpace(family) == "inter" {
			return []RenderQueueAsset{NewRenderQueueAsset(
				kernelasset.Ref{AssetID: capoverlay.CanonicalInterFontPath, SHA256: capoverlay.CanonicalInterFontHash},
				capoverlay.CanonicalInterFontPath, "")}
		}
	}
	return nil
}

// RenderingGen queue job states (the `state` field of GET /jobs/{id}).
//
// The vocabulary is a WIRE FACT owned by the queue, so it is declared once here
// and every decision in this file derives from these constants. It used to be
// four raw literals across three functions (`terminalRenderState`,
// `terminalRenderResult` twice, `RearmFailedRenderJob`), kept aligned by a
// comment: a rename on the queue side would have silently stopped matching —
// the poll loop would never see terminal and the re-arm would never fire.
const (
	// RenderQueueStateCompleted — terminal success; the only state that yields
	// a render result.
	RenderQueueStateCompleted = "completed"
	// RenderQueueStateFailed — terminal failure carrying FailReason.
	RenderQueueStateFailed = "failed"
	// RenderQueueStateCancelled — terminal cancellation, reported as a failure
	// with its own reason (never as a completed render).
	RenderQueueStateCancelled = "cancelled"
)

// terminalRenderState reports whether state is a terminal render state. The
// canonical terminal set is completed | failed | cancelled and MUST stay
// aligned with the RenderQueueWaiter contract above; a terminal state that is
// not recognised here would either be treated as success or poll forever.
func terminalRenderState(state string) bool {
	switch state {
	case RenderQueueStateCompleted, RenderQueueStateFailed, RenderQueueStateCancelled:
		return true
	default:
		return false
	}
}

// terminalRenderResult maps a terminal job to the enqueuer's return value.
// Only "completed" is a success: a failed job surfaces its reason, and a
// cancelled job fails closed with its own reason rather than being reported as
// a completed render (which would surface downstream as the misleading
// "completed without certified artifact" error).
//
// An UNRECOGNISED terminal state fails closed. The previous default branch
// returned a nil error, so any terminal state added by a newer queue (a
// timeout, a preemption, a partial) would have been reported to the caller as a
// successful render with no artifact — the flow declaring success without
// having completed. The caller responds to a terminal state by fetching the
// certified artifact, so "I do not know this state" must never be the answer
// that skips that step.
func terminalRenderResult(job RenderQueueJob, id string, metrics RenderCompletionMetrics) (RenderQueueJob, RenderCompletionMetrics, error) {
	switch job.State {
	case RenderQueueStateCompleted:
		return job, metrics, nil
	case RenderQueueStateFailed:
		reason := job.FailReason
		if reason == "" {
			reason = "unknown failure"
		}
		return job, metrics, fmt.Errorf("render job %s failed: %s", id, reason)
	case RenderQueueStateCancelled:
		reason := job.FailReason
		if reason == "" {
			reason = "unknown reason"
		}
		return job, metrics, fmt.Errorf("render job %s cancelled: %s", id, reason)
	default:
		return job, metrics, fmt.Errorf("render job %s reached unrecognised terminal state %q", id, job.State)
	}
}
