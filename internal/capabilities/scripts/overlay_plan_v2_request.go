// overlay_plan_v2_request.go — producer and single-call cutover for the
// versioned 13d contract.
//
// The request is built from the certified scene projection. compileOverlayPlanV2
// sends that complete envelope in one call to the configured v2 compiler; the
// returned plan is adopted only after the Go seal boundary validates it.
package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

// buildOverlayPlanV2Request maps the planner's scenes and the two extractive
// manifests onto the versioned full-plan envelope. It fails closed on identity
// or timing that the planner would otherwise have to guess.
func buildOverlayPlanV2Request(
	result *GenerateResult,
	language Language,
	canvas OverlayCanvasSpec,
	planID, videoID, projectID, motionSeed string,
	timedScenes []Scene,
	sceneInputs []capabilityoverlay.SceneInput,
	rawScenes []overlayplan.RawScene,
	imagePlan *overlayplan.ImagePlan,
	timelineBounds map[string][2]int64,
) (overlayplan.V2Request, error) {
	if result == nil {
		return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: result is required")
	}
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: plan_id and video_id are required")
	}
	canvas = canvas.withDefaults()
	if err := validateOverlayEditorialDensity(canvas); err != nil {
		return overlayplan.V2Request{}, err
	}
	// The editorial manifest is extracted from the source scenes before the
	// translation phase. Its titles and bullets are therefore source-language
	// surfaces: projecting them into a localized plan would either render the
	// wrong language or make the Rust compiler reject the whole plan. Disable
	// just those two categories; phrases, maps, images and every other lane keep
	// the caller's toggles.
	localizedSourceEditorial := isLocalizedSourceEditorial(result.Editorial, language)
	if localizedSourceEditorial {
		canvas = withoutSourceEditorialCategories(canvas)
	}
	canvasWire, err := overlayPlanCanvas(canvas)
	if err != nil {
		return overlayplan.V2Request{}, err
	}
	request := overlayplan.V2Request{
		Operation:      "compile_editorial_plan",
		SchemaVersion:  overlayplan.SchemaVersionV2,
		Plan:           overlayPlanIdentity(planID, videoID, projectID, motionSeed, canvas),
		Language:       string(language),
		SourceLanguage: string(result.SourceLanguage),
		Seal:           overlayPlanSeal(result, canvas, timelineBounds),
		Canvas:         canvasWire,
		Scenes:         make([]overlayplan.Scene, 0, len(timedScenes)),
		EntityTimeline: overlayPlanEntityTimeline(result, language, projectID, timelineBounds),
		Media:          overlayPlanMedia(result, language, sceneInputs),
		ImagePlan:      imagePlan,
		Budgets:        overlayPlanBudgets(canvas),
		SceneWindows:   make(map[string][2]int64, len(timedScenes)),
		Editorial:      editorialManifestToWire(result.Editorial),
		Chapters:       chapterManifestToWire(result.ChapterManifest),
		HeavySentences: heavySentencesToWire(result.HeavySentences),
		NumericLexicon: numericLexiconToWire(),
	}
	seen := make(map[string]struct{}, len(timedScenes))
	inputsByScene := make(map[string]capabilityoverlay.SceneInput, len(sceneInputs))
	for _, input := range sceneInputs {
		inputsByScene[strings.TrimSpace(input.ID)] = input
	}
	rawByScene := make(map[string]overlayplan.RawScene, len(rawScenes))
	for _, raw := range rawScenes {
		rawByScene[strings.TrimSpace(raw.SceneID)] = raw
	}
	for _, scene := range timedScenes {
		sceneID := strings.TrimSpace(scene.ID)
		if sceneID == "" {
			return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: scene id is required")
		}
		if _, duplicate := seen[sceneID]; duplicate {
			return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: duplicate scene id %q", sceneID)
		}
		seen[sceneID] = struct{}{}
		bounds, ok := timelineBounds[sceneID]
		if !ok || bounds[1] < bounds[0] {
			return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: scene %q has no canonical timeline bounds", sceneID)
		}
		wire := overlayplan.Scene{
			ID:                 sceneID,
			Text:               scene.Text[language],
			DurationMS:         scene.DurationMS,
			ExecutionMode:      string(scene.ExecutionMode),
			ShortPhraseOverlay: scene.ShortPhraseOverlay,
			OverlayCategories:  overlayCategoriesToWire(scene.OverlayCategories),
			Index:              scene.Index,
			TimelineStartUS:    bounds[0],
			DurationUS:         bounds[1] - bounds[0],
		}
		if localizedSourceEditorial {
			wire.OverlayCategories = withoutSourceEditorialWireCategories(wire.OverlayCategories)
		}
		if raw, ok := rawByScene[sceneID]; ok {
			wire.Raw = &raw
		}
		if input, hasAnnotations := inputsByScene[sceneID]; hasAnnotations {
			wire.Annotations = sceneAnnotationsToWire(input)
		}
		if ref, hasVoiceover := scene.Voiceover[language]; hasVoiceover && ref.Timing != nil {
			wire.Voiceover = &overlayplan.Voiceover{Timing: speechTimingToWire(ref.Timing)}
		}
		request.Scenes = append(request.Scenes, wire)
		// The semantic overlay plan only contains generated narration scenes.
		// Fixed-media intro clips have their own localized subtitle renders and
		// belong to the final assembly timeline, but are not scenes in this
		// editorial plan. Including their bounds here makes the Rust compiler
		// reject the request as containing an unknown scene ID.
		request.SceneWindows[sceneID] = bounds
	}
	// Clips and scenes must describe the same set: the Rust compiler validates
	// every clip's scene_id against scenes[] and rejects the whole request on a
	// scene that the plan legitimately left out (a fixed-media intro has its own
	// localized render and is not an editorial scene). Projecting the canonical
	// timeline unfiltered sent that intro clip anyway and failed the run with
	// `invalid clips: unknown scene id "scene-intro"`.
	request.Clips = overlayPlanClips(result, seen)
	// A bullet pin names its bullet by scene, so an unknown scene would style
	// nothing. Reject it instead of shipping a silent no-op. (The style fields
	// themselves were already validated when the canvas was encoded.)
	if err := validateEditorialBulletStyleScenes(request.Canvas.EditorialBulletStyles, seen); err != nil {
		return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: %w", err)
	}
	return request, nil
}

