package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/background"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
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
	// separateItemRenders enables one short artifact per semantic overlay item
	// after nearby items have been fused into chain items. Ordinary overlay plans
	// must not become a single full-duration video with sparse layers.
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
	// Nearby semantic items have already been coalesced into chain items by the
	// planner. Render each resulting item/chain as its own short MP4. Rendering
	// the entire plan here creates one sparse video spanning the full narration.
	// Maps force the per-item route as well: each map item then folds its own
	// dynamic render exactly like a single-map plan, so the backend and the
	// presentation never depend on the plan's item count.
	if e != nil && len(plan.Items) > 0 && (e.separateItemRenders || planNeedsPerItemMapRenders(plan)) {
		return e.enqueueSeparateOverlayItems(ctx, plan)
	}
	return e.enqueueChrononPlan(ctx, plan, nil)
}

// planMapBackend is one map item's render-boundary backend decision: the
// capability-resolved backend plus the reason recorded for the map decision
// report. resolvePlanMapBackends is the SINGLE owner of this decision — the
// per-item routing predicate and the queue annotation below both consume its
// output, so the route a map takes and the backend stamped on it can never
// disagree (the P0 rule: one decision, made per map from its own
// requirements, never from the plan's item count).
type planMapBackend struct {
	itemID  string
	backend scriptoverlay.MapBackend
	reason  string
}

// resolvePlanMapBackends resolves every map item's backend from its own real
// render requirements. It fails closed on the first unresolvable item.
func resolvePlanMapBackends(plan capoverlay.OverlayPlan) ([]planMapBackend, error) {
	nativeDiagnostic := os.Getenv("PIPELINEGEN_MAP_NATIVE_RENDER") == "1"
	var decisions []planMapBackend
	for i := range plan.Items {
		if plan.Items[i].Map == nil {
			continue
		}
		backend, reason, err := scriptoverlay.ResolveMapItemBackend(plan.Items[i], nativeDiagnostic)
		if err != nil {
			return nil, fmt.Errorf("resolve map backend for item %q: %w", plan.Items[i].ID, err)
		}
		decisions = append(decisions, planMapBackend{itemID: plan.Items[i].ID, backend: backend, reason: reason})
	}
	return decisions, nil
}

// annotatePlanMapBackends stamps the single backend decision onto every map
// item, so the worker executes exactly the backend the capability resolution
// chose. It is the second half of the single decision (resolve, then
// annotate) — callers never re-resolve per item.
func annotatePlanMapBackends(plan *capoverlay.OverlayPlan, decisions []planMapBackend) {
	if plan == nil {
		return
	}
	byItem := make(map[string]planMapBackend, len(decisions))
	for _, decision := range decisions {
		byItem[decision.itemID] = decision
	}
	for itemIndex := range plan.Items {
		if plan.Items[itemIndex].Map == nil {
			continue
		}
		decision, ok := byItem[plan.Items[itemIndex].ID]
		if !ok {
			continue
		}
		if plan.Items[itemIndex].Params == nil {
			plan.Items[itemIndex].Params = map[string]any{}
		}
		plan.Items[itemIndex].Params["map_backend"] = string(decision.backend)
		plan.Items[itemIndex].Params["map_backend_reason"] = decision.reason
	}
}

