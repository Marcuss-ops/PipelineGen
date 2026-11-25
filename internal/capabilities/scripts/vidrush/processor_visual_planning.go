package vidrush

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// VisualPlanningProcessor resolves every open scene through one batched
// MediaMemory request. Locked assignments are applied locally and are never
// sent to the resolver. The resolver owns candidate ranking and layer choice.
type VisualPlanningProcessor struct {
	resolver     mediamemory.Resolver
	materializer scriptports.AssetMaterializer
	log          *zap.Logger
}

func NewVisualPlanningProcessor(resolver mediamemory.Resolver, materializer scriptports.AssetMaterializer, log *zap.Logger) *VisualPlanningProcessor {
	if log == nil {
		log = zap.NewNop()
	}
	return &VisualPlanningProcessor{resolver: resolver, materializer: materializer, log: log}
}

func (p *VisualPlanningProcessor) Name() ProcessorName { return ProcessorVisualPlanning }
func (p *VisualPlanningProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *VisualPlanningProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil || plan.MediaPlan.Mode == mediadomain.MediaPlanModeDisabled || !mediadomain.IsActiveMediaPlanMode(plan.MediaPlan.Mode) || len(input.SpecScene.Scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	locked := make(map[string]mediamemory.SceneVisualPlan)
	for _, a := range plan.MediaPlan.Assignments {
		if !a.Locked {
			continue
		}
		key := a.SegmentID + "/" + string(a.Slot)
		locked[key] = visualPlanFromAssignment(plan, a)
	}

	open := make([]mediamemory.SceneSpec, 0, len(input.SpecScene.Scenes))
	for i, scene := range input.SpecScene.Scenes {
		if !sceneAllowsMediaResolution(scene) || sceneHasDirectStockBinding(input.SpecScene, input.StockBindings, scene.ID, scene.SegmentID, i) {
			// A direct stock binding is the scene's authoritative visual
			// source: the memory catalog must never fill its slots.
			continue
		}
		segmentID := visualSegmentID(plan, scene, i)
		slots := visualSlotsForPolicy(plan.MediaPlan, segmentID)
		openSlots := make([]mediadomain.SlotKind, 0, len(slots))
		for _, slot := range slots {
			if _, ok := locked[segmentID+"/"+string(slot)]; !ok {
				openSlots = append(openSlots, slot)
			}
		}
		if len(openSlots) > 0 {
			open = append(open, mediamemory.SceneSpec{ID: scene.ID, Text: scene.Text, Language: plan.Language, Slots: openSlots})
		}
	}

	plans := make([]mediamemory.SceneVisualPlan, 0, len(locked)+len(open))
	for _, scene := range input.SpecScene.Scenes {
		idx := sceneIndex(input.SpecScene.Scenes, scene.ID)
		if !sceneAllowsMediaResolution(scene) || sceneHasDirectStockBinding(input.SpecScene, input.StockBindings, scene.ID, scene.SegmentID, idx) {
			continue
		}
		segmentID := visualSegmentID(plan, scene, idx)
		if v, ok := locked[segmentID+"/primary_video"]; ok {
			v.SceneID, v.SegmentID, v.Text = scene.ID, segmentID, scene.Text
			plans = append(plans, v)
		}
		for _, slot := range visualSlotsForPolicy(plan.MediaPlan, segmentID) {
			if slot == mediadomain.SlotPrimaryVideo {
				continue
			}
			if v, ok := locked[segmentID+"/"+string(slot)]; ok {
				v.SceneID, v.SegmentID, v.Text = scene.ID, segmentID, scene.Text
				plans = append(plans, v)
			}
		}
	}
	if len(open) > 0 {
		if p.resolver == nil {
			return &PostProcessResult{Warnings: []string{"visual planning resolver unavailable"}}, nil
		}
		resolved, err := p.resolver.Resolve(ctx, mediamemory.ResolveRequest{ProjectID: plan.ID, Language: plan.Language, Scenes: open, Policy: mediamemory.ResolvePolicy{PreferApprovedBindings: true, AllowExternalSearch: plan.MediaPlan.Mode != mediadomain.MediaPlanModeCacheOnly, MaxCandidatesPerSlot: plannerLimit(plan)}})
		if err != nil {
			return &PostProcessResult{Warnings: []string{fmt.Sprintf("visual planning: %v", err)}}, nil
		}
		for _, v := range resolved.Plans {
			plans = append(plans, v)
		}
	}

	p.materializeLayers(ctx, plans)

	projected := cloneScenes(input.SpecScene.Scenes)
	projectVisualBindings(projected, plans, plan.MediaPlan.Assignments)
	return &PostProcessResult{VisualPlans: plans, SynthesizedScenes: projected, Changed: len(plans) > 0}, nil
}

// materializeLayers ensures each winning external asset is materialized
// once. The layer is mutated in place with the materialized asset ID
// and provider.
func (p *VisualPlanningProcessor) materializeLayers(ctx context.Context, plans []mediamemory.SceneVisualPlan) {
	if p.materializer == nil {
		return
	}
	for pi := range plans {
		for li := range plans[pi].Layers {
			layer := &plans[pi].Layers[li]
			mat, err := p.materializer.Materialize(ctx, *layer)
			if err != nil {
				p.log.Warn("visual planning: materialization failed",
					zap.String("scene_id", plans[pi].SceneID),
					zap.String("slot", string(layer.Slot)),
					zap.Error(err),
				)
				continue
			}
			layer.AssetID = mat.AssetID
			layer.Provider = mat.Provider
			if mat.DurationMs > 0 && layer.EndMs == 0 {
				layer.EndMs = layer.StartMs + mat.DurationMs
			}
		}
	}
}

func plannerLimit(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		return plan.MediaPlan.Planner.CandidateLimit
	}
	return 10
}

