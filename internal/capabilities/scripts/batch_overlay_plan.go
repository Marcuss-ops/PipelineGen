package scriptgeneration

// batch_overlay_plan.go bridges the batch script.generate_item result to the
// same single-call editorial_plan.v2 compiler used by the durable Runner. The
// bridge is intentionally in-memory: it carries the exact word timing artifact
// from the voiceover call, builds the canonical scene timeline, and then
// delegates all overlay semantics to the Rust compiler.

import (
	"context"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// CompileOverlayPlanFromGenerationResult is the batch bridge with an explicit
// v2 compiler. The compiler is mandatory: all overlay selection and timing are
// owned by the semantic engine.
func CompileOverlayPlanFromGenerationResult(
	ctx context.Context,
	result *scriptpkg.GenerationResult,
	language Language,
	timingArtifacts map[string]*capabilityaudio.SpeechTimingArtifact,
	background *scriptpkg.OverlayBackgroundSpec,
	style *scriptpkg.OverlayStyleSpec,
	maxPhraseOverlays int,
	planID string,
	projectID string,
	compiler OverlayPlanCompiler,
	aligner PhraseAligner,
) (*capmodel.OverlayPlan, error) {
	if result == nil {
		return nil, nil
	}
	language = Language(strings.TrimSpace(string(language)))
	if language == "" {
		return nil, fmt.Errorf("batch overlay plan: language is required")
	}
	if strings.TrimSpace(planID) == "" {
		return nil, fmt.Errorf("batch overlay plan: plan_id is required")
	}

	capResult := &GenerateResult{
		SourceLanguage: language,
		Title:          result.Title,
		OutputName:     result.Title,
		AudioMode:      capabilityaudio.AudioMode(strings.ToUpper(strings.TrimSpace(result.AudioMode))),
		phraseAligner:  aligner,
	}
	if capResult.AudioMode == "" {
		capResult.AudioMode = capabilityaudio.AudioModeCombinedTimeline
	}
	capResult.Scenes = make([]Scene, 0, len(result.Output.SpecScene.Scenes))
	for i, source := range result.Output.SpecScene.Scenes {
		if strings.TrimSpace(source.ID) == "" {
			return nil, fmt.Errorf("batch overlay plan: scene %d has empty id", i)
		}
		if source.Index != i {
			return nil, fmt.Errorf("batch overlay plan: scene %s has index %d, want %d", source.ID, source.Index, i)
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
		// Verbatim render-only scenes can carry an explicit editorial window
		// without synthesizing voiceover. Preserve that duration as the final
		// fallback so map and phrase overlays can be planned in audio-free jobs.
		if durationMS <= 0 && source.DurationMS > 0 {
			durationMS = source.DurationMS
		}
		if durationMS <= 0 {
			return nil, fmt.Errorf("batch overlay plan: scene %s has no resolved duration", source.ID)
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
		return nil, fmt.Errorf("batch overlay plan: compile canonical timeline: %w", err)
	}
	capResult.CanonicalTimeline = &timeline
	if err := compileResultEntityTimeline(ctx, aligner, capResult, language); err != nil {
		return nil, fmt.Errorf("batch overlay plan: compile entity timeline: %w", err)
	}
	canvas := OverlayCanvasSpec{
		Background:        overlayBackgroundFromPayload(background),
		Style:             style,
		MaxPhraseOverlays: maxPhraseOverlays,
	}
	return CompileOverlayPlanWithRustResolver(ctx, capResult, language, canvas, planID, planID, projectID, nil, compiler)
}