func sceneAnnotationsToWire(in capabilityoverlay.SceneInput) *overlayplan.SceneAnnotations {
	out := &overlayplan.SceneAnnotations{
		Phrases:           timedAnnotationsToWire(in.Phrases),
		Keywords:          timedAnnotationsToWire(in.Keywords),
		Images:            imageCandidatesToWire(in.Images),
		Numbers:           timedAnnotationsToWire(in.Numbers),
		ChartNumbers:      timedAnnotationsToWire(in.ChartNumbers),
		BrandTexts:        timedAnnotationsToWire(in.BrandTexts),
		Quotes:            timedAnnotationsToWire(in.Quotes),
		Products:          imageCandidatesToWire(in.Products),
		Logos:             imageCandidatesToWire(in.Logos),
		Maps:              mapCandidatesToWire(in.Maps),
		MultiEntityGroups: multiEntityGroupsToWire(in.MultiEntityGroups),
		EndUS:             in.EndUS,
	}
	if len(out.Phrases)+len(out.Keywords)+len(out.Images)+len(out.Numbers)+len(out.ChartNumbers)+
		len(out.BrandTexts)+len(out.Quotes)+len(out.Products)+len(out.Logos)+len(out.Maps)+
		len(out.MultiEntityGroups) == 0 {
		return nil
	}
	return out
}

func timedAnnotationsToWire(in []capabilityoverlay.TimedAnnotation) []overlayplan.TimedAnnotation {
	if len(in) == 0 {
		return nil
	}
	out := make([]overlayplan.TimedAnnotation, 0, len(in))
	for _, annotation := range in {
		out = append(out, overlayplan.TimedAnnotation{
			Text:               annotation.Text,
			Type:               annotation.Type,
			StartMS:            annotation.StartMs,
			EndMS:              annotation.EndMs,
			StartUS:            annotation.StartUS,
			DurationUS:         annotation.DurationUS,
			Score:              annotation.Score,
			PresentationRole:   annotation.PresentationRole,
			EditorialCandidate: annotation.EditorialCandidate,
		})
	}
	return out
}