func visualSegmentID(plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene, index int) string {
	if scene.SegmentID != "" {
		return scene.SegmentID
	}
	if index >= 0 && index < len(plan.Segments) && plan.Segments[index].ID != "" {
		return plan.Segments[index].ID
	}
	return scene.ID
}

func visualSlots(plan mediadomain.MediaPlanSpec, segmentID string) []mediadomain.SlotKind {
	seen := map[mediadomain.SlotKind]bool{}
	add := func(raw string) {
		slot := mediadomain.SlotKind(raw)
		if slot.IsValid() {
			seen[slot] = true
		}
	}
	for _, a := range plan.Assignments {
		if a.SegmentID == segmentID {
			add(string(a.Slot))
		}
	}
	for _, slot := range ManualSearchSlots(plan, segmentID) {
		seen[slot] = true
	}
	if len(seen) == 0 {
		add(string(mediadomain.SlotPrimaryVideo))
	}
	ordered := []mediadomain.SlotKind{mediadomain.SlotPrimaryVideo, mediadomain.SlotSecondaryImage, mediadomain.SlotEvidenceOverlay, mediadomain.SlotPortrait, mediadomain.SlotDocument, mediadomain.SlotBackground, mediadomain.SlotMap}
	out := make([]mediadomain.SlotKind, 0, len(seen))
	for _, slot := range ordered {
		if seen[slot] {
			out = append(out, slot)
		}
	}
	return out
}

// visualSlotsForPolicy applies the provider policy to implicit visual slots.
// A hybrid plan with no explicit searches historically synthesized a
// primary_video slot, which caused the generic MediaMemory resolver to fan out
// even when the caller explicitly disabled the video provider. Explicit locked
// assignments remain authoritative and are handled separately by Process.
func visualSlotsForPolicy(plan mediadomain.MediaPlanSpec, segmentID string) []mediadomain.SlotKind {
	slots := visualSlots(plan, segmentID)
	if plan.ProviderPolicy.YouTube != mediadomain.MediaToggleDisabled {
		return slots
	}
	filtered := make([]mediadomain.SlotKind, 0, len(slots))
	for _, slot := range slots {
		if slot == mediadomain.SlotPrimaryVideo {
			continue
		}
		filtered = append(filtered, slot)
	}
	return filtered
}
func sceneIndex(s []scriptpkg.SpecScene, id string) int {
	for i := range s {
		if s[i].ID == id {
			return i
		}
	}
	return -1
}

func visualPlanFromAssignment(plan *scriptpkg.ResolvedGenerationPlan, a mediadomain.SegmentMediaAssignment) mediamemory.SceneVisualPlan {
	assetID := a.Asset.AssetID
	if assetID == "" {
		assetID = a.Asset.ClipID
	}
	provider := a.Asset.Provider
	return mediamemory.SceneVisualPlan{ProjectID: plan.ID, SegmentID: a.SegmentID, Source: provider, Layers: []mediamemory.Layer{{Slot: a.Slot, AssetID: assetID, Provider: provider, StartMs: a.Asset.StartMs, EndMs: a.Asset.EndMs}}}
}

