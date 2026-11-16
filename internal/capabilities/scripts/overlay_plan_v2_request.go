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

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
		Clips:          overlayPlanClips(result),
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

func overlayPlanIdentity(planID, videoID, projectID, motionSeed string, canvas OverlayCanvasSpec) overlayplan.PlanIdentity {
	return overlayplan.PlanIdentity{
		PlanID:        planID,
		VideoID:       videoID,
		ProjectID:     projectID,
		MotionSeed:    motionSeed,
		CoherenceSeed: canvas.CoherenceSeed,
		Width:         canvas.Width,
		Height:        canvas.Height,
		FPSNum:        canvas.FPSNum,
		FPSDen:        canvas.FPSDen,
	}
}

func overlayPlanCanvas(canvas OverlayCanvasSpec) (overlayplan.Canvas, error) {
	directives := make([]json.RawMessage, 0, len(canvas.AnimationDirectives))
	for _, directive := range canvas.AnimationDirectives {
		encoded, err := json.Marshal(directive)
		if err != nil {
			return overlayplan.Canvas{}, fmt.Errorf("overlay plan v2: encode animation directive: %w", err)
		}
		directives = append(directives, encoded)
	}
	presentation := overlayplan.MapPresentation{
		MapType: canvas.MapType, CameraAnimation: canvas.MapCameraAnimation,
		LabelAnimation: canvas.MapLabelAnimation, Animation: canvas.MapAnimation, Level: canvas.MapLevel,
	}
	if canvas.MapPresentation != nil {
		presentation.GroupNearbyPlaces = canvas.MapPresentation.GroupNearbyPlaces
		presentation.MaxPinsPerGroup = canvas.MapPresentation.MaxPinsPerGroup
		presentation.AllowRoute = canvas.MapPresentation.AllowRoute
	}
	return overlayplan.Canvas{
		OverlayCategories:        overlayCategoriesToWire(canvas.OverlayCategories),
		GroupLimits:              cloneIntMap(canvas.OverlayGroupLimits),
		MaxOverlaysPerScene:      canvas.MaxOverlaysPerScene,
		MaxPhraseOverlays:        canvas.MaxPhraseOverlays,
		MaxImageOverlays:         canvas.MaxImageOverlays,
		MaxMapOverlays:           canvas.MaxMapOverlays,
		MapPresentation:          presentation,
		AnimationCounts:          cloneIntMap(canvas.AnimationCounts),
		PhraseMotions:            append([]string(nil), canvas.PhraseMotions...),
		ShortPhraseMotion:        canvas.ShortPhraseMotion,
		LongPhraseMotion:         canvas.LongPhraseMotion,
		ImageMotions:             append([]string(nil), canvas.ImageMotions...),
		ImageWithTextMotions:     append([]string(nil), canvas.ImageWithTextMotions...),
		EntityImageMotions:       append([]string(nil), canvas.EntityImageMotions...),
		EntityCaptionMotions:     append([]string(nil), canvas.EntityCaptionMotions...),
		EntityStyleID:            canvas.EntityStyleID,
		AnimationDirectives:      directives,
		AnimationContractVersion: canvas.AnimationContractVersion,
		AnimationCatalogDigest:   canvas.AnimationCatalogDigest,
		DisableEditorialOverlays: canvas.DisableEditorialOverlays,
		DisableRecapOverlays:     canvas.DisableRecapOverlays,
		DateMotionFamily:         canvas.DateMotionFamily,
		EnableCharts:             canvas.EnableCharts,
		DisableChartOverlays:     canvas.DisableChartOverlays,
		DisableNumberOverlays:    canvas.DisableNumberOverlays,
		MapsOnly:                 canvas.MapsOnly,
		LightLeakOnClipStart:     canvas.RandomLightLeakOnClipStart,
		LightLeakOnOverlayStart:  canvas.RandomLightLeakOnOverlayStart,
	}, nil
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// overlayPlanClips projects the canonical real video spans. Freeze tails stay
// explicit so Rust can skip them; no clip is invented or retimed here.
func overlayPlanClips(result *GenerateResult) []overlayplan.Clip {
	if result == nil || result.CanonicalTimeline == nil {
		return nil
	}
	var out []overlayplan.Clip
	for _, scene := range result.CanonicalTimeline.Segments {
		for index, clip := range scene.EffectiveVideoSegments() {
			if scene.ID == "" || clip.AssetID == "" {
				continue
			}
			out = append(out, overlayplan.Clip{
				SceneID:    scene.ID,
				AssetID:    clip.AssetID,
				Index:      index,
				StartUS:    scene.TimelineStartUS + clip.TimelineOffsetUS,
				DurationUS: clip.TimelineDurationUS,
				Freeze:     clip.Freeze,
			})
		}
	}
	return out
}

func overlayPlanSeal(result *GenerateResult, canvas OverlayCanvasSpec, bounds map[string][2]int64) *overlayplan.Seal {
	durationUS := timelineDurationUS(bounds)
	if result.CanonicalTimeline != nil && result.CanonicalTimeline.DurationUS > durationUS {
		durationUS = result.CanonicalTimeline.DurationUS
	}
	return &overlayplan.Seal{
		AudioDurationUS: durationUS,
		OutputProfileID: canvas.OutputProfileID,
		Background:      canvas.Background,
	}
}

func timelineDurationUS(bounds map[string][2]int64) int64 {
	var duration int64
	for _, window := range bounds {
		if window[1] > duration {
			duration = window[1]
		}
	}
	return duration
}

func overlayPlanBudgets(canvas OverlayCanvasSpec) *overlayplan.Budgets {
	return &overlayplan.Budgets{
		PhraseOverlays: canvas.MaxPhraseOverlays, ImageOverlays: canvas.MaxImageOverlays,
		MapOverlays: canvas.MaxMapOverlays, MaxPerScene: canvas.MaxOverlaysPerScene,
		ShortPhrase: canvas.OverlayGroupLimits["short_phrase"], LongPhrase: canvas.OverlayGroupLimits["long_phrase"],
		Bullet: canvas.OverlayGroupLimits["bullets"],
	}
}

func overlayPlanMedia(result *GenerateResult, language Language, sceneInputs []capabilityoverlay.SceneInput) *overlayplan.Media {
	media := &overlayplan.Media{}
	if result != nil {
		seen := map[string]struct{}{}
		for i := range result.Scenes {
			annotations := annotationsForLanguage(result.Scenes[i], language, result.SourceLanguage)
			if annotations == nil {
				continue
			}
			entities := append(append([]scriptpkg.AnnotatedEntity(nil), annotations.PrimaryEntities...), annotations.SecondaryEntities...)
			for _, entity := range entities {
				if entity.Image == nil || strings.TrimSpace(entity.Image.AssetID) == "" {
					continue
				}
				stable := annotationStableEntityID(entity)
				canonical := annotationCanonicalEntityID(entity)
				key := stable + "\x00" + canonical
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				media.VerifiedEntityAssets = append(media.VerifiedEntityAssets, overlayplan.VerifiedEntityAsset{
					EntityID: stable, CanonicalEntityID: canonical,
					Assets: []overlayplan.AssetRef{{
						AssetID: entity.Image.AssetID, URL: entityImageURL(entity.Image),
						SHA256: entity.Image.SHA256, MediaType: entity.Image.MediaType,
					}},
				})
			}
		}
	}
	for _, input := range sceneInputs {
		if len(input.Images) == 0 {
			continue
		}
		media.SceneImageCandidates = append(media.SceneImageCandidates, overlayplan.SceneImageCandidateSet{
			SceneID: input.ID, Candidates: imageCandidatesToWire(input.Images),
		})
	}
	return media
}

func overlayPlanEntityTimeline(result *GenerateResult, language Language, projectID string, bounds map[string][2]int64) *overlayplan.EntityTimeline {
	durationUS := timelineDurationUS(bounds)
	out := &overlayplan.EntityTimeline{Version: 1, ProjectID: projectID, Language: string(language), DurationUS: durationUS}
	if result.EntityTimeline == nil {
		return out
	}
	out.ProjectID = result.EntityTimeline.ProjectID
	out.Language = result.EntityTimeline.Language
	if result.EntityTimeline.DurationUS > 0 {
		out.DurationUS = result.EntityTimeline.DurationUS
	}
	for _, scene := range result.EntityTimeline.Scenes {
		wireScene := overlayplan.SceneEntityTimeline{
			SceneID: scene.SceneID, SceneIndex: scene.SceneIndex,
			TimelineStartUS: scene.TimelineStartUS, VoiceoverAssetID: scene.VoiceoverAssetID,
		}
		for _, entity := range scene.Entities {
			wireScene.Entities = append(wireScene.Entities, overlayplan.EntityOccurrence{
				EntityID: entity.EntityID, Name: entity.Name, Type: entity.Type,
				SceneID: entity.SceneID, SceneIndex: entity.SceneIndex,
				TextStart: entity.TextStart, TextEnd: entity.TextEnd,
				WordStart: entity.WordStart, WordEnd: entity.WordEnd,
				LocalStartUS: entity.LocalStartUS, LocalEndUS: entity.LocalEndUS,
				TimelineStartUS: entity.TimelineStartUS, AudioStartUS: entity.AudioStartUS, AudioEndUS: entity.AudioEndUS,
				Confidence: entity.Confidence, CanonicalEntityID: entity.EntityID,
			})
		}
		out.Scenes = append(out.Scenes, wireScene)
	}
	return out
}

func overlayCategoriesToWire(in map[scriptpkg.OverlayCategory]scriptpkg.Toggle) map[string]string {
	out := make(map[string]string, len(in))
	for category, toggle := range in {
		out[string(category)] = string(toggle)
	}
	return out
}

func speechTimingToWire(timing *capabilityaudio.SpeechTimingArtifact) *overlayplan.Timing {
	if timing == nil {
		return nil
	}
	out := &overlayplan.Timing{
		DurationUS: timing.DurationUS,
		Words:      make([]overlayplan.WordTiming, 0, len(timing.Words)),
	}
	for _, word := range timing.Words {
		out.Words = append(out.Words, overlayplan.WordTiming{
			Index:   word.Index,
			Text:    word.Text,
			StartUS: word.StartUS,
			EndUS:   word.EndUS,
		})
	}
	return out
}

func editorialManifestToWire(in *scriptpkg.EditorialManifest) *overlayplan.EditorialManifest {
	if in == nil {
		return nil
	}
	out := &overlayplan.EditorialManifest{
		SchemaVersion:  in.SchemaVersion,
		SourceLanguage: in.SourceLanguage,
		ProfileVersion: in.ProfileVersion,
		Certified:      in.Certified,
		Fingerprint:    in.Fingerprint,
		Scenes:         make([]overlayplan.EditorialScene, 0, len(in.Scenes)),
	}
	for _, scene := range in.Scenes {
		wire := overlayplan.EditorialScene{
			SceneID:         scene.SceneID,
			Title:           scene.Title,
			TitleStatus:     scene.TitleStatus,
			TitleSource:     scene.TitleSource,
			GloballyIndexed: scene.GloballyIndexed,
			Bullets:         make([]overlayplan.EditorialBullet, 0, len(scene.Bullets)),
		}
		for _, bullet := range scene.Bullets {
			wire.Bullets = append(wire.Bullets, overlayplan.EditorialBullet{
				SentenceStart: bullet.SentenceStart,
				SentenceEnd:   bullet.SentenceEnd,
				Text:          bullet.Text,
			})
		}
		out.Scenes = append(out.Scenes, wire)
	}
	return out
}

func chapterManifestToWire(in *scriptpkg.ChapterManifest) *overlayplan.ChapterManifest {
	if in == nil {
		return nil
	}
	out := &overlayplan.ChapterManifest{
		SchemaVersion: in.SchemaVersion,
		Chapters:      make([]overlayplan.Chapter, 0, len(in.Chapters)),
	}
	for _, chapter := range in.Chapters {
		wire := overlayplan.Chapter{Bullets: make([]overlayplan.ChapterBullet, 0, len(chapter.Bullets))}
		for _, bullet := range chapter.Bullets {
			wire.Bullets = append(wire.Bullets, overlayplan.ChapterBullet{
				Text:          bullet.Text,
				StartSentence: bullet.StartSentence,
				EndSentence:   bullet.EndSentence,
			})
		}
		out.Chapters = append(out.Chapters, wire)
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
func materializeEditorialMapItems(plan *capmodel.OverlayPlan, plates capabilityoverlay.PlateResolver, canvas OverlayCanvasSpec, seed string) error {
	if plan == nil {
		return nil
	}
	ordinal := 0
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
		materialized, found := capabilityoverlay.MaterializeMapCandidate(
			item.SceneID, candidate, plates, canvas.Width, canvas.Height, ordinal, 0, seed, capabilityoverlay.MapGroupConfig{},
		)
		if !found {
			return fmt.Errorf("overlay plan: Rust-selected map %q has no certified local map plate", item.ID)
		}
		ordinal++
		item.Map = materialized.Map
		item.AssetRefs = materialized.AssetRefs
		if item.Params == nil {
			item.Params = make(map[string]any)
		}
		for key, value := range materialized.Params {
			if key != "priority" {
				item.Params[key] = value
			}
		}
	}
	return nil
}