func imageCandidatesToWire(in []capabilityoverlay.ImageCandidate) []overlayplan.ImageCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]overlayplan.ImageCandidate, 0, len(in))
	for _, candidate := range in {
		out = append(out, overlayplan.ImageCandidate{
			AssetID: candidate.AssetID, URL: candidate.URL, SHA256: candidate.SHA256, MediaType: candidate.MediaType,
			StartMS: candidate.StartMs, EndMS: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS, Score: candidate.Score,
			Pinned: candidate.Pinned,
		})
	}
	return out
}

func mapCandidatesToWire(in []capabilityoverlay.MapCandidate) []overlayplan.MapCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]overlayplan.MapCandidate, 0, len(in))
	for _, candidate := range in {
		out = append(out, overlayplan.MapCandidate{
			EntityID: candidate.EntityID, Label: candidate.Label, Scope: candidate.Scope,
			Latitude: candidate.Latitude, Longitude: candidate.Longitude,
			StartMS: candidate.StartMs, EndMS: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS, Score: candidate.Score,
		})
	}
	return out
}

func multiEntityGroupsToWire(in []capabilityoverlay.MultiEntityGroup) []overlayplan.MultiEntityGroup {
	if len(in) == 0 {
		return nil
	}
	out := make([]overlayplan.MultiEntityGroup, 0, len(in))
	for _, group := range in {
		wire := overlayplan.MultiEntityGroup{
			GroupID: group.GroupID, SceneID: group.SceneID, GroupType: string(group.GroupType),
			Count: group.Count, StartMS: group.StartMS, EndMS: group.EndMS, FocusStrategy: group.FocusStrategy,
		}
		for _, item := range group.Items {
			candidate := overlayplan.MultiEntityCandidate{
				EntityID: item.ID, Kind: string(item.Kind), Label: item.Name, StartMS: item.StartMS, EndMS: item.EndMS,
			}
			if item.AssetRef != nil {
				candidate.AssetRef = &overlayplan.AssetRef{
					AssetID: item.AssetRef.AssetID, URL: item.AssetRef.URL,
					SHA256: item.AssetRef.SHA256, MediaType: item.AssetRef.MediaType,
				}
			}
			wire.Items = append(wire.Items, candidate)
		}
		out = append(out, wire)
	}
	return out
}

// OverlayPlanCompiler is the single-call worker contract implemented by
// rustexec.SemanticEngineAdapter. It replaces the retired per-phase Go ports.
type OverlayPlanCompiler interface {
	CompileOverlayPlanV2(context.Context, json.RawMessage) (*capmodel.OverlayPlan, error)
}

// compileOverlayPlanV2 sends one request for the whole run. The request is the
// same envelope the Rust planner validates and seals; no Go selection runs
// before or after it. Render keys are derived state and are stamped by the Go
// seal boundary after adoption.