func cloneScenes(in []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := append([]scriptpkg.SpecScene(nil), in...)
	for i := range out {
		out[i].Bindings = in[i].Bindings
	}
	return out
}

func projectVisualBindings(scenes []scriptpkg.SpecScene, plans []mediamemory.SceneVisualPlan, assignments []mediadomain.SegmentMediaAssignment) {
	for _, vp := range plans {
		for i := range scenes {
			if scenes[i].ID != vp.SceneID {
				continue
			}
			visualPlan := &scriptpkg.VisualPlan{
				Layers:     make([]scriptpkg.VisualLayer, 0, len(vp.Layers)),
				Candidates: make([]scriptpkg.VisualCandidate, 0, len(vp.Candidates)),
			}
			for _, layer := range vp.Layers {
				if layer.AssetID == "" {
					continue
				}
				durationMs := layer.EndMs - layer.StartMs
				scenes[i].Bindings.Media = append(scenes[i].Bindings.Media, scriptpkg.ResolvedMediaBinding{Slot: string(layer.Slot), AssetID: layer.AssetID, BindingID: layer.BindingID, Provider: layer.Provider, Score: layer.CandidateScore})
				if layer.Slot == mediadomain.SlotPrimaryVideo {
					if scenes[i].Bindings.Stock == nil {
						scenes[i].Bindings.Stock = &scriptpkg.StockBinding{}
					}
					scenes[i].Bindings.Stock.AssetID = layer.AssetID
					scenes[i].Bindings.Stock.Source = layer.Provider
					scenes[i].Bindings.Stock.Score = layer.CandidateScore
					// A locked primary-video assignment is also a canonical
					// renderable clip binding. Keep the visual/media binding
					// for planning provenance, but expose the clip identity in
					// SpecScene so downstream render, Drive reconciliation and
					// /jobs/:id/full consumers see the same scene skeleton.
					scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
						ClipID:     layer.AssetID,
						StartMs:    layer.StartMs,
						EndMs:      layer.EndMs,
						DurationMs: durationMs,
					}
				}
				visualPlan.Layers = append(visualPlan.Layers, scriptpkg.VisualLayer{
					Slot:       layer.Slot,
					AssetID:    layer.AssetID,
					Provider:   layer.Provider,
					StartMs:    layer.StartMs,
					EndMs:      layer.EndMs,
					DurationMs: durationMs,
					Score:      layer.CandidateScore,
				})
			}
			for _, c := range vp.Candidates {
				visualPlan.Candidates = append(visualPlan.Candidates, scriptpkg.VisualCandidate{
					AssetID:      c.AssetID,
					Provider:     c.Provider,
					Score:        c.Score,
					DurationMs:   c.DurationMs,
					MediaType:    c.MediaType,
					RightsStatus: c.RightsStatus,
				})
			}
			scenes[i].VisualPlan = visualPlan
		}
	}
	for _, a := range assignments {
		if !a.Locked || a.Asset.ClipID == "" {
			continue
		}
		for i := range scenes {
			if scenes[i].SegmentID != a.SegmentID {
				continue
			}
			if scenes[i].Bindings.Clip == nil {
				scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{}
			}
			scenes[i].Bindings.Clip.ClipID = a.Asset.ClipID
			scenes[i].Bindings.Clip.StartMs = a.Asset.StartMs
			scenes[i].Bindings.Clip.EndMs = a.Asset.EndMs
			scenes[i].Bindings.Clip.DurationMs = a.Asset.EndMs - a.Asset.StartMs
		}
	}
}

