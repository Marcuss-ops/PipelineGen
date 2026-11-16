// overlay_plan_v2_request.go — producer for the versioned 13d contract.
//
// The request is built from the same inputs the interleaved planner phases
// already receive. It is deliberately not wired into the compilation path yet:
// the next slice ports the scene projection into overlay-compiler and replaces
// the Go chapter/editorial functions with this envelope. Keeping the producer
// separate lets the wire shape be parity-tested before the cutover.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
	timelineBounds map[string][2]int64,
) (overlayplan.V2Request, error) {
	if result == nil {
		return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: result is required")
	}
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return overlayplan.V2Request{}, fmt.Errorf("overlay plan v2: plan_id and video_id are required")
	}
	canvas = canvas.withDefaults()
	request := overlayplan.V2Request{
		SchemaVersion:  overlayplan.SchemaVersionV2,
		Plan:           overlayPlanIdentity(planID, videoID, projectID, motionSeed, canvas),
		Language:       string(language),
		SourceLanguage: string(result.SourceLanguage),
		Canvas:         overlayPlanCanvas(canvas),
		Scenes:         make([]overlayplan.Scene, 0, len(timedScenes)),
		Editorial:      editorialManifestToWire(result.Editorial),
		Chapters:       chapterManifestToWire(result.ChapterManifest),
	}
	seen := make(map[string]struct{}, len(timedScenes))
	inputsByScene := make(map[string]capabilityoverlay.SceneInput, len(sceneInputs))
	for _, input := range sceneInputs {
		inputsByScene[strings.TrimSpace(input.ID)] = input
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
			TimelineStartUS:    bounds[0],
			DurationUS:         bounds[1] - bounds[0],
		}
		if input, hasAnnotations := inputsByScene[sceneID]; hasAnnotations {
			wire.Annotations = sceneAnnotationsToWire(input)
		}
		if ref, hasVoiceover := scene.Voiceover[language]; hasVoiceover && ref.Timing != nil {
			wire.Voiceover = &overlayplan.Voiceover{Timing: speechTimingToWire(ref.Timing)}
		}
		request.Scenes = append(request.Scenes, wire)
	}
	return request, nil
}

func sceneAnnotationsToWire(in capabilityoverlay.SceneInput) *overlayplan.SceneAnnotations {
	if len(in.Phrases) == 0 {
		return nil
	}
	out := &overlayplan.SceneAnnotations{Phrases: make([]overlayplan.TimedAnnotation, 0, len(in.Phrases))}
	for _, phrase := range in.Phrases {
		out.Phrases = append(out.Phrases, overlayplan.TimedAnnotation{
			Text:               phrase.Text,
			Type:               phrase.Type,
			StartMS:            phrase.StartMs,
			EndMS:              phrase.EndMs,
			StartUS:            phrase.StartUS,
			DurationUS:         phrase.DurationUS,
			Score:              phrase.Score,
			PresentationRole:   phrase.PresentationRole,
			EditorialCandidate: phrase.EditorialCandidate,
		})
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

func overlayPlanCanvas(canvas OverlayCanvasSpec) overlayplan.Canvas {
	return overlayplan.Canvas{
		OverlayCategories:        overlayCategoriesToWire(canvas.OverlayCategories),
		MaxOverlaysPerScene:      canvas.MaxOverlaysPerScene,
		MaxPhraseOverlays:        canvas.MaxPhraseOverlays,
		MaxImageOverlays:         canvas.MaxImageOverlays,
		MaxMapOverlays:           canvas.MaxMapOverlays,
		DisableEditorialOverlays: canvas.DisableEditorialOverlays,
		DisableRecapOverlays:     canvas.DisableRecapOverlays,
		EnableCharts:             canvas.EnableCharts,
		DisableChartOverlays:     canvas.DisableChartOverlays,
		MapsOnly:                 canvas.MapsOnly,
	}
}

func overlayCategoriesToWire(in map[scriptpkg.OverlayCategory]scriptpkg.Toggle) map[string]string {
	if in == nil {
		return nil
	}
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