func compileOverlayPlanV2(
	ctx context.Context,
	compiler OverlayPlanCompiler,
	result *GenerateResult,
	language Language,
	canvas OverlayCanvasSpec,
	planID, videoID, projectID, motionSeed string,
	timedScenes []Scene,
	scenes []capabilityoverlay.SceneInput,
	rawScenes []overlayplan.RawScene,
	imagePlan *overlayplan.ImagePlan,
	timelineBounds map[string][2]int64,
	plates capabilityoverlay.PlateResolver,
) (*capmodel.OverlayPlan, error) {
	request, err := buildOverlayPlanV2Request(result, language, canvas, planID, videoID, projectID, motionSeed, timedScenes, scenes, rawScenes, imagePlan, timelineBounds)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: encode v2 request: %w", err)
	}
	plan, err := compiler.CompileOverlayPlanV2(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: compile v2: %w", err)
	}
	if plan == nil {
		return nil, fmt.Errorf("overlay plan: v2 compiler returned no plan")
	}
	// LocalPath is intentionally producer-only and omitted from the Rust wire.
	// Reattach the already verified cache location by content identity so the
	// local renderer and final-job preflight can use the exact selected bytes
	// without downloading them again. The path is not part of plan identity or
	// serialized to the remote worker.
	localBySHA := make(map[string]capmodel.OverlayAssetRef)
	if canvas.Background != nil {
		for _, bgRef := range canvas.Background.AssetRefs {
			sha := strings.ToLower(strings.TrimSpace(bgRef.SHA256))
			if sha == "" || strings.TrimSpace(bgRef.LocalPath) == "" {
				continue
			}
			localBySHA[sha] = bgRef
		}
	}
	for _, scene := range scenes {
		for _, candidate := range scene.Images {
			sha := strings.ToLower(strings.TrimSpace(candidate.SHA256))
			if sha == "" || strings.TrimSpace(candidate.LocalPath) == "" {
				continue
			}
			localBySHA[sha] = capmodel.OverlayAssetRef{
				AssetID: candidate.AssetID, URL: candidate.URL, LocalPath: candidate.LocalPath,
				SHA256: sha, MediaType: candidate.MediaType,
			}
		}
	}
	if plan.Background != nil {
		for j := range plan.Background.AssetRefs {
			ref := &plan.Background.AssetRefs[j]
			if cached, ok := localBySHA[strings.ToLower(strings.TrimSpace(ref.SHA256))]; ok {
				ref.LocalPath = cached.LocalPath
				if strings.TrimSpace(ref.URL) == "" {
					ref.URL = cached.URL
				}
			}
		}
	}
	for i := range plan.Items {
		for j := range plan.Items[i].AssetRefs {
			ref := &plan.Items[i].AssetRefs[j]
			if cached, ok := localBySHA[strings.ToLower(strings.TrimSpace(ref.SHA256))]; ok {
				ref.LocalPath = cached.LocalPath
				if strings.TrimSpace(ref.URL) == "" {
					ref.URL = cached.URL
				}
			}
		}
	}
	if err := materializeEditorialMapItems(plan, plates, canvas, motionSeed); err != nil {
		return nil, err
	}
	return plan, nil
}

// attachMapAssetLocalPaths reattaches the runtime local path of every map
// raster from the certified plate that owns it. LocalPath is producer-only
// (`json:"-"`), so a plan decoded from a checkpoint or a frozen fixture always
// arrives without it; the bytes are still the same certified assets, and the
// seal must be able to verify them before the renderer uses them.
func attachMapAssetLocalPaths(item *capmodel.OverlayItem, plates capabilityoverlay.PlateResolver) {
	if item == nil || item.Map == nil || plates == nil {
		return
	}
	centers := make([]capmodel.MapCenter, 0, len(item.Map.LODs)+1)
	centers = append(centers, item.Map.Center)
	for _, lod := range item.Map.LODs {
		centers = append(centers, lod.Center)
	}
	byAssetID := make(map[string]capmodel.OverlayAssetRef, len(centers)+1)
	for _, center := range centers {
		plate, ok := plates.ResolvePlate(center.Latitude, center.Longitude)
		if !ok {
			continue
		}
		if id := strings.TrimSpace(plate.Asset.AssetID); id != "" {
			byAssetID[id] = plate.Asset
		}
		for _, lod := range plate.LODs {
			if id := strings.TrimSpace(lod.Asset.AssetID); id != "" {
				byAssetID[id] = lod.Asset
			}
		}
	}
	for i := range item.AssetRefs {
		asset, ok := byAssetID[strings.TrimSpace(item.AssetRefs[i].AssetID)]
		if !ok {
			continue
		}
		if strings.TrimSpace(item.AssetRefs[i].LocalPath) == "" {
			item.AssetRefs[i].LocalPath = asset.LocalPath
		}
		if strings.TrimSpace(item.AssetRefs[i].SHA256) == "" {
			item.AssetRefs[i].SHA256 = asset.SHA256
		}
		if strings.TrimSpace(item.AssetRefs[i].MediaType) == "" {
			item.AssetRefs[i].MediaType = asset.MediaType
		}
	}
}

