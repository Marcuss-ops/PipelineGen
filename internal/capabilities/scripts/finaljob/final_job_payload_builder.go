package finaljob

import (
	"context"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	overlaystatus "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlaystatus"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// finalJobSceneBuilder owns one pass over the canonical timeline. It keeps the
// three visual decisions (fixed media, clip-only, concrete stock) separate so
// the payload orchestration stays readable and each lane can be tested without
// rebuilding the whole handoff. It changes no wire field and no ordering.
type finalJobSceneBuilder struct {
	ctx                    context.Context
	req                    contract.GenerateRequest
	result                 *contract.GenerateResult
	resolver               FinalJobAssetResolver
	stockPoolFolderID      string
	renderLanguage         string
	renderLanguageErr      error
	scenesByID             map[string]contract.Scene
	remoteScenes           []map[string]any
	runtimeAssets          []any
	seenRuntimeAssets      map[string]bool
	stockBoundWindowsMS    map[string]int64
	consumedPhraseOverlays map[string]struct{}
	clipOrdinal            int
	plannedDurationMS      int64
	scriptText             strings.Builder
}

func newFinalJobSceneBuilder(ctx context.Context, req contract.GenerateRequest, result *contract.GenerateResult, resolver FinalJobAssetResolver, stockPoolFolderID string) *finalJobSceneBuilder {
	scenesByID := make(map[string]contract.Scene, len(result.Scenes))
	for _, scene := range result.Scenes {
		scenesByID[scene.ID] = scene
	}
	// The remote video is built from ONE language: the certified renders of the
	// source language, or the single render language the run produced. The
	// decision is made once here instead of per scene, and an ambiguous run is
	// reported by the clip-only path rather than silently resolved.
	renderLanguage, renderLanguageErr := FinalJobRenderLanguage(req, result)
	return &finalJobSceneBuilder{
		ctx: ctx, req: req, result: result, resolver: resolver,
		stockPoolFolderID: stockPoolFolderID,
		renderLanguage:    renderLanguage, renderLanguageErr: renderLanguageErr,
		scenesByID:             scenesByID,
		remoteScenes:           make([]map[string]any, 0, len(result.CanonicalTimeline.Segments)),
		runtimeAssets:          make([]any, 0),
		seenRuntimeAssets:      make(map[string]bool),
		stockBoundWindowsMS:    make(map[string]int64),
		consumedPhraseOverlays: make(map[string]struct{}),
	}
}

func (b *finalJobSceneBuilder) appendSegments() error {
	for _, segment := range b.result.CanonicalTimeline.Segments {
		if err := b.appendSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func (b *finalJobSceneBuilder) appendSegment(segment capabilityaudio.TimelineSegment) error {
	localScene, sceneExists := b.scenesByID[segment.ID]
	fixedMedia := segment.FixedMedia || (sceneExists && localScene.ExecutionMode.IsFixedMedia())
	if !fixedMedia && !sceneExists {
		return fmt.Errorf("final_job scene %q has no generated scene", segment.ID)
	}
	if fixedMedia {
		return b.appendFixedSegment(segment, localScene)
	}
	if !sceneExists {
		return fmt.Errorf("final_job scene %q has no generated scene", segment.ID)
	}
	if b.stockPoolFolderID != "" && localScene.Stock != nil && localScene.Stock.PoolSlot {
		return b.appendStockPoolScene(segment, localScene, false)
	}
	// A single caller-provided stock folder is a stock pool for the whole run.
	// Do not require a per-scene binding: the Master selects whole clips from
	// this folder, and the Producer must not prepare stock.
	if b.stockPoolFolderID != "" && b.req.MediaMode == scriptpkg.MediaModeStockOnly {
		return b.appendStockPoolScene(segment, localScene, true)
	}
	if b.req.MediaMode == scriptpkg.MediaModeStockOnly && !hasFinalJobStockBinding(localScene.Stock) {
		return fmt.Errorf("final_job stock_only scene %q has no stock binding (folder or concrete); refusing to route it through the clip path", segment.ID)
	}
	if sceneVisualClipsAreStockOnly(localScene) && !hasFinalJobStockBinding(localScene.Stock) {
		return fmt.Errorf("final_job scene %q has only stock-marked clips and no stock binding (folder or concrete)", segment.ID)
	}
	if !hasFinalJobStockBinding(localScene.Stock) {
		return b.appendClipScene(segment, localScene)
	}
	return b.appendConcreteStockScene(segment, localScene)
}

func (b *finalJobSceneBuilder) appendFixedSegment(segment capabilityaudio.TimelineSegment, localScene contract.Scene) error {
	type fixedClip struct {
		AssetID    string
		SourceInUS int64
		DurationUS int64
	}
	fixedClips := make([]fixedClip, 0)
	// Keep the original clips in their declared order, but fit them inside the
	// fixed-media segment's canonical timeline duration. The physical Drive
	// asset can be longer than the authored window (for example, a 10-second
	// intro cut from a 27-second source clip).
	for _, intent := range segment.EffectiveAudioIntents() {
		AssetID := strings.TrimSpace(intent.ClipAssetID)
		if AssetID == "" {
			continue
		}
		fixedClips = append(fixedClips, fixedClip{
			AssetID: AssetID, SourceInUS: intent.SourceInUS,
			DurationUS: intent.TimelineDurationUS,
		})
	}
	if len(fixedClips) == 0 {
		for _, clip := range localScene.Clips {
			if clip != nil && strings.TrimSpace(clip.ID) != "" {
				fixedClips = append(fixedClips, fixedClip{AssetID: strings.TrimSpace(clip.ID)})
			}
		}
	}
	if len(fixedClips) == 0 && localScene.Clip != nil && strings.TrimSpace(localScene.Clip.ID) != "" {
		fixedClips = append(fixedClips, fixedClip{AssetID: strings.TrimSpace(localScene.Clip.ID)})
	}
	if len(fixedClips) == 0 {
		return fmt.Errorf("fixed scene %q has no clip assets", segment.ID)
	}
	remainingMS := (segment.DurationUS + 999) / 1000
	if remainingMS <= 0 {
		return fmt.Errorf("fixed scene %q has no positive canonical duration", segment.ID)
	}
	for _, fixed := range fixedClips {
		if remainingMS <= 0 {
			break
		}
		asset, err := resolveFinalJobFixedMediaAsset(b.ctx, b.resolver, b.result, segment.ID, fixed.AssetID, b.renderLanguage)
		if err != nil {
			return fmt.Errorf("resolve intro clip %s: %w", fixed.AssetID, err)
		}
		durationMS, _ := asset["duration_ms"].(int64)
		if durationMS <= 0 {
			return fmt.Errorf("fixed intro/outro clip %q has no certified physical duration", fixed.AssetID)
		}
		sceneMS := min(durationMS, remainingMS)
		if fixed.DurationUS > 0 {
			requestedMS := (fixed.DurationUS + 999) / 1000
			sceneMS = min(sceneMS, requestedMS)
		}
		if fixed.SourceInUS > 0 {
			asset["source_in_ms"] = fixed.SourceInUS / 1000
		}
		// The source clip file may cover a much longer archive window than its
		// authored intro beat. Carry the exact clip window into the render
		// contract; otherwise the first clip consumes the whole intro and later
		// beats are silently omitted (or appear as overlapping source footage).
		asset["source_out_ms"] = fixed.SourceInUS/1000 + sceneMS
		asset["duration_ms"] = sceneMS
		b.remoteScenes = append(b.remoteScenes, CompositeVideoScene(fmt.Sprintf("scene-%04d", b.clipOrdinal), b.clipOrdinal, "clip", asset, sceneMS, "Protected intro clip"))
		AppendFinalJobRuntimeAsset(&b.runtimeAssets, b.seenRuntimeAssets, "clip", asset)
		b.plannedDurationMS += sceneMS
		remainingMS -= sceneMS
		b.clipOrdinal++
	}
	if remainingMS > 0 {
		return fmt.Errorf("fixed scene %q clips are %dms shorter than its canonical duration", segment.ID, remainingMS)
	}
	return nil
}

func (b *finalJobSceneBuilder) appendStockPoolScene(segment capabilityaudio.TimelineSegment, localScene contract.Scene, appendText bool) error {
	sceneText := firstFinalJobValue(localScene.Text[b.req.SourceLanguage], b.req.Title, localScene.ID)
	sceneMS := (segment.DurationUS + 999) / 1000
	scene := CompositeVideoScene(fmt.Sprintf("scene-%04d", b.clipOrdinal), b.clipOrdinal, "stock", nil, sceneMS, sceneText)
	scene["stock_pool_slot"] = true
	b.remoteScenes = append(b.remoteScenes, scene)
	b.plannedDurationMS += sceneMS
	b.clipOrdinal++
	if appendText {
		if text := localScene.Text[b.req.SourceLanguage]; text != "" {
			b.scriptText.WriteString(text)
			b.scriptText.WriteByte('\n')
		}
	}
	return nil
}

func (b *finalJobSceneBuilder) appendClipScene(segment capabilityaudio.TimelineSegment, localScene contract.Scene) error {
	// Clip-only scene: the runtime receives the clip THIS pipeline produced,
	// never the unmodified source clip from the library.
	var asset map[string]any
	var durationMS int64
	asset, durationMS, err := clipSceneRuntimeAsset(b.ctx, b.resolver, b.result, b.renderLanguage, b.renderLanguageErr, segment.ID)
	if err != nil {
		// Text-only narration beats in a clip-only documentary have no source
		// video of their own. Use the next protected clip's already-certified
		// localized render as their visual bed, keeping the narration beat in
		// the canonical timeline without sending an unmodified source clip.
		if visual, visualDurationMS, ok, visualErr := b.nextCertifiedClipVisual(localScene); visualErr != nil {
			return visualErr
		} else if ok {
			asset, durationMS = visual, visualDurationMS
		} else if len(b.result.LocalizedRenders) != 0 {
			return err
		} else {
			// A phrase-only scene has no source video to localize. Its certified
			// full-frame phrase render is already the visual for that scene, so
			// hand that video to the compositor as the clip and don't overlay the
			// same animation a second time.
			var overlayID string
			var phraseOK bool
			var overlayErr error
			asset, durationMS, overlayID, phraseOK, overlayErr = finalJobPhraseOverlayClip(b.ctx, b.result, segment.ID)
			if overlayErr != nil {
				return overlayErr
			}
			if !phraseOK {
				// Keep the original clip error but say why the phrase-only fallback
				// did not apply: it needs exactly one text_phrase item for this
				// scene with a certified copy-eligible video artifact. Returning the
				// bare clip error hid that reason.
				return fmt.Errorf("%w; phrase-only fallback did not apply for scene %q (need exactly one text_phrase item with a certified copy-eligible video artifact)", err, segment.ID)
			}
			b.consumedPhraseOverlays[overlayID] = struct{}{}
		}
	}
	// A certified scene render often contains only the selected source clip's
	// original duration, while its narration can be much longer. Repeat that
	// same pipeline-produced visual in bounded chunks to fill the canonical
	// scene duration; otherwise the final video ends before the certified audio
	// and later overlays are lost.
	remainingMS := (segment.DurationUS + 999) / 1000
	if remainingMS <= 0 {
		return fmt.Errorf("final_job clip-only scene %q has no positive canonical duration", segment.ID)
	}
	for remainingMS > 0 {
		chunkMS := min(durationMS, remainingMS)
		b.remoteScenes = append(b.remoteScenes, CompositeVideoScene(fmt.Sprintf("scene-%04d", b.clipOrdinal), b.clipOrdinal, "clip", asset, chunkMS, firstFinalJobValue(localScene.Text[b.req.SourceLanguage], b.req.Title, localScene.ID)))
		b.plannedDurationMS += chunkMS
		b.clipOrdinal++
		remainingMS -= chunkMS
	}
	AppendFinalJobRuntimeAsset(&b.runtimeAssets, b.seenRuntimeAssets, "clip", asset)
	if text := localScene.Text[b.req.SourceLanguage]; text != "" {
		b.scriptText.WriteString(text)
		b.scriptText.WriteByte('\n')
	}
	return nil
}

// nextCertifiedClipVisual gives a text-only clip-mode scene a visual from the
// next fixed-media scene. This is used for narration bridges that precede the
// clip they introduce; the localized render is already certified and carries
// the target-language treatment, so no raw media fallback is possible.
func (b *finalJobSceneBuilder) nextCertifiedClipVisual(scene contract.Scene) (map[string]any, int64, bool, error) {
	if b == nil || b.result == nil || scene.Clip != nil || len(scene.Clips) != 0 {
		return nil, 0, false, nil
	}
	for _, candidate := range b.result.Scenes {
		if candidate.Index <= scene.Index || !candidate.ExecutionMode.IsFixedMedia() {
			continue
		}
		for _, unit := range RenderUnitsForScene(candidate) {
			if unit.Clip == nil {
				continue
			}
			rendered, ok := CertifiedLocalizedRenderForClip(b.result, candidate.ID, unit.Clip.ID, b.renderLanguage)
			if !ok {
				continue
			}
			asset, durationMS, err := RenderedClipAssetRef(b.ctx, b.resolver, rendered, candidate.ID)
			if err != nil {
				return nil, 0, false, err
			}
			return asset, durationMS, true, nil
		}
	}
	return nil, 0, false, nil
}

func (b *finalJobSceneBuilder) appendConcreteStockScene(segment capabilityaudio.TimelineSegment, localScene contract.Scene) error {
	sceneText := firstFinalJobValue(localScene.Text[b.req.SourceLanguage], b.req.Title, localScene.ID)
	sceneMS := (segment.DurationUS + 999) / 1000
	// The selected stock file is handed to the remote compositor unchanged.
	// Clip card styling and subtitle renders never replace a stock binding.
	stockAsset, stockWindowMS, err := finalJobStockBindingAsset(b.ctx, b.resolver, localScene.Stock)
	if err != nil {
		return fmt.Errorf("final_job scene %q: %w", segment.ID, err)
	}
	if localScene.Stock != nil && localScene.Stock.DurationMs <= 0 && localScene.Stock.EndMs <= localScene.Stock.StartMs && sceneMS > 0 {
		stockWindowMS = sceneMS
	}
	// The binding window is Producer-local metadata: it is keyed by the
	// generated scene id and never added to the asset the Master receives.
	// The remote renderer loops that source window over the canonical
	// voiceover duration.
	SceneID := fmt.Sprintf("scene-%04d", b.clipOrdinal)
	b.stockBoundWindowsMS[SceneID] = stockWindowMS
	// A concrete stock window is Master-owned: the Drive bytes are
	// downloaded, cut and looped by the Master. The Producer never certifies,
	// cuts or re-encodes stock windows locally.
	AppendFinalJobRuntimeAsset(&b.runtimeAssets, b.seenRuntimeAssets, "stock", stockAsset)
	b.remoteScenes = append(b.remoteScenes, CompositeVideoScene(SceneID, b.clipOrdinal, "stock", stockAsset, sceneMS, sceneText))
	b.plannedDurationMS += sceneMS
	b.clipOrdinal++
	if text := localScene.Text[b.req.SourceLanguage]; text != "" {
		b.scriptText.WriteString(text)
		b.scriptText.WriteByte('\n')
	}
	return nil
}

// finalJobPreflight validates the canonical inputs and records the overlay
// gate before any expensive work starts. It returns the three facts the later
// phases need instead of recomputing them.
func finalJobPreflight(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult) (string, bool, bool, error) {
	if result == nil || result.CanonicalTimeline == nil || result.CanonicalTimeline.DurationUS <= 0 {
		return "", false, false, fmt.Errorf("final_job requires a canonical timeline with positive duration")
	}
	// Audio is optional for render-only runs (for example phrase animation
	// previews). When present, retain the certified mux checks below. The
	// canonical video timeline remains the duration authority when audio is off.
	hasFinalAudio := result.FinalAudio != nil && result.FinalAudio.DurationMS > 0
	// The finalizer muxes this single canonical mix at video time zero. A
	// non-zero encoded PTS would shift the voiceover against every scene even
	// when the reported duration matches, so reject it before handing off to 51.
	if hasFinalAudio && result.FinalAudio.StartPTS != 0 {
		return "", false, false, fmt.Errorf("final_job final audio start_pts is %d, want 0 for voiceover alignment", result.FinalAudio.StartPTS)
	}
	if len(result.Scenes) == 0 {
		return "", false, false, fmt.Errorf("final_job has no generated scenes")
	}
	stockPoolFolderID, err := finalJobStockPoolFolder(req)
	if err != nil {
		return "", false, false, err
	}
	imageRequested := FinalJobImageRequested(req)
	var preflightWarnings []string
	if budget := result.PhraseOverlayBudget; budget != nil && budget.Shortfall > 0 {
		preflightWarnings = append(preflightWarnings, fmt.Sprintf(
			"phrase overlay shortfall: requested %d, materialized %d, missing %d",
			budget.Requested, budget.Materialized, budget.Shortfall))
	}
	overlaystatus.Update(runID, func(status *overlaystatus.Status) {
		status.Stage = "preflight"
		status.Requested = OverlayStatusRequest(req)
		status.Audit = result.OverlayAudit
		status.Plan = OverlayStatusPlanSummary(result.OverlayPlan)
	})
	if err := FinalJobEntityImageGate(result.OverlayAudit, imageRequested); err != nil {
		overlaystatus.Update(runID, func(status *overlaystatus.Status) {
			status.Stage = "preflight_failed"
			status.Error = err.Error()
			status.Preflight = &overlaystatus.Preflight{Passed: false, Error: err.Error()}
		})
		return "", false, false, err
	}
	if imageRequested {
		if err := FinalJobImagePreflightOnce(ctx, true, result); err != nil {
			overlaystatus.Update(runID, func(status *overlaystatus.Status) {
				status.Stage = "preflight_failed"
				status.Error = err.Error()
				status.Preflight = &overlaystatus.Preflight{Passed: false, Error: err.Error()}
			})
			return "", false, false, err
		}
	}
	overlaystatus.Update(runID, func(status *overlaystatus.Status) {
		status.Stage = "preflight_passed"
		status.Error = ""
		status.Preflight = &overlaystatus.Preflight{Passed: true, Warnings: preflightWarnings}
	})
	return stockPoolFolderID, hasFinalAudio, imageRequested, nil
}

// finalJobFinalizePayload turns the scene plan into the two Master payloads.
// It is the only place that serializes the compiled plan and the runtime
// assets; the scene builder never sees the wire DTO.
func finalJobFinalizePayload(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult, builder *finalJobSceneBuilder, preparers []FinalJobVideoSegmentPreparer, hasFinalAudio, imageRequested bool) (map[string]any, map[string]any, error) {
	remoteScenes := builder.remoteScenes
	runtimeAssets := builder.runtimeAssets
	seenRuntimeAssets := builder.seenRuntimeAssets
	var audioRef map[string]any
	stockBoundWindowsMS := builder.stockBoundWindowsMS
	clipOrdinal := builder.clipOrdinal
	plannedDurationMS := builder.plannedDurationMS
	scriptText := builder.scriptText
	consumedPhraseOverlays := builder.consumedPhraseOverlays
	if clipOrdinal == 0 {
		return nil, nil, fmt.Errorf("final_job produced no remote scenes")
	}
	if err := EnforceFinalJobMinimumSceneDuration(remoteScenes, 100); err != nil {
		return nil, nil, err
	}
	plannedDurationMS = FinalJobSceneDurationMS(remoteScenes)
	if hasFinalAudio {
		if delta := plannedDurationMS - result.FinalAudio.DurationMS; delta < -capabilityaudio.FinalAudioDurationToleranceMS || delta > capabilityaudio.FinalAudioDurationToleranceMS {
			return nil, nil, fmt.Errorf("final_job scene duration %dms does not match certified final audio %dms (tolerance %dms)", plannedDurationMS, result.FinalAudio.DurationMS, capabilityaudio.FinalAudioDurationToleranceMS)
		}
		if err := EnforceFinalJobPreflight(remoteScenes, result.FinalAudio.DurationMS); err != nil {
			return nil, nil, err
		}
		var err error
		audioRef, err = FinalJobAudioAsset(ctx, *result.FinalAudio)
		if err != nil {
			return nil, nil, err
		}
		AppendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "final_audio", audioRef)
	} else if delta := plannedDurationMS*1000 - result.CanonicalTimeline.DurationUS; delta < -capabilityaudio.FinalAudioDurationToleranceUS || delta > capabilityaudio.FinalAudioDurationToleranceUS {
		return nil, nil, fmt.Errorf("final_job scene duration %dms does not match canonical timeline %dus", plannedDurationMS, result.CanonicalTimeline.DurationUS)
	}
	remoteOverlays, overlayManifest, err := FinalJobOverlayHandoff(ctx, result, consumedPhraseOverlays, imageRequested)
	if err != nil {
		return nil, nil, err
	}
	if overlayManifest != nil {
		if asset, ok := overlayManifest["asset"].(map[string]any); ok {
			AppendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "overlay_timeline", asset)
		}
		if manifests, ok := overlayManifest["manifests"].([]any); ok {
			for _, raw := range manifests {
				if manifest, ok := raw.(map[string]any); ok {
					if asset, ok := manifest["asset"].(map[string]any); ok {
						AppendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "overlay_timeline", asset)
					}
				}
			}
		}
	}
	for _, raw := range remoteOverlays {
		if asset, ok := raw.(map[string]any); ok {
			AppendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "overlay", asset)
		}
	}
	// Master 51's deployed build may predate source_uri propagation in the
	// future-asset plan. Keep the canonical field and mirror it into url for
	// that worker path so assets do not fall back to master_asset_bridge.
	exposeFinalJobSourceURLs(runtimeAssets)
	if audioRef == nil {
		return nil, nil, fmt.Errorf("final_job V2 worker submission requires certified final audio")
	}
	compiledPlan, _, err := FinalJobCompiledPlanPayloadWithStockPool(remoteScenes, remoteOverlays, overlayManifest, *result.FinalAudio, audioRef, result.CanonicalTimeline.DurationUS, builder.stockPoolFolderID, stockBoundWindowsMS)
	if err != nil {
		return nil, nil, err
	}
	// The runtime refs are content-addressed (SHA), while the plan may still
	// carry the fragment work ids used while building it. Bind both to the
	// same canonical identities before the Master sees the plan; otherwise the
	// copy-only assembler rejects segment-0000 with "manifest binding is not
	// exact" even though the prefetched bytes are correct.
	compiledPlan, err = AlignFinalJobCompiledPlanAssetIDs(compiledPlan, runtimeAssets)
	if err != nil {
		return nil, nil, err
	}
	retainedRuntimeAssets, err := RetainFinalJobRuntimeAssets(compiledPlan, runtimeAssets)
	if err != nil {
		return nil, nil, err
	}
	runtimeAssets = retainedRuntimeAssets
	if len(preparers) > 0 && preparers[0] != nil {
		compiledPlan, runtimeAssets, err = PrepareFinalJobVideoSegments(ctx, compiledPlan, runtimeAssets, preparers[0])
		if err != nil {
			return nil, nil, fmt.Errorf("prepare non-stock final_job segments: %w", err)
		}
	}
	compiledPlanSHA := digest.SHA256String(compiledPlan)
	attempt := 1
	if result.RemoteFinalJob != nil && result.RemoteFinalJob.Attempt > 0 {
		attempt = result.RemoteFinalJob.Attempt
	}
	key := FinalJobAttemptIdempotencyKey(req.IdempotencyKey, runID, attempt, compiledPlanSHA)
	pre := map[string]any{
		"idempotency_key":             key,
		"job_type":                    "video.render.v2",
		"render_only":                 true,
		"video_name":                  FinalJobVideoName(req, runID),
		"script_text":                 strings.TrimSpace(scriptText.String()),
		"compiled_render_plan_json":   compiledPlan,
		"compiled_render_plan_sha256": compiledPlanSHA,
	}
	finalize := map[string]any{
		"idempotency_key": key + "-finalize",
		"runtime_assets":  runtimeAssets,
	}
	// The Master has a strict FINALIZE DTO. Overlay and legacy runtime hints
	// stay inside its opaque runtime_payload; the worker's executable timing
	// source is compiled_render_plan_json above.
	runtimePayload := map[string]any{
		"video_name":  FinalJobVideoName(req, runID),
		"script_text": strings.TrimSpace(scriptText.String()),
		"overlays":    remoteOverlays,
	}
	if overlayManifest != nil {
		runtimePayload["overlay_manifest"] = overlayManifest
	}
	if hasFinalAudio {
		runtimePayload["runtime_audio"] = map[string]any{
			"voiceover_asset_id":         strings.TrimSpace(result.FinalAudio.AssetID),
			"voiceover_volume":           1.0,
			"voiceover_duration_seconds": float64(result.FinalAudio.DurationMS) / 1000,
		}
	}
	finalize["runtime_payload"] = runtimePayload
	// The submitted envelope IS the durable diagnostic surface. Writing a
	// second copy under /tmp made a temporary path look authoritative and
	// lost the evidence whenever the host cleaned it up.
	return pre, finalize, nil
}
