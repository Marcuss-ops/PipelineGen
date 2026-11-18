package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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

// ── overlay_image_sfx.go (merged to respect the package file cap) ──
// overlay_image_sfx.go binds the payload's explicit
// per-image cues (overlay_images[].sound_effect) to the image overlay items the
// planner produced for that scene.
//
// Why it exists: audio.random_sfx_on_image_overlay only ever attaches a
// deterministic RANDOM cue, and audio.sound_effects is scene/anchor-scoped, so
// an operator had no way to say "this photo gets this whoosh". The item-level
// cue is the producer-selected contract RenderingGen already validates and
// mixes, so this file is the projection that fills it from the payload.
//
// Matching is positional and deterministic: pins are declaration-ordered, and
// within a scene they attach to the image items in start-time order. A cue the
// plan cannot host fails the run instead of being silently dropped.

// attachImageAssetSFXForResult applies the explicit pins to the source plan and
// every localized plan. Translation never changes the stills, so the same cue
// belongs to the same scene in each language.
func attachImageAssetSFXForResult(ctx context.Context, source AudioAssetSource, result *GenerateResult, pins []scriptpkg.OverlayImageAsset) error {
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
	return attachImageAssetSFX(ctx, source, plans, pins)
}

// attachImageAssetSFX resolves each pinned cue through the same audio asset
// resolver audio.sound_effects uses, then seals it onto the matching image item.
func attachImageAssetSFX(ctx context.Context, source AudioAssetSource, plans []*capmodel.OverlayPlan, pins []scriptpkg.OverlayImageAsset) error {
	sceneCues, sceneOrder := groupImageSFXByScene(pins)
	if len(sceneOrder) == 0 {
		return nil
	}
	if source == nil {
		return fmt.Errorf("overlay_images.sound_effect requires the audio asset resolver, which is not wired")
	}
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		imageItemsByScene := imageOverlayItemsByScene(plan)
		changed := false
		for _, scene := range sceneOrder {
			cues := sceneCues[scene]
			candidates := imageItemsByScene[scene]
			if len(cues) > len(candidates) {
				return fmt.Errorf("overlay_images.sound_effect: scene %q declares %d cues but the overlay plan has %d image items", scene, len(cues), len(candidates))
			}
			for cueIndex, cue := range cues {
				item := candidates[cueIndex]
				startOffsetMS, visibleEndMS := imageSFXWindow(*item)
				durationMS := randomOverlaySFXDurationMS
				if cue.DurationMS != nil {
					durationMS = *cue.DurationMS
				}
				if visibleMS := visibleEndMS - startOffsetMS; durationMS > visibleMS {
					durationMS = visibleMS
				}
				if durationMS <= 0 {
					return fmt.Errorf("overlay_images.sound_effect: item %q has no positive visible image window for its cue", item.ID)
				}
				resolved, err := source.ResolveAudioAsset(ctx, capabilityaudio.CanonicalAssetID(cue.AssetID))
				if err != nil {
					return fmt.Errorf("overlay_images.sound_effect: resolve audio asset %q: %w", cue.AssetID, err)
				}
				path := strings.TrimSpace(resolved.Path)
				if path == "" {
					return fmt.Errorf("overlay_images.sound_effect: audio asset %q resolved to an empty path", cue.AssetID)
				}
				absolutePath, err := filepath.Abs(path)
				if err != nil {
					return fmt.Errorf("overlay_images.sound_effect: resolve cue path for %q: %w", item.ID, err)
				}
				contents, err := os.ReadFile(absolutePath)
				if err != nil {
					return fmt.Errorf("overlay_images.sound_effect: read cue %q: %w", absolutePath, err)
				}
				if resolved.DurationUS > 0 && durationMS*1000 > resolved.DurationUS {
					durationMS = resolved.DurationUS / 1000
					if durationMS <= 0 {
						return fmt.Errorf("overlay_images.sound_effect: audio asset %q is shorter than one millisecond", cue.AssetID)
					}
				}
				sum := digest.SHA256Sum(contents)
				sha := fmt.Sprintf("%x", sum)
				gainDB := randomOverlaySFXGainDB
				if cue.GainDB != nil {
					gainDB = *cue.GainDB
				}
				assetID := strings.TrimSpace(resolved.AssetID)
				if assetID == "" {
					assetID = strings.TrimSpace(cue.AssetID)
				}
				item.SoundEffect = &capmodel.OverlaySoundEffect{
					AssetRef: capmodel.OverlayAssetRef{
						AssetID: assetID, URL: "assets/semantic/overlay-sfx-" + sha + ".m4a",
						LocalPath: absolutePath, SHA256: sha, MediaType: "audio/mp4",
					},
					StartOffsetMS: startOffsetMS,
					DurationMS:    durationMS,
					GainDB:        gainDB,
				}
				item.RenderKey = ""
				changed = true
			}
		}
		if !changed {
			continue
		}
		plan.Fingerprint = ""
		if err := plan.Validate(); err != nil {
			return fmt.Errorf("seal overlay image SFX plan %q: %w", plan.PlanID, err)
		}
	}
	return nil
}