// materializeEditorialMapItems lowers Rust-selected grounded places through
// the existing certified map-plate resolver. Rust owns which place to show
// and when; this Producer boundary attaches the licensed local/dynamic map
// source needed by the renderer.
// applyChainSegmentPresets mirrors the semantic_role → preset_id decision onto
// every chain segment. Rust coalesces standalone overlays into one chain and
// carries each segment's kind/template, but the preset decision belongs to
// this resolver (ADR-029). Without it RenderingGen lowers the text segments
// with no preset, emits a partial style (fill/stroke only, no font/font_size)
// and the Chronon daemon rejects the whole render plan.
func applyChainSegmentPresets(plan *capmodel.OverlayPlan) {
	if plan == nil {
		return
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		firstPreset := ""
		capmodel.UpdateChainSegmentMaps(item.Params, func(segments []map[string]any) bool {
			changed := false
			for _, segment := range segments {
				preset := strings.TrimSpace(chainSegmentString(segment, "preset_id"))
				if preset == "" {
					templateID := strings.TrimSpace(chainSegmentString(segment, "template_id"))
					if templateID == "" {
						continue
					}
					resolved, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(templateID)
					if !ok {
						continue
					}
					preset = resolved
					segment["preset_id"] = resolved
					changed = true
				}
				if firstPreset == "" {
					firstPreset = preset
				}
			}
			return changed
		})
		// The chain item mirrors its first segment for consumers that do not
		// read chain_segments (motion/preview paths).
		if strings.TrimSpace(item.PresetID) == "" && firstPreset != "" {
			item.PresetID = firstPreset
		}
	}
}

func chainSegmentString(segment map[string]any, key string) string {
	if value, ok := segment[key].(string); ok {
		return value
	}
	return ""
}

