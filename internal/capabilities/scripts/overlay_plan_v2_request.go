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
	grouping, _ := mapGroupingForCanvas(canvas)
	ordinal := 0
	originalLen := len(plan.Items)
	dropped := make(map[int]struct{})
	var appended []capmodel.OverlayItem
	for i := 0; i < originalLen; i++ {
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
		if _, _, _, _, _, ok := item.Map.SemanticCandidate(); !ok {
			// An already materialized map (a frozen/legacy plan or a response
			// that includes the full renderer block) needs no plate selection,
			// but its local paths are runtime-only and therefore do not survive
			// the wire/checkpoint round-trip. Reattach them from the certified
			// plate before the seal validates the raster.
			if strings.EqualFold(strings.TrimSpace(item.Map.Provider), "local") && len(item.Map.Pins) > 0 {
				attachMapAssetLocalPaths(item, plates)
				continue
			}
			return fmt.Errorf("overlay plan: Rust map %q has no semantic place candidate", item.ID)
		}
		// Membership, spoken order, the pin cap and the readable floor are the
		// Rust planner's decisions; this loop only attaches a certified plate.
		// It never re-groups, so a rendered map can no longer be matched to a
		// different Rust item (the swapped-label defect).
		pins, err := mapGroupPins(item)
		if err != nil {
			return err
		}
		// The requested cartographic level is a hard scope gate: a country pin
		// inside a city plate is noise, not a map.
		if grouping.Level != "" {
			kept := make([]capabilityoverlay.MapCandidate, 0, len(pins))
			for _, pin := range pins {
				if capabilityoverlay.ScopeFitsMapLevel(pin.Scope, grouping.Level) {
					kept = append(kept, pin)
				}
			}
			pins = kept
			if len(pins) == 0 {
				dropped[i] = struct{}{}
				continue
			}
		}
		sceneID := strings.TrimSpace(item.SceneID)
		materialized := capabilityoverlay.MaterializeMapPins(
			sceneID, pins, plates, canvas.Width, canvas.Height, ordinal, 0, seed, grouping,
		)
		if len(materialized) == 0 {
			return fmt.Errorf("overlay plan: Rust-selected map %q has no certified local map plate", item.ID)
		}
		ordinal += len(materialized)
		applyMaterializedMap(item, materialized[0])
		// A group no single plate covers is split by plate, never dropped: the
		// extra shots keep their own pins and are appended as derived items.
		for _, extra := range materialized[1:] {
			if strings.TrimSpace(extra.PresetID) == "" {
				if preset, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(strings.TrimSpace(extra.TemplateID)); ok {
					extra.PresetID = preset
				}
			}
			appended = append(appended, extra)
		}
	}
	if len(dropped) > 0 {
		kept := plan.Items[:0]
		for i := 0; i < originalLen; i++ {
			if _, remove := dropped[i]; remove {
				continue
			}
			kept = append(kept, plan.Items[i])
		}
		plan.Items = kept
	}
	plan.Items = append(plan.Items, appended...)
	if err := applyRequestedMapAnimation(plan, grouping.Animation); err != nil {
		return err
	}
	return nil
}

// mapGroupPins resolves the pins of one Rust map item. The group is
// authoritative; only an item that declares no group at all falls back to its
// single semantic place.
func mapGroupPins(item *capmodel.OverlayItem) ([]capabilityoverlay.MapCandidate, error) {
	pins, err := capabilityoverlay.MapGroupPinsFromParams(item.Params)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: Rust map %q: %w", item.ID, err)
	}
	if len(pins) > 0 {
		return pins, nil
	}
	entityID, label, scope, latitude, longitude, ok := item.Map.SemanticCandidate()
	if !ok {
		return nil, fmt.Errorf("overlay plan: Rust map %q has no semantic place candidate", item.ID)
	}
	durationUS := item.DurationUS
	if durationUS <= 0 {
		durationUS = (item.EndMs - item.StartMs) * 1000
	}
	candidate, valid := capabilityoverlay.NewMapCandidate(entityID, label, latitude, longitude, item.StartUS, durationUS, 1, scope)
	if !valid {
		return nil, fmt.Errorf("overlay plan: Rust map %q contains an invalid grounded candidate", item.ID)
	}
	return []capabilityoverlay.MapCandidate{candidate}, nil
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
		Level:               presentation.Level,
		Animation:           presentation.Animation,
	}, true
}