// groupImageSFXByScene keeps declaration order for scenes while grouping the
// cue entries of the same scene. Scene keys are compared case-insensitively,
// exactly like the planner's scene identity.
func groupImageSFXByScene(pins []scriptpkg.OverlayImageAsset) (map[string][]scriptpkg.OverlayImageSoundEffect, []string) {
	cues := make(map[string][]scriptpkg.OverlayImageSoundEffect)
	order := make([]string, 0)
	seen := make(map[string]struct{}, len(pins))
	for _, pin := range pins {
		if pin.SoundEffect == nil {
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
		cues[scene] = append(cues[scene], *pin.SoundEffect)
	}
	return cues, order
}

// imageOverlayItemsByScene collects the image items still eligible for a cue,
// ordered by start time (then id) so the positional match is deterministic.
// Items that already carry a cue are skipped, which keeps a resumed run
// idempotent.
func imageOverlayItemsByScene(plan *capmodel.OverlayPlan) map[string][]*capmodel.OverlayItem {
	return imageOverlayItemsBySceneWhere(plan, func(item *capmodel.OverlayItem) bool {
		return item.SoundEffect == nil
	})
}

// imageOverlayItemsBySceneWhere is the shared positional matcher: per scene,
// the image items that satisfy eligible, ordered by start time (then id).
func imageOverlayItemsBySceneWhere(plan *capmodel.OverlayPlan, eligible func(*capmodel.OverlayItem) bool) map[string][]*capmodel.OverlayItem {
	out := make(map[string][]*capmodel.OverlayItem)
	for index := range plan.Items {
		item := &plan.Items[index]
		if !eligible(item) || !capoverlay.IsImageOverlayItem(*item) {
			continue
		}
		scene := strings.ToLower(strings.TrimSpace(item.SceneID))
		out[scene] = append(out[scene], item)
	}
	for scene := range out {
		items := out[scene]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].StartMs != items[j].StartMs {
				return items[i].StartMs < items[j].StartMs
			}
			return items[i].ID < items[j].ID
		})
	}
	return out
}

// imageSFXWindow mirrors the visible window the renderer and the plan seal use:
// a single-layer image is visible for the whole item, a composite for its
// first visible layer (earliest start, widest on a tie).
func imageSFXWindow(item capmodel.OverlayItem) (startOffsetMS, visibleEndMS int64) {
	visibleEndMS = item.EndMs - item.StartMs
	if len(item.ImageLayers) == 0 {
		return 0, visibleEndMS
	}
	first := item.ImageLayers[0]
	for _, layer := range item.ImageLayers[1:] {
		if layer.StartMS < first.StartMS || layer.StartMS == first.StartMS && layer.EndMS > first.EndMS {
			first = layer
		}
	}
	if first.EndMS < visibleEndMS {
		visibleEndMS = first.EndMS
	}
	return first.StartMS, visibleEndMS
}

// applyImageAssetStylesForResult applies the per-pin styles to the source plan
// and every localized plan: translation never changes the stills, so a pin's
// look belongs to the same scene in each language.
func applyImageAssetStylesForResult(result *GenerateResult, pins []scriptpkg.OverlayImageAsset, runStyle *scriptpkg.OverlayImageStyleSpec) error {
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
	return applyImageAssetStyles(plans, pins, runStyle)
}

func applyImageAssetStyles(plans []*capmodel.OverlayPlan, pins []scriptpkg.OverlayImageAsset, runStyle *scriptpkg.OverlayImageStyleSpec) error {
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
		imageItemsByScene := imageOverlayItemsBySceneWhere(plan, func(*capmodel.OverlayItem) bool { return true })
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

// attachRandomImageEffectsToPlan applies the deterministic per-image runtime
// decoration to one plan. The selection lives in the overlays package (single
// owner of the EffectsPack decoration vocabulary); this is only the plan-level
// binding the runner calls when the payload asked for random_image_effects.
func attachRandomImageEffectsToPlan(plan *capmodel.OverlayPlan) {
	if plan == nil {
		return
	}
	capoverlay.AttachRandomImageEffects(plan.PlanID, plan.Items)
}
