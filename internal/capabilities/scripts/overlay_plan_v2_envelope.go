// overlay_plan_v2_envelope.go owns the run-level envelope projection of the
// v2 request: identity, canvas, clips, seal/background, budgets, media,
// entity timeline and the extractive timing/category filters. It is split from
// overlay_plan_v2_request.go by DOMAIN so the request builder stays within the
// repo's max_lines_per_file_strict=600 gate.
package scriptgeneration

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func overlayPlanIdentity(planID, videoID, projectID, motionSeed string, canvas OverlayCanvasSpec) overlayplan.PlanIdentity {
	return overlayplan.PlanIdentity{
		PlanID:     planID,
		VideoID:    videoID,
		ProjectID:  projectID,
		MotionSeed: motionSeed,
		Width:      canvas.Width,
		Height:     canvas.Height,
		FPSNum:     canvas.FPSNum,
		FPSDen:     canvas.FPSDen,
	}
}

func overlayPlanCanvas(canvas OverlayCanvasSpec) (overlayplan.Canvas, error) {
	entityBadgeColor := strings.TrimSpace(canvas.EntityBadgeColor)
	if entityBadgeColor != "" {
		if len(entityBadgeColor) != 7 || entityBadgeColor[0] != '#' {
			return overlayplan.Canvas{}, fmt.Errorf("entity_badge_color must be #RRGGBB")
		}
		if _, err := hex.DecodeString(entityBadgeColor[1:]); err != nil {
			return overlayplan.Canvas{}, fmt.Errorf("entity_badge_color must be #RRGGBB")
		}
	}
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
		presentation.AnimationPool = append([]string(nil), canvas.MapPresentation.AnimationPool...)
		presentation.StylePool = append([]string(nil), canvas.MapPresentation.StylePool...)
	}
	// The pin is validated before it is encoded: a font the runtime does not
	// bundle or a motion the registry does not certify must fail here, with the
	// field named, rather than be dropped by the compiler.
	bulletStyles, err := editorialBulletStylesToWire(canvas.EditorialBulletStyles)
	if err != nil {
		return overlayplan.Canvas{}, fmt.Errorf("overlay plan v2: %w", err)
	}
	// The short-phrase buckets are the registry's, not a second list: the run
	// inherits them unless it carries its own resolved pools. Without them the
	// planner cannot rotate the lane and every phrase falls back to the preset's
	// single motion.
	shortPhraseMotions := cloneStringSliceMap(canvas.ShortPhraseMotionsByWord)
	if len(shortPhraseMotions) == 0 {
		shortPhraseMotions = capabilityoverlay.ShortPhraseMotionsByWord()
	}
	longPhraseMotions := append([]string(nil), canvas.LongPhraseMotions...)
	if len(longPhraseMotions) == 0 {
		longPhraseMotions = capabilityoverlay.LongPhraseMotions()
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
		ShortPhraseMotion:        canvas.ShortPhraseMotion,
		ShortPhraseMotionsByWord: shortPhraseMotions,
		LongPhraseMotions:        longPhraseMotions,
		ImageMotions:             append([]string(nil), canvas.ImageMotions...),
		ImageWithTextMotions:     append([]string(nil), canvas.ImageWithTextMotions...),
		EntityImageMotions:       append([]string(nil), canvas.EntityImageMotions...),
		EntityCaptionMotions:     append([]string(nil), canvas.EntityCaptionMotions...),
		EntityStyleID:            canvas.EntityStyleID,
		EntityBadgeColor:         entityBadgeColor,
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
		EditorialBulletStyles:    bulletStyles,
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

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, value := range in {
		out[key] = append([]string(nil), value...)
	}
	return out
}

// overlayPlanClips projects the canonical real video spans of the scenes this
// request actually carries. Freeze tails stay explicit so Rust can skip them; no
// clip is invented or retimed here. A timeline segment whose scene the request
// did not include (fixed-media intro/outro) is not part of the editorial plan
// and must not be projected, or the request contradicts itself.
func overlayPlanClips(result *GenerateResult, planScenes map[string]struct{}) []overlayplan.Clip {
	if result == nil || result.CanonicalTimeline == nil || len(planScenes) == 0 {
		return nil
	}
	var out []overlayplan.Clip
	for _, scene := range result.CanonicalTimeline.Segments {
		sceneID := strings.TrimSpace(scene.ID)
		if _, inPlan := planScenes[sceneID]; !inPlan {
			continue
		}
		for index, clip := range scene.EffectiveVideoSegments() {
			if sceneID == "" || clip.AssetID == "" {
				continue
			}
			out = append(out, overlayplan.Clip{
				SceneID:    sceneID,
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
		Background:      overlayPlanBackground(canvas.Background),
	}
}

// overlayPlanBackground projects the sealed background onto the typed wire
// contract. Nil stays nil so the envelope omits an unset background.
func overlayPlanBackground(src *capmodel.OverlayBackground) *overlayplan.Background {
	if src == nil {
		return nil
	}
	out := &overlayplan.Background{
		Kind:    src.Kind,
		Color:   append([]float64(nil), src.Color...),
		Fit:     src.Fit,
		Opacity: src.Opacity,
		Loop:    src.Loop,
		Style:   src.Style,
	}
	for _, ref := range src.AssetRefs {
		out.AssetRefs = append(out.AssetRefs, overlayplan.AssetRef{
			AssetID: ref.AssetID, URL: ref.URL, SHA256: ref.SHA256, MediaType: ref.MediaType,
		})
	}
	return out
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

// isLocalizedSourceEditorial reports whether a plan targets a language other
// than the language the editorial manifest was extracted from. Equal or empty
// source languages keep the historical behaviour and are not "localized".
func isLocalizedSourceEditorial(manifest *scriptpkg.EditorialManifest, language Language) bool {
	if manifest == nil {
		return false
	}
	source := strings.TrimSpace(manifest.SourceLanguage)
	target := strings.TrimSpace(string(language))
	return source != "" && target != "" && !strings.EqualFold(source, target)
}

func withoutSourceEditorialCategories(canvas OverlayCanvasSpec) OverlayCanvasSpec {
	if canvas.OverlayCategories == nil {
		canvas.OverlayCategories = make(map[scriptpkg.OverlayCategory]scriptpkg.Toggle, 2)
	}
	canvas.OverlayCategories[scriptpkg.OverlayCategoryTitles] = scriptpkg.ToggleDisabled
	canvas.OverlayCategories[scriptpkg.OverlayCategoryBullets] = scriptpkg.ToggleDisabled
	return canvas
}

func withoutSourceEditorialWireCategories(in map[string]string) map[string]string {
	if in == nil {
		in = make(map[string]string, 2)
	}
	in[string(scriptpkg.OverlayCategoryTitles)] = string(scriptpkg.ToggleDisabled)
	in[string(scriptpkg.OverlayCategoryBullets)] = string(scriptpkg.ToggleDisabled)
	return in
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