// materializeOne materializes one enriched segment: it acquires, verifies and
// finalizes every candidate through the shared provider registry and common
// finalizer, applies the generation fallback, and selects the primary video.
// It is shared by the batch Process path and the single-segment Materialize
// port so the materialization stage is implemented exactly once.
func (p *VidRushMaterializationProcessor) materializeOne(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (vidRushMaterializedSegment, error) {
	updated := CloneVidRushSegmentResult(segment)
	if segment.ExecutionMode.IsFixedMedia() {
		// Fixed media is already authoritative and must not be acquired,
		// verified, persisted, ranked or replaced by this processor.
		updated.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
		return vidRushMaterializedSegment{result: updated}, nil
	}
	run := &vidRushMaterializationRun{
		processor:          p,
		plan:               plan,
		segment:            segment,
		perSceneImageScope: plan != nil && plan.MediaPlan.Extraction.EntityImages.PerScene(),
	}

	// Search candidates must be acquired and verified before deciding how
	// many generated images are actually missing. This keeps generation a
	// true fallback instead of a parallel source that duplicates valid web
	// assets.
	imageTarget := vidRushImageTargetForSegment(plan, segment)
	materializationImageTarget := imageTarget
	if plan != nil && entityImageSurfaceEnabled(plan) && imageTarget > 0 {
		// Materialize the current demand plus one alternate for later scenes.
		// Discovery keeps its larger candidate pool; downloading a five-image
		// library synchronously on every run delayed the current video.
		materializationImageTarget = imageTarget * entityImageCatalogMaterializationCopies
	} else if run.perSceneImageScope && imageTarget > 0 {
		// Keep one extra verified per-scene candidate as a uniqueness fallback.
		// Independent searches can return the same syndicated photo in two
		// scenes; the overlay planner chooses the next materialized image by
		// content hash so the selected images are genuinely distinct.
		materializationImageTarget++
	}
	discoveredCandidates := prioritizeExactVidRushImageCandidates(updated.Assets.Candidates, imageTarget, plan)
	// Same surface decision as the target multiplication above: a payload that
	// asks for persons/entities through `include` (the r20minuti shape) has no
	// legacy entity_images toggle, so reading the toggle here skipped the
	// durable pool while the target was still multiplied for it. The pool must
	// follow the surface, not the deprecated spelling.
	if p.catalog != nil && plan != nil && entityImageSurfaceEnabled(plan) {
		// Attach the durable per-person pool before acquisition so
		// verified-but-never-downloaded catalog rows get a real
		// Acquire → Verify → Finalize attempt in-run instead of arriving
		// after materialization, when they can no longer be fetched.
		if pooled := durableEntityImagePoolForSegment(ctx, p.catalog, updated, p.contentStore); len(pooled) > 0 {
			discoveredCandidates = AppendProviderCandidatesUnique(discoveredCandidates, pooled)
		}
	}
	var materializeErr error
	updated.Assets.Candidates, materializeErr = run.materialize(ctx, discoveredCandidates, materializationImageTarget)
	if materializeErr != nil {
		return vidRushMaterializedSegment{}, materializeErr
	}
	generationCandidates, generationState := p.planGenerationFallback(plan, updated)
	updated.Cache.ImageGeneration = generationState
	if len(generationCandidates) > 0 {
		// Only the newly planned fallback candidates need a second
		// acquisition pass. Replaying the already attempted web candidates
		// here would duplicate downloads after a generation fallback.
		generated, materializeErr := run.materialize(ctx, generationCandidates, len(generationCandidates))
		if materializeErr != nil {
			return vidRushMaterializedSegment{}, materializeErr
		}
		updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, generated)
	}
	materialized := updated.Assets.Candidates
	updated.Assets.SecondaryImages = selectExactVidRushImages(materialized, imageTarget, plan, updated)
	logVidRushSelectedImages(p.log, segment.SegmentID, updated.Assets.SecondaryImages)
	updated.Assets.GeneratedImages = filterVidRushGeneratedImages(updated.Assets.SecondaryImages)
	if imageTarget > 0 && len(updated.Assets.SecondaryImages) != imageTarget {
		run.warnings = append(run.warnings, fmt.Sprintf(
			"FAILED_REQUIRED_IMAGE_COUNT: required=%d verified=%d segment=%s",
			imageTarget, len(updated.Assets.SecondaryImages), segment.SegmentID,
		))
	}
	profile := updated.CanonicalSemanticProfile()
	if p.sampler != nil {
		updated.Assets.PrimaryVideo = p.selectPrimaryWithMediaSampler(ctx, materialized, profile)
	} else {
		// Selection is fail-closed when the canonical sampler is absent. The
		// legacy VidRush ranker is not a production fallback; wiring must
		// provide MediaSampler before a primary can be bound.
		updated.Assets.PrimaryVideo = nil
	}
	updated.Assets.CandidateSetHash = candidateSetHash(materialized)
	// Numeric new-upload counter: 0 when the catalog Drive materialization is
	// reused, 1 per freshly finalized internet_images candidate otherwise.
	updated.Cache.InternetImagesNewUploads = run.newInternetImageUploads
	return vidRushMaterializedSegment{result: updated, warnings: run.warnings}, nil
}