// planNeedsPerItemMapRenders reports whether the plan holds map content that
// must render through the dynamic per-item route: a python-backend map in a
// plan that is not the single foldable map item. It consumes the single
// backend decision above, so routing and annotation always agree.
func planNeedsPerItemMapRenders(plan capoverlay.OverlayPlan) bool {
	nativeDiagnostic := os.Getenv("PIPELINEGEN_MAP_NATIVE_RENDER") == "1"
	if nativeDiagnostic {
		return false
	}
	decisions, err := resolvePlanMapBackends(plan)
	if err != nil {
		return true
	}
	pythonMaps := 0
	for _, decision := range decisions {
		if decision.backend == scriptoverlay.MapBackendPython {
			pythonMaps++
		}
	}
	if pythonMaps > 0 && len(plan.Items) > 1 {
		return true
	}
	return len(decisions) > 1
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
	var generatedMapVideo string
	// Map backend selection is a PER-MAP capability decision made at the render
	// boundary (the P0 rule): every map item resolves its backend from its own
	// render requirements, identically whether the plan holds one map or
	// several. Dynamic map overlays use ChrononTemplate's live tile-pyramid
	// renderer by default. The native MAP compiler treats the basemap as one
	// raster plane; its camera transform looks like a zoomed screenshot and is
	// not an acceptable substitute for a cartographic fly-in. The native path
	// remains an explicit diagnostic opt-in (PIPELINEGEN_MAP_NATIVE_RENDER=1)
	// until it can produce equivalent motion.
	decisions, err := resolvePlanMapBackends(semanticPlan)
	if err != nil {
		return RenderReference{}, err
	}
	mapItemCount := len(decisions)
	pythonMapCount := 0
	for _, decision := range decisions {
		if decision.backend == scriptoverlay.MapBackendPython {
			pythonMapCount++
		}
	}
	annotatePlanMapBackends(&semanticPlan, decisions)
	nativeDiagnostic := os.Getenv("PIPELINEGEN_MAP_NATIVE_RENDER") == "1"
	if mapItemCount == 1 && pythonMapCount == 1 && len(semanticPlan.Items) == 1 && !nativeDiagnostic {
		// The plan is ONE dynamic map: fold its rendered video into the
		// first-class background block (the certified single-map production
		// path). Multi-map plans take the per-item route in
		// EnqueueChrononPlan, so a map is rendered the same way regardless of
		// how many maps the plan holds.
		videoPath, err := renderDynamicMapVideo(ctx, semanticPlan)
		if err != nil {
			return RenderReference{}, fmt.Errorf("render Chronon dynamic map: %w", err)
		}
		generatedMapVideo = videoPath
		defer func() { _ = os.RemoveAll(filepath.Dir(generatedMapVideo)) }()
		sha, size, err := digest.SHA256File(videoPath)
		if err != nil {
			return RenderReference{}, fmt.Errorf("identify Chronon dynamic map video: %w", err)
		}
		if size == 0 {
			return RenderReference{}, fmt.Errorf("dynamic map video is empty")
		}
		assetRef := capoverlay.NewOverlayAssetRef(
			kernelasset.Ref{AssetID: "chronon:dynamic-map:" + sha, SHA256: sha, MediaType: "video/mp4"},
			"", videoPath,
		)
		semanticPlan.Background = &capoverlay.OverlayBackground{Kind: "video", AssetRefs: []capoverlay.OverlayAssetRef{assetRef}, Fit: "cover"}
		semanticPlan.Items = nil
	}
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
		if item.SoundEffect != nil {
			addAsset(item.SoundEffect.AssetRef)
		}
	}
	// Chronon's VisualPresetRegistry preflights the preset-owned font before
	// applying a layer's explicit font_asset override. Keep both content-
	// addressed fonts in the queue payload: the explicit PipelineGen font
	// remains authoritative for the layer, while Poppins satisfies the
	// registry dependency during plan preparation. DejaVuSans is also staged
	// because RenderingGen selects it for Cyrillic semantic plans.
	for _, item := range semanticPlan.Items {
		// Image presets may build their own text layer for entity captions even
		// when the source overlay has no standalone Text field. Stage the preset
		// fonts for those items too so the runtime can compile that caption.
		if item.Text != "" || item.TemplateID == "IMPORTANT_WORD" || item.TemplateID == "IMPORTANT_PHRASE" || item.TemplateID == "lower_third" || isImageOverlayItem(item) || item.EntityCaption != "" || len(item.ImageLayers) > 0 {
			if _, ok := seen[caprender.GoldenPresetFontHash]; !ok {
				assets = append(assets, NewRenderQueueAsset(
					kernelasset.Ref{AssetID: caprender.CanonicalPresetFontPath, SHA256: caprender.GoldenPresetFontHash},
					caprender.CanonicalPresetFontPath, ""))
			}
			if _, ok := seen[caprender.GoldenFontHash]; !ok {
				assets = append(assets, NewRenderQueueAsset(
					kernelasset.Ref{AssetID: caprender.CanonicalTextFontPath, SHA256: caprender.GoldenFontHash},
					caprender.CanonicalTextFontPath, ""))
			}
			break
		}
	}
	// A payload-selected runtime font is a real Chronon dependency: its
	// workspace-relative path must be staged alongside the preset fonts before
	// the worker mounts the plan. The runtime family vocabulary is closed at
	// OverlayStyleSpec.Validate, and this mapping is the matching asset owner.
	fontAssets, err := runtimeFontAssets(semanticPlan)
	if err != nil {
		return RenderReference{}, fmt.Errorf("stage selected runtime fonts: %w", err)
	}
	assets = append(assets, fontAssets...)
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

	submitStartedAt := time.Now()
	submitErr := e.client.Submit(ctx, job)
	if submitErr != nil {
		if e.freshRender || !errors.Is(submitErr, ErrJobExists) {
			message := "chronon queue render submit failed"
			if e.freshRender {
				message = "chronon queue fresh render submit failed"
			}
			recordErr := e.recordQueueAttempt(ctx, jobID, plan, metadata, RenderCompletionMetrics{}, submitStartedAt, time.Time{}, nil, time.Time{}, "failure")
			return RenderReference{}, errors.Join(fmt.Errorf("%s: %w", message, submitErr), recordErr)
		}
		// The re-arm decision is owned by RearmFailedRenderJob (the same
		// helper the clip.render executor uses), and it fails closed when the
		// job is FAILED and cannot be re-armed.
		if rearmErr := RearmFailedRenderJob(ctx, e.client, plan.PlanID); rearmErr != nil {
			recordErr := e.recordQueueAttempt(ctx, jobID, plan, metadata, RenderCompletionMetrics{}, submitStartedAt, time.Time{}, nil, time.Time{}, "failure")
			return RenderReference{}, errors.Join(fmt.Errorf("chronon queue render: %w", rearmErr), recordErr)
		}
	}
	submitAcceptedAt := time.Now()
	done, wait, err := e.waitForCompletion(ctx, jobID)
	if err != nil {
		observeCompletionWait(wait, renderOutcomeFailure)
		recordErr := e.recordQueueAttempt(ctx, jobID, plan, metadata, wait, submitStartedAt, submitAcceptedAt, &done, time.Time{}, "failure")
		return RenderReference{}, errors.Join(err, recordErr)
	}
	observeCompletionWait(wait, renderOutcomeSuccess)
	artifactAvailableAt := time.Now()
	postRender := func(postCtx context.Context) (postErr error) {
		defer func() {
			if e.recorder == nil {
				return
			}
			recordErr := e.recordQueueAttempt(postCtx, jobID, plan, metadata, wait, submitStartedAt, submitAcceptedAt, &done, artifactAvailableAt, renderOutcomeSuccess)
			if recordErr != nil {
				postErr = errors.Join(postErr, fmt.Errorf("record render attempt analytics: %w", recordErr))
			}
		}()
		if e.publisher != nil {
			if done.Artifact == nil || done.Artifact.SHA256 == "" || done.Artifact.SizeBytes <= 0 || done.Artifact.URL == "" {
				return fmt.Errorf("render job %s completed without certified artifact", jobID)
			}
			publication := OverlayPublicationSpec{
				ScriptName:               plan.ScriptName,
				Language:                 plan.Language,
				ProjectID:                plan.ProjectID,
				JobID:                    textutil.FirstNonEmpty(plan.DriveJobID, plan.PlanID),
				ResultJobID:              textutil.FirstNonEmpty(plan.ResultJobID, plan.DriveJobID),
				PlanID:                   plan.PlanID,
				DriveFolderID:            plan.DriveFolderID,
				RequireDriveBeforeReturn: plan.RequireDriveBeforeReturn,
				CompletionWait:           wait.CompletionWait,
				PollingSleep:             wait.PollingSleep,
				PollingInterval:          wait.PollInterval,
				PollCount:                wait.PollCount,
			}
			if metadata != nil {
				if publication.JobID == "" && metadata.JobID != "" {
					publication.JobID = metadata.JobID
				}
				if publication.ResultJobID == "" && metadata.ResultJobID != "" {
					publication.ResultJobID = metadata.ResultJobID
				}
				publication.OverlayItemID = metadata.ItemID
				publication.OverlayItemKind = metadata.ItemKind
				publication.OverlayMotionID = metadata.MotionID
				publication.OverlayEntityID = metadata.EntityID
				publication.OverlayEntityName = metadata.EntityName
				publication.OverlayEntityIDs = append([]string(nil), metadata.EntityIDs...)
				publication.OverlayEntityLabels = append([]string(nil), metadata.EntityLabels...)
				publication.OverlayText = metadata.Text
				publication.SourceStartUS = metadata.SourceStartUS
				publication.SourceEndUS = metadata.SourceEndUS
				publication.TargetDurationUS = metadata.TargetDurationUS
			}
			if err := kernobs.MeasureOperation(postCtx, kernobs.OperationInfo{
				Stage: StageOverlayRender, Component: "drive", Operation: "overlay_upload", Provider: "drive",
			}, func(measureCtx context.Context) error {
				return e.publisher.PublishOverlay(measureCtx, publication, done.Artifact)
			}); err != nil {
				return fmt.Errorf("publish overlay artifact to Drive: %w", err)
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
	// never re-times a phase the worker already measured). The boundary the
	// CALLER owns is projected alongside them, so the stage wall decomposes
	// as submit + wait + Σ worker phases.
	recordRenderingGenPhases(ctx, done.Artifact)
	recordQueueBoundaryPhases(ctx, submitAcceptedAt.Sub(submitStartedAt).Milliseconds(), wait.CompletionWait.Milliseconds())
	return RenderReference{JobID: jobID, Status: "COMPLETED", Artifact: done.Artifact}, nil
}
