package overlayplan

import (
	"context"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// batch_overlay_plan.go bridges the batch script.generate_item result to the
// same single-call editorial_plan.v2 compiler used by the durable Runner. The
// bridge is intentionally in-memory: it carries the exact word timing artifact
// from the voiceover call, builds the canonical scene timeline, and then
// delegates all overlay semantics to the Rust compiler.

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
		AudioOutput: contract.AudioOutput{
			AudioMode: capabilityaudio.AudioMode(strings.ToUpper(strings.TrimSpace(result.AudioMode))),
		},
		Aligner: aligner,
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
				ExecutionMode: source.ExecutionMode, FixedPlayback: CloneFixedPlayback(source.FixedPlayback),
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
	ApplySegmentEntityAnnotations(capResult, language, result.Segments)

	timeline, err := CompileCanonicalTimeline(*capResult)
	if err != nil {
		return nil, fmt.Errorf("batch overlay plan: compile canonical timeline: %w", err)
	}
	capResult.CanonicalTimeline = &timeline
	if err := CompileResultEntityTimeline(ctx, aligner, capResult, language); err != nil {
		return nil, fmt.Errorf("batch overlay plan: compile entity timeline: %w", err)
	}
	canvas := OverlayCanvasSpec{
		Background:        overlayBackgroundFromPayload(background),
		Style:             style,
		MaxPhraseOverlays: maxPhraseOverlays,
	}
	return CompileOverlayPlanWithRustResolver(ctx, capResult, language, canvas, planID, planID, projectID, nil, compiler)
}

// ApplyImageAssetStylesForResult applies the per-pin styles to the source plan
// and every localized plan: translation never changes the stills, so a pin's
// look belongs to the same scene in each language.
func ApplyImageAssetStylesForResult(result *GenerateResult, pins []scriptpkg.OverlayImageAsset, runStyle *scriptpkg.OverlayImageStyleSpec) error {
	if result == nil {
		return nil
	}
	plans := make([]*capmodel.OverlayPlan, 0, 1+len(result.LocalizedOverlayPlans))
	if result.OverlayPlan != nil {
		plans = append(plans, result.OverlayPlan)
	}
	for _, plan := range result.LocalizedOverlayPlans {
		if plan != nil {
			plans = append(plans, plan)
		}
	}
	return ApplyImageAssetStyles(plans, pins, runStyle)
}

func ApplyImageAssetStyles(plans []*capmodel.OverlayPlan, pins []scriptpkg.OverlayImageAsset, runStyle *scriptpkg.OverlayImageStyleSpec) error {
	sceneStyles, sceneOrder := groupImageStylesByScene(pins)
	if len(sceneOrder) == 0 {
		return nil
	}
	// The pin style is an override of the exact run-level style the compiler
	// applies, defaults included, so an unset pin field keeps that look.
	base := effectiveImageStyle(runStyle)
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		// A pin styles a still. Entity cards are image surfaces too, but they are
		// not stills: letting them into the positional list made a portrait card
		// consume the slot of the next still, which then kept the planner's
		// default geometry (r6: the third photo stayed 1340x760/contain while the
		// entity card got its height pin).
		imageItemsByScene := imageOverlayItemsBySceneWhere(plan, func(item *capmodel.OverlayItem) bool {
			return strings.EqualFold(strings.TrimSpace(item.Kind), "image")
		})
		changed := false
		for _, scene := range sceneOrder {
			overrides := sceneStyles[scene]
			candidates := imageItemsByScene[scene]
			if len(candidates) == 0 {
				// The still is optional media: it may be missing because the
				// provider returned no verified asset, or because the planner
				// folded it into an image_chain with an entity portrait. A pin
				// styles a standalone image, so there is nothing to style here;
				// failing the whole language for an optional still turned a
				// cosmetic pin into a dead job (r134). The overlay audit keeps
				// the missing item visible.
				continue
			}
			if len(overrides) > len(candidates) {
				// Positional pins beyond the planned stills have no target.
				overrides = overrides[:len(candidates)]
			}
			for index, override := range overrides {
				item := candidates[index]
				applyOverlayImageStyle(item, capoverlay.MergeImageStyle(base, override))
				item.RenderKey = ""
				changed = true
			}
		}
		if !changed {
			continue
		}
		plan.Fingerprint = ""
		if err := plan.Validate(); err != nil {
			return fmt.Errorf("seal overlay image style plan %q: %w", plan.PlanID, err)
		}
	}
	return nil
}

// groupImageStylesByScene keeps declaration order for scenes while grouping the
// style entries of the same scene. Scene keys are compared case-insensitively,
// exactly like the planner's scene identity.
func groupImageStylesByScene(pins []scriptpkg.OverlayImageAsset) (map[string][]*scriptpkg.OverlayImageStyleSpec, []string) {
	styles := make(map[string][]*scriptpkg.OverlayImageStyleSpec)
	order := make([]string, 0)
	seen := make(map[string]struct{}, len(pins))
	for _, pin := range pins {
		if pin.Style == nil {
			continue
		}
		scene := strings.ToLower(strings.TrimSpace(pin.SceneID))
		if scene == "" {
			continue
		}
		if _, ok := seen[scene]; !ok {
			seen[scene] = struct{}{}
			order = append(order, scene)
		}
		styles[scene] = append(styles[scene], pin.Style)
	}
	return styles, order
}

// ── random_image_effects.go (merged to respect the package file cap) ──

// AttachRandomImageEffectsToPlan applies the deterministic per-image runtime
// decoration to one plan. The selection lives in the overlays package (single
// owner of the EffectsPack decoration vocabulary); this is only the plan-level
// binding the runner calls when the payload asked for random_image_effects.
func AttachRandomImageEffectsToPlan(plan *capmodel.OverlayPlan) {
	if plan == nil {
		return
	}
	capoverlay.AttachRandomImageEffects(plan.PlanID, plan.Items)
}
