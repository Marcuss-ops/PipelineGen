package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/background"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func (e *QueueRenderEnqueuer) enqueueChrononPlan(ctx context.Context, plan capoverlay.OverlayPlan, metadata *overlayItemPublicationMetadata) (RenderReference, error) {
	if e == nil || e.client == nil {
		return RenderReference{}, fmt.Errorf("queue render enqueuer is not configured")
	}
	if plan.RequireDriveBeforeReturn && e.publisher == nil {
		return RenderReference{}, fmt.Errorf("render plan requires Drive delivery but no overlay publisher is configured")
	}
	// A dynamic-map import attests both PlanID and VideoID. Plans built by the
	// single-item path already fall back to PlanID; normalizeDynamicMapFoldPlan
	// applies the same stable identity here so a plan that predates VideoID
	// stamping still produces a complete import attestation instead of failing
	// at the worker boundary. The PREPARE phase normalizes identically
	// (PreMaterializeDynamicMapVideo), so both sides resolve one cache key.
	semanticPlan, decisions, err := normalizeDynamicMapFoldPlan(plan)
	if err != nil {
		return RenderReference{}, err
	}
	var generatedMapVideo string
	var dynamicMapImportSpec []byte
	var dynamicMapImportSHA string
	// Map backend selection is a PER-MAP capability decision made at the render
	// boundary (the P0 rule): every map item resolves its backend from its own
	// render requirements, identically whether the plan holds one map or
	// several. Dynamic map overlays use ChrononTemplate's live tile-pyramid
	// renderer by default. The native MAP compiler treats the basemap as one
	// raster plane; its camera transform looks like a zoomed screenshot and is
	// not an acceptable substitute for a cartographic fly-in. The native path
	// remains an explicit diagnostic opt-in (PIPELINEGEN_MAP_NATIVE_RENDER=1)
	// until it can produce equivalent motion.
	mapItemCount := len(decisions)
	pythonMapCount := 0
	for _, decision := range decisions {
		if decision.backend == scriptoverlay.MapBackendDynamic {
			pythonMapCount++
		}
	}
	nativeDiagnostic := os.Getenv("PIPELINEGEN_MAP_NATIVE_RENDER") == "1"
	if mapItemCount == 1 && pythonMapCount == 1 && len(semanticPlan.Items) == 1 && !nativeDiagnostic {
		// The plan is ONE dynamic map: fold its rendered video into the
		// first-class background block (the certified single-map production
		// path). Multi-map plans take the per-item route in
		// EnqueueChrononPlan, so a map is rendered the same way regardless of
		// how many maps the plan holds.
		videoPath, err := e.renderDynamicMapVideo(ctx, semanticPlan)
		if err != nil {
			return RenderReference{}, fmt.Errorf("render Chronon dynamic map: %w", err)
		}
		generatedMapVideo = videoPath
		defer func() { _ = os.RemoveAll(filepath.Dir(generatedMapVideo)) }()
		sha, size, err := dynamicMapVideoIdentity(videoPath)
		if err != nil {
			return RenderReference{}, fmt.Errorf("identify Chronon dynamic map video: %w", err)
		}
		if size == 0 {
			return RenderReference{}, fmt.Errorf("dynamic map video is empty")
		}
		dynamicMapImportSHA = sha
		telemetry, err := os.ReadFile(filepath.Join(filepath.Dir(videoPath), "map.telemetry.json"))
		if err != nil {
			return RenderReference{}, fmt.Errorf("read dynamic map producer telemetry for import: %w", err)
		}
		var mapFacts struct {
			Frames int `json:"frames"`
		}
		if err := json.Unmarshal(telemetry, &mapFacts); err != nil {
			return RenderReference{}, fmt.Errorf("decode dynamic map producer telemetry for import: %w", err)
		}
		itemDurationUS := scriptoverlay.MapItemDurationMS(semanticPlan.Items[0], semanticPlan.DurationMS) * 1000
		dynamicMapImportSpec, err = marshalDynamicMapImport(semanticPlan, sha, size, mapFacts.Frames, itemDurationUS, telemetry)
		if err != nil {
			return RenderReference{}, err
		}
		assetRef := capoverlay.NewOverlayAssetRef(
			kernelasset.Ref{AssetID: "chronon:dynamic-map:" + sha, SHA256: sha, SizeBytes: size, MediaType: "video/mp4"},
			"", videoPath,
		)
		semanticPlan.Background = &capoverlay.OverlayBackground{Kind: "video", AssetRefs: []capoverlay.OverlayAssetRef{assetRef}, Fit: "cover"}
		semanticPlan.Items = nil
	}
	for _, item := range semanticPlan.Items {
		if item.Map != nil && item.Map.DynamicRendererOnly {
			return RenderReference{}, fmt.Errorf("dynamic-renderer-only map %q must be submitted as a one-item producer import", item.ID)
		}
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
	var spec []byte
	if len(dynamicMapImportSpec) > 0 {
		spec = dynamicMapImportSpec
	} else {
		spec, err = marshalRenderingGenOverlayPlan(wirePlan)
		if err != nil {
			return RenderReference{}, fmt.Errorf("marshal semantic chronon plan: %w", err)
		}
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
	if semanticPlan.Source != nil && len(dynamicMapImportSpec) == 0 {
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
	if len(dynamicMapImportSpec) > 0 && (len(assets) != 1 || assets[0].SHA256 != dynamicMapImportSHA) {
		return RenderReference{}, fmt.Errorf("dynamic map import must stage exactly its producer MP4 asset")
	}
	jobID := plan.PlanID
	jobType := capoverlay.JobTypeRender
	if len(dynamicMapImportSpec) > 0 {
		jobType = capoverlay.JobTypeImport
		jobID = fmt.Sprintf("%s:map-import:%s", plan.PlanID, digest.SHA256String(string(dynamicMapImportSpec))[:16])
	}
	if e.freshRender {
		seq := e.freshSeq.Add(1)
		jobID = fmt.Sprintf("%s:render:%d:%d", plan.PlanID, time.Now().UTC().UnixNano(), seq)
		if jobType == capoverlay.JobTypeImport {
			jobID = fmt.Sprintf("%s:map-import:%d:%d", plan.PlanID, time.Now().UTC().UnixNano(), seq)
		}
	}
	// Keep the queue dispatch explicit. RenderingGen uses this discriminator to
	// route the job through the overlay renderer (and to apply the overlay media
	// contract/ffprobe checks); omitting it silently falls back to the legacy
	// render_segment path.
	job := RenderQueueJob{
		ID:      jobID,
		JobType: jobType,
		// ParentJobID is the correlation id of the run that produced this
		// render (pkg/corid). It is the ONLY join key between a master run
		// and the remote queue entry: without it the queue record cannot be
		// traced back to the job that asked for the render. Empty when no
		// correlation id was propagated.
		ParentJobID: corid.FromContext(ctx),
		OverlaySpec: spec,
		Assets:      assets,
	}

	// Content-addressed reuse: a single-item or fused-batch plan whose render
	// identity already names a certified artifact skips submit + wait and flows
	// through this run's publication step. A miss is the historical path.
	var cacheKey OverlayRenderCacheKey
	cacheable := false
	if e.renderCache != nil {
		cacheKey, cacheable = overlayRenderCacheKeyForPlan(semanticPlan, e.renderCacheEpochValue())
	}
	cacheHit := false
	cacheTiming := overlayRenderCacheTiming{outcome: "not_applicable"}
	submitStartedAt := time.Now()
	submitAcceptedAt := time.Time{}
	done := RenderQueueJob{ID: jobID, State: RenderQueueStateCompleted}
	wait := RenderCompletionMetrics{}
	if cacheable {
		cached, ok, timing := e.lookupOverlayRenderCacheTimed(ctx, cacheKey)
		cacheTiming = timing
		applyOverlayRenderCacheTiming(&wait, cacheTiming)
		if ok {
			cacheHit = true
			done.Artifact = cached
			submitAcceptedAt = submitStartedAt
		}
	}
	if !cacheHit {
		submitErr := e.client.Submit(ctx, job)
		if submitErr != nil {
			if e.freshRender || !errors.Is(submitErr, ErrJobExists) {
				message := "chronon queue render submit failed"
				if e.freshRender {
					message = "chronon queue fresh render submit failed"
				}
				recordErr := e.recordQueueAttempt(ctx, jobID, plan, metadata, wait, submitStartedAt, time.Time{}, nil, time.Time{}, "failure")
				return RenderReference{}, errors.Join(fmt.Errorf("%s: %w", message, submitErr), recordErr)
			}
			// The re-arm decision is owned by RearmFailedRenderJob (the same
			// helper the clip.render executor uses), and it fails closed when the
			// job is FAILED and cannot be re-armed.
			if rearmErr := RearmFailedRenderJob(ctx, e.client, jobID); rearmErr != nil {
				recordErr := e.recordQueueAttempt(ctx, jobID, plan, metadata, wait, submitStartedAt, time.Time{}, nil, time.Time{}, "failure")
				return RenderReference{}, errors.Join(fmt.Errorf("chronon queue render: %w", rearmErr), recordErr)
			}
		}
		submitAcceptedAt = time.Now()
		var err error
		done, wait, err = e.waitForCompletion(ctx, jobID)
		applyOverlayRenderCacheTiming(&wait, cacheTiming)
		if err != nil {
			observeCompletionWait(wait, renderOutcomeFailure)
			recordErr := e.recordQueueAttempt(ctx, jobID, plan, metadata, wait, submitStartedAt, submitAcceptedAt, &done, time.Time{}, "failure")
			return RenderReference{}, errors.Join(err, recordErr)
		}
		// Store BEFORE publication: the render is already certified, and a Drive
		// failure must not throw away the GPU work that produced it. Only a
		// cacheable item or fused-batch plans own a key. A dynamic map fold has
		// no semantic item after its video is installed as the background, so it
		// is not addressable here and must not be counted as a store error.
		if cacheable {
			var storeOutcome string
			cacheTiming.store, storeOutcome = e.storeOverlayRenderCacheTimed(ctx, cacheKey, done.Artifact)
			switch cacheTiming.outcome {
			case "miss":
				cacheTiming.outcome = "miss_" + storeOutcome
			case "lookup_error", "probe_error", "invalid":
				cacheTiming.outcome += "_rerender_" + storeOutcome
			}
			applyOverlayRenderCacheTiming(&wait, cacheTiming)
		}
	}
	observeCompletionWait(wait, renderOutcomeSuccess)
	artifactAvailableAt := time.Now()
	segmentRefs, err := certifiedSegmentReferences(done.Artifact, semanticPlan, jobID)
	if err != nil {
		return RenderReference{}, err
	}
	attemptOutcome := renderOutcomeSuccess
	if cacheHit {
		attemptOutcome = renderOutcomeCacheHit
	}
	postRender := func(postCtx context.Context) (postErr error) {
		defer func() {
			if e.recorder == nil {
				return
			}
			recordErr := e.recordQueueAttempt(postCtx, jobID, plan, metadata, wait, submitStartedAt, submitAcceptedAt, &done, artifactAvailableAt, attemptOutcome)
			if recordErr != nil {
				postErr = errors.Join(postErr, fmt.Errorf("record render attempt analytics: %w", recordErr))
			}
		}()
		if plan.RequireDriveBeforeReturn && e.publisher == nil {
			return fmt.Errorf("render job %s requires Drive delivery but no overlay publisher is configured", jobID)
		}
		// Certified ordinary video renders remain local/cacheable artifacts.
		// Only a caller that consumes a remote Drive identity (the final-job
		// delivery surface) explicitly opts into publication.
		if plan.RequireDriveBeforeReturn && e.publisher != nil {
			if len(segmentRefs) == 0 && (done.Artifact == nil || done.Artifact.SHA256 == "" || done.Artifact.SizeBytes <= 0 || done.Artifact.URL == "") {
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
				publication.OverlayBatchID = metadata.BatchID
				publication.OverlayBatchItemIDs = append([]string(nil), metadata.BatchItemIDs...)
				publication.OverlayBatchEndUS = metadata.BatchEndUS
			}
			outputs := segmentRefs
			if len(outputs) == 0 {
				outputs = []OverlayItemRenderReference{{Artifact: done.Artifact}}
			}
			for _, output := range outputs {
				spec := publication
				if output.ItemID != "" {
					spec.OverlayItemID = output.ItemID
					spec.OverlayBatchID, spec.OverlayBatchItemIDs = "", nil
					for _, item := range semanticPlan.Items {
						if item.ID == output.ItemID {
							spec.SourceStartUS, spec.SourceEndUS = overlayItemWindow(item)
							if metadata != nil && metadata.BatchID != "" {
								spec.SourceStartUS += metadata.SourceStartUS
								spec.SourceEndUS += metadata.SourceStartUS
							}
							spec.TargetDurationUS = output.Artifact.DurationUS
							spec.OverlayItemKind, spec.OverlayText = item.Kind, item.Text
							break
						}
					}
				}
				if err := kernobs.MeasureOperation(postCtx, kernobs.OperationInfo{
					Stage: StageOverlayRender, Component: "drive", Operation: "overlay_upload", Provider: "drive",
				}, func(measureCtx context.Context) error {
					return e.publisher.PublishOverlay(measureCtx, spec, output.Artifact)
				}); err != nil {
					return fmt.Errorf("publish overlay artifact to Drive: %w", err)
				}
			}
		}
		return nil
	}
	if cacheHit {
		// A reused locator that fails the publication step is not trustworthy:
		// dropping the row makes the next run re-render instead of looping on
		// unverified bytes. Losing one cache entry is much cheaper than serving
		// a poison entry to every subsequent run.
		verifiedPostRender := postRender
		postRender = func(postCtx context.Context) error {
			if err := verifiedPostRender(postCtx); err != nil {
				e.invalidateOverlayRenderCache(postCtx, cacheKey, "cached artifact failed its publication step: "+err.Error())
				return err
			}
			return nil
		}
	}
	if e.asyncPublication && (e.publisher != nil || e.recorder != nil) {
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
	return RenderReference{JobID: jobID, Status: "COMPLETED", Artifact: done.Artifact, Items: segmentRefs}, nil
}
