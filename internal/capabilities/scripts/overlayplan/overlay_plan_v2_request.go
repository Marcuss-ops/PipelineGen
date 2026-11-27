// overlay_plan_v2_request.go — producer and single-call cutover for the
// versioned 13d contract.
//
// The request is built from the certified scene projection. CompileOverlayPlanV2
// sends that complete envelope in one call to the configured v2 compiler; the
// returned plan is adopted only after the Go seal boundary validates it.
package overlayplan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

// BuildOverlayPlanV2Request maps the planner's scenes and the two extractive
// manifests onto the versioned full-plan envelope. It fails closed on identity
// or timing that the planner would otherwise have to guess.
func BuildOverlayPlanV2Request(
	result *contract.GenerateResult,
	language contract.Language,
	canvas OverlayCanvasSpec,
	planID, videoID, projectID, motionSeed string,
	timedScenes []contract.Scene,
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
	canvas = canvas.WithDefaults()
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
	canvasWire, err := OverlayPlanCanvas(canvas)
	if err != nil {
		return overlayplan.V2Request{}, err
	}
	// The offset is derived from the same motion seed that rotates the other
	// lanes. It used to be computed by PrepareOverlayPlanCompile and then
	// dropped before the wire, so every plan started its image rotation at 0.
	canvasWire.ImageMotionOffset = capabilityoverlay.ImageMotionOffsetForSeed(motionSeed)
	media := OverlayPlanMedia(result, language, sceneInputs)
	request := overlayplan.V2Request{
		Operation:      "compile_editorial_plan",
		SchemaVersion:  overlayplan.SchemaVersionV2,
		Plan:           overlayPlanIdentity(planID, videoID, projectID, motionSeed, canvas),
		Language:       string(language),
		SourceLanguage: string(result.SourceLanguage),
		Seal:           overlayPlanSeal(result, canvas, timelineBounds),
		Canvas:         canvasWire,
		Scenes:         make([]overlayplan.Scene, 0, len(timedScenes)),
		EntityTimeline: OverlayPlanEntityTimeline(result, language, projectID, timelineBounds, OverlayPlanCanonicalEntityIDs(result, media)),
		Media:          media,
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
	request.Clips = OverlayPlanClips(result, seen)
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

// CompileOverlayPlanV2 sends one request for the whole run. The request is the
// same envelope the Rust planner validates and seals; no Go selection runs
// before or after it. Render keys are derived state and are stamped by the Go
// seal boundary after adoption.

func CompileOverlayPlanV2(
	ctx context.Context,
	compiler OverlayPlanCompiler,
	result *contract.GenerateResult,
	language contract.Language,
	canvas OverlayCanvasSpec,
	planID, videoID, projectID, motionSeed string,
	timedScenes []contract.Scene,
	scenes []capabilityoverlay.SceneInput,
	rawScenes []overlayplan.RawScene,
	imagePlan *overlayplan.ImagePlan,
	timelineBounds map[string][2]int64,
	plates capabilityoverlay.PlateResolver,
) (*capmodel.OverlayPlan, error) {
	request, err := BuildOverlayPlanV2Request(result, language, canvas, planID, videoID, projectID, motionSeed, timedScenes, scenes, rawScenes, imagePlan, timelineBounds)
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
	// The creative seed is decided ONCE, here, and travels with the plan to
	// RenderingGen: it is the same entropy that rotated every planner lane, so a
	// new render resamples the entity styles and a replay keeps them. Before
	// this stamp the field was never written, so RenderingGen silently fell back
	// to the stable project identity and every run of the same content looked
	// identical (ANIMRAND-01). It is render content and participates in the
	// fingerprint.
	plan.EntityVariationSeed = strings.TrimSpace(motionSeed)
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
	// An entity portrait is not a scene image, so the scene loop above never
	// sees it. Reattach its already-materialized cache location from the same
	// unified surface (bundle or annotation binding) that resolved it.
	for sha, ref := range EntityAssetLocalPaths(result) {
		if _, exists := localBySHA[sha]; !exists {
			localBySHA[sha] = ref
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
	if err := MaterializeEditorialMapItems(plan, plates, canvas, motionSeed); err != nil {
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
