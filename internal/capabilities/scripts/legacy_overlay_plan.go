package scriptgeneration

// legacy_overlay_plan.go bridges the batch script.generate_item result to the
// same semantic OverlayPlan compiler used by the durable Runner. The bridge is
// intentionally in-memory: it carries the exact word timing artifact from the
// voiceover call, builds the canonical scene timeline, and then delegates all
// overlay semantics to the existing SSOT compiler.

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// CompileOverlayPlanFromGenerationResult adapts the legacy/batch domain result
// to the canonical overlay compiler. timingArtifacts must contain the
// word-level artifact captured by the exact TTS call that produced each scene
// audio file; no timestamp is inferred from text length or from bundle links.
func CompileOverlayPlanFromGenerationResult(
	result *scriptpkg.GenerationResult,
	language Language,
	timingArtifacts map[string]*capabilityaudio.SpeechTimingArtifact,
	background *scriptpkg.OverlayBackgroundSpec,
	planID string,
	projectID string,
) (*capabilityoverlay.OverlayPlan, error) {
	return CompileOverlayPlanFromGenerationResultWithStyle(
		result, language, timingArtifacts, background, nil, planID, projectID,
	)
}

// CompileOverlayPlanFromGenerationResultWithStyle is the batch bridge used by
// script.generate_item. It keeps the legacy signature above source-compatible
// while carrying the same caller-owned overlay style that the durable Runner
// already applies. The style is a render override only; preset selection and
// timing remain owned by the canonical overlay compiler.
func CompileOverlayPlanFromGenerationResultWithStyle(
	result *scriptpkg.GenerationResult,
	language Language,
	timingArtifacts map[string]*capabilityaudio.SpeechTimingArtifact,
	background *scriptpkg.OverlayBackgroundSpec,
	style *scriptpkg.OverlayStyleSpec,
	planID string,
	projectID string,
) (*capabilityoverlay.OverlayPlan, error) {
	if result == nil {
		return nil, nil
	}
	language = Language(strings.TrimSpace(string(language)))
	if language == "" {
		return nil, fmt.Errorf("legacy overlay plan: language is required")
	}
	if strings.TrimSpace(planID) == "" {
		return nil, fmt.Errorf("legacy overlay plan: plan_id is required")
	}

	capResult := &GenerateResult{
		Title:      result.Title,
		OutputName: result.Title,
		AudioMode:  capabilityaudio.AudioMode(strings.ToUpper(strings.TrimSpace(result.AudioMode))),
	}
	if capResult.AudioMode == "" {
		capResult.AudioMode = capabilityaudio.AudioModeCombinedTimeline
	}
	capResult.Scenes = make([]Scene, 0, len(result.Output.SpecScene.Scenes))
	for i, source := range result.Output.SpecScene.Scenes {
		if strings.TrimSpace(source.ID) == "" {
			return nil, fmt.Errorf("legacy overlay plan: scene %d has empty id", i)
		}
		if source.Index != i {
			return nil, fmt.Errorf("legacy overlay plan: scene %s has index %d, want %d", source.ID, source.Index, i)
		}
		if source.ExecutionMode.IsFixedMedia() {
			// Fixed intro/outro scenes are retained in the canonical timeline but
			// do not carry generated speech timing or semantic overlay input.
			capResult.Scenes = append(capResult.Scenes, Scene{
				ID: source.ID, Index: i, Text: map[Language]string{language: source.DisplayText},
				ExecutionMode: source.ExecutionMode, FixedPlayback: cloneFixedPlayback(source.FixedPlayback),
				Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip},
			})
			continue
		}

		key := fmt.Sprintf("%s:%d", strings.ToLower(string(language)), i)
		artifact := timingArtifacts[key]
		durationMS := int64(0)
		if source.Bindings.Voiceover != nil {
			durationMS = source.Bindings.Voiceover.DurationMs
		}
		if durationMS <= 0 && artifact != nil {
			durationMS = (artifact.DurationUS + 999) / 1000
		}
		if durationMS <= 0 {
			return nil, fmt.Errorf("legacy overlay plan: scene %s has no measured voiceover duration", source.ID)
		}

		voiceoverID := fmt.Sprintf("%s:%s", result.ItemID, source.ID)
		voiceover := AudioReference{
			ID:       voiceoverID,
			Duration: float64(durationMS) / 1000,
			Timing:   artifact,
		}
		if source.Bindings.Voiceover != nil {
			voiceover.URL = source.Bindings.Voiceover.Link
			voiceover.FilePath = source.Bindings.Voiceover.LocalPath
		}
		capResult.Scenes = append(capResult.Scenes, Scene{
			ID: source.ID, Index: i, DurationMS: durationMS,
			Text:          map[Language]string{language: source.Text},
			Voiceover:     map[Language]AudioReference{language: voiceover},
			ExecutionMode: source.ExecutionMode, Annotations: source.Annotations,
			Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: voiceoverID},
			AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: voiceoverID}},
		})
	}
	if len(capResult.Scenes) == 0 {
		return nil, nil
	}
	// The batch postprocessor owns the enriched VidRush segment surface while
	// the overlay compiler consumes scene-local annotations. Project the exact
	// enriched segments here, using the same grounding rules as the durable
	// runner, before compiling the entity timeline and phrase intents.
	applySegmentEntityAnnotations(capResult, language, result.Segments)

	timeline, err := CompileCanonicalTimeline(*capResult)
	if err != nil {
		return nil, fmt.Errorf("legacy overlay plan: compile canonical timeline: %w", err)
	}
	capResult.CanonicalTimeline = &timeline
	if err := compileResultEntityTimeline(capResult, language); err != nil {
		return nil, fmt.Errorf("legacy overlay plan: compile entity timeline: %w", err)
	}
	canvas := OverlayCanvasSpec{
		Background: overlayBackgroundFromPayload(background),
		Style:      style,
	}
	return CompileOverlayPlan(capResult, language, canvas, planID, planID, projectID)
}