func materializeEditorialMapItems(plan *capmodel.OverlayPlan, plates capabilityoverlay.PlateResolver, canvas OverlayCanvasSpec, seed string) error {
	if plan == nil {
		return nil
	}
	applyChainSegmentPresets(plan)
	grouping, groupingEnabled := mapGroupingForCanvas(canvas)

	type pendingMap struct {
		index     int
		candidate capabilityoverlay.MapCandidate
	}
	pendingByScene := make(map[string][]pendingMap)
	sceneOrder := make([]string, 0, len(plan.Items))
	for i := range plan.Items {
		item := &plan.Items[i]
		if strings.TrimSpace(item.PresetID) == "" {
			if preset, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(strings.TrimSpace(item.TemplateID)); ok {
				item.PresetID = preset
			}
		}
		if !strings.EqualFold(strings.TrimSpace(item.Kind), "map") {
			continue
		}
		if item.Map == nil {
			return fmt.Errorf("overlay plan: Rust selected map %q without a grounded map declaration", item.ID)
		}
		entityID, label, scope, latitude, longitude, ok := item.Map.SemanticCandidate()
		if !ok {
			// An already materialized map (a frozen/legacy plan or a future Rust
			// response that includes the full renderer block) needs no plate
			// selection, but its local paths are runtime-only and therefore do
			// not survive the wire/checkpoint round-trip. Reattach them from the
			// certified plate before the seal validates the raster.
			if strings.EqualFold(strings.TrimSpace(item.Map.Provider), "local") && len(item.Map.Pins) > 0 {
				attachMapAssetLocalPaths(item, plates)
				continue
			}
			return fmt.Errorf("overlay plan: Rust map %q has no semantic place candidate", item.ID)
		}
		durationUS := item.DurationUS
		if durationUS <= 0 {
			durationUS = (item.EndMs - item.StartMs) * 1000
		}
		candidate, valid := capabilityoverlay.NewMapCandidate(entityID, label, latitude, longitude, item.StartUS, durationUS, 1, scope)
		if !valid {
			return fmt.Errorf("overlay plan: Rust map %q contains an invalid grounded candidate", item.ID)
		}
		sceneID := strings.TrimSpace(item.SceneID)
		if _, seen := pendingByScene[sceneID]; !seen {
			sceneOrder = append(sceneOrder, sceneID)
		}
		pendingByScene[sceneID] = append(pendingByScene[sceneID], pendingMap{index: i, candidate: candidate})
	}

	ordinal := 0
	originalLen := len(plan.Items)
	dropped := make(map[int]struct{})
	var appended []capmodel.OverlayItem
	for _, sceneID := range sceneOrder {
		entries := pendingByScene[sceneID]
		// Grouping is a scene-level policy: it can only merge places the
		// planner admitted together. A single map keeps the historical
		// per-item lowering, byte for byte.
		if !groupingEnabled || len(entries) < 2 {
			for _, entry := range entries {
				item := &plan.Items[entry.index]
				materialized, found := capabilityoverlay.MaterializeMapCandidate(
					sceneID, entry.candidate, plates, canvas.Width, canvas.Height, ordinal, 0, seed, capabilityoverlay.MapGroupConfig{},
				)
				if !found {
					return fmt.Errorf("overlay plan: Rust-selected map %q has no certified local map plate", item.ID)
				}
				ordinal++
				applyMaterializedMap(item, materialized, plan.DurationMS)
			}
			continue
		}
		candidates := make([]capabilityoverlay.MapCandidate, 0, len(entries))
		for _, entry := range entries {
			candidates = append(candidates, entry.candidate)
		}
		items := capabilityoverlay.MaterializeMapGroup(
			sceneID, candidates, plates, canvas.Width, canvas.Height, ordinal, 0, seed, grouping,
		)
		if len(items) == 0 {
			return fmt.Errorf("overlay plan: Rust-selected maps for scene %q have no certified local map plate", sceneID)
		}
		ordinal += len(items)
		// MaterializeMapGroup returns groups in pin-timing order, which is NOT
		// the Rust plan order (Firenze, Siena, Toscana, Campo). Assign every
		// group to the Rust item that owns one of its pins: positional
		// assignment silently swapped labels and coordinates between maps.
		used := make([]bool, len(entries))
		for j := range items {
			match := -1
			for k := range entries {
				if used[k] {
					continue
				}
				if materializedMapHasEntity(items[j], entries[k].candidate.EntityID) {
					match = k
					break
				}
			}
			if match >= 0 {
				used[match] = true
				applyMaterializedMap(&plan.Items[entries[match].index], items[j], plan.DurationMS)
				continue
			}
			extra := items[j]
			if strings.TrimSpace(extra.PresetID) == "" {
				if preset, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(strings.TrimSpace(extra.TemplateID)); ok {
					extra.PresetID = preset
				}
			}
			appended = append(appended, extra)
		}
		for k := range entries {
			if !used[k] {
				dropped[entries[k].index] = struct{}{}
			}
		}
	}
	if len(dropped) == 0 {
		plan.Items = append(plan.Items, appended...)
		clampMapWindowsToNonOverlappingPresentation(plan)
		plan.Items = capabilityoverlay.MergeShortMapTail(plan.Items, grouping.Pins(), capabilityoverlay.MapMinimumReadableUS, seed)
		return nil
	}
	kept := plan.Items[:0]
	for i := 0; i < originalLen; i++ {
		if _, remove := dropped[i]; remove {
			continue
		}
		kept = append(kept, plan.Items[i])
	}
	plan.Items = append(kept, appended...)
	// Clamp only after the merged/dropped items are gone: clamping against a
	// Rust item that is about to be dropped shortened the surviving group to
	// the dropped item's next start (a 0.6 s map flash).
	clampMapWindowsToNonOverlappingPresentation(plan)
	// A map clamped below the readable floor by the next scene's first map is
	// folded into the preceding map of its own scene; otherwise a 0.94 s Campo
	// remains a flash even though the previous plate covers it.
	plan.Items = capabilityoverlay.MergeShortMapTail(plan.Items, grouping.Pins(), capabilityoverlay.MapMinimumReadableUS, seed)
	return nil
}