// applyRequestedMapAnimation makes map_presentation.animation the single owner
// of the composition when the caller named one. The planner still seeds a
// compatible default for random/auto, but an explicit certified id is applied
// to every map item and admitted against that item's real pins (count, scope,
// territory and area-glow data). An incompatible explicit request fails here,
// not inside the renderer after the expensive plate work.
func applyRequestedMapAnimation(plan *capmodel.OverlayPlan, requested string) error {
	requested = strings.TrimSpace(requested)
	if plan == nil || requested == "" || strings.EqualFold(requested, "random") || strings.EqualFold(requested, "auto") {
		return nil
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if !strings.EqualFold(strings.TrimSpace(item.Kind), "map") || item.Map == nil {
			continue
		}
		pins := make([]map[string]any, 0, len(item.Map.Pins))
		for _, pin := range item.Map.Pins {
			pins = append(pins, map[string]any{
				"id": pin.ID, "label": pin.Label, "scope": pin.Scope,
				"latitude": pin.Latitude, "longitude": pin.Longitude,
			})
		}
		input := capabilityoverlay.MapCompositionInput{
			PinCount:         len(pins),
			Pins:             pins,
			DurationUS:       item.DurationUS,
			Strategy:         mapStringParam(item.Params, "map_group_strategy"),
			AreaGlowRadiusKM: item.Map.AreaGlowRadiusKM,
		}
		if len(item.Map.Pins) > 0 {
			pin := item.Map.Pins[0]
			input.TerritoryScope = pin.Scope
			if feature, ok := capabilityoverlay.ResolveMapTerritoryFeature(
				capabilityoverlay.MapTerritoryCatalogDir(), pin.Latitude, pin.Longitude,
			); ok {
				input.TerritoryFeature = feature
			}
		}
		if err := capabilityoverlay.AdmitMapComposition(requested, input, capabilityoverlay.MapCompositionCertified()); err != nil {
			return fmt.Errorf("map_presentation.animation %q cannot be applied to map %q: %w", requested, item.ID, err)
		}
		if item.Params == nil {
			item.Params = make(map[string]any, 1)
		}
		item.Params["map_animation"] = requested
	}
	return nil
}

func mapStringParam(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
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
// item. The item keeps its editorial identity (id, scene, priority) AND its
// window: the Rust planner owns the camera minimum, the readable floor and the
// tail bound now, so this boundary only attaches the certified plate and the
// per-pin schedule. Re-timing the item here would make Go a second owner of the
// floor (the defect the late clamp used to paper over).
func applyMaterializedMap(item *capmodel.OverlayItem, materialized capmodel.OverlayItem) {
	item.Map = materialized.Map
	item.AssetRefs = materialized.AssetRefs
	if item.Params == nil {
		item.Params = make(map[string]any, len(materialized.Params))
	}
	for key, value := range materialized.Params {
		if key != "priority" {
			item.Params[key] = value
		}
	}
	// The materialized schedule is relative to that shot's own start. The item
	// keeps the Rust start, so shift the offsets onto it: a level-gated or
	// plate-dropped pin must not activate earlier than it was spoken.
	if delta := materialized.StartUS - item.StartUS; delta != 0 {
		if timing, ok := capabilityoverlay.MapPinTimingsFromParams(materialized.Params); ok {
			item.Params["map_pin_timing"] = timing.WithOffset(delta).Params()
		}
	}
}