// mapGroupingForCanvas resolves the unified map_presentation contract into the
// planner's grouping policy. A nil presentation keeps the historical behavior.
func mapGroupingForCanvas(canvas OverlayCanvasSpec) (capabilityoverlay.MapGroupConfig, bool) {
	presentation := canvas.MapPresentation
	if presentation == nil {
		return capabilityoverlay.MapGroupConfig{}, false
	}
	return capabilityoverlay.MapGroupConfig{
		MaxPinsPerGroup:     presentation.MaxPinsPerGroup,
		NoNarrativeGrouping: !presentation.GroupNearbyPlaces,
		NoRoutes:            !presentation.AllowRoute,
	}, true
}

// clampMapWindowsToNonOverlappingPresentation keeps every map readable without
// stacking two plates over the same frames. Each group gets the certified
// presentation window, but when a later map starts inside it the current map
// ends at that start: two basemaps composited on the same frame are a visible
// defect, while a shorter (still readable) first camera move is not.
func clampMapWindowsToNonOverlappingPresentation(plan *capmodel.OverlayPlan) {
	if plan == nil {
		return
	}
	indices := make([]int, 0, len(plan.Items))
	for i := range plan.Items {
		if strings.EqualFold(strings.TrimSpace(plan.Items[i].Kind), "map") {
			indices = append(indices, i)
		}
	}
	sort.SliceStable(indices, func(a, b int) bool {
		left, right := plan.Items[indices[a]], plan.Items[indices[b]]
		if left.StartUS != right.StartUS {
			return left.StartUS < right.StartUS
		}
		return left.ID < right.ID
	})
	for k := 0; k+1 < len(indices); k++ {
		current := &plan.Items[indices[k]]
		next := plan.Items[indices[k+1]]
		if next.StartUS <= current.StartUS {
			continue
		}
		if current.StartUS+current.DurationUS <= next.StartUS {
			continue
		}
		current.DurationUS = next.StartUS - current.StartUS
		current.EndMs = (current.StartUS + current.DurationUS + 999) / 1000
	}
}

// materializedMapHasEntity reports whether one of the renderer map's pins is
// the given grounded entity. Pin IDs are the planner's entity IDs, so this is
// the only stable join between a grouped map item and the Rust item that
// proposed the place.
func materializedMapHasEntity(item capmodel.OverlayItem, entityID string) bool {
	if item.Map == nil || strings.TrimSpace(entityID) == "" {
		return false
	}
	for _, pin := range item.Map.Pins {
		if strings.TrimSpace(pin.ID) == strings.TrimSpace(entityID) {
			return true
		}
	}
	return false
}

// applyMaterializedMap copies the resolved renderer map onto the Rust-selected
// item. The item keeps its editorial identity (id, scene, priority); the
// certified presentation window (camera lead + readable pin slots, bounded by
// the plan) replaces the raw spoken span, because a place mention is often
// shorter than the map can be read. Clamping to the plan extent keeps a map
// from outliving the video when the mention sits at the tail.
func applyMaterializedMap(item *capmodel.OverlayItem, materialized capmodel.OverlayItem, planDurationMS int64) {
	item.Map = materialized.Map
	item.AssetRefs = materialized.AssetRefs
	item.StartUS = materialized.StartUS
	item.DurationUS = materialized.DurationUS
	item.StartMs = materialized.StartMs
	item.EndMs = materialized.EndMs
	if planDurationMS > 0 {
		planEndUS := planDurationMS * 1000
		if planEndUS > item.StartUS && item.StartUS+item.DurationUS > planEndUS {
			item.DurationUS = planEndUS - item.StartUS
			item.EndMs = (item.StartUS + item.DurationUS + 999) / 1000
		}
	}
	if item.Params == nil {
		item.Params = make(map[string]any, len(materialized.Params))
	}
	for key, value := range materialized.Params {
		if key != "priority" {
			item.Params[key] = value
		}
	}
}
