package voiceover

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ClipBindingsProcessor assigns clips from ClipEvidence to scenes. Each clip
// maps to exactly one scene, in the canonical order from
// plan.ClipEvidence.AcceptedClipIDs. Extra scenes beyond the clip count receive
// no clip binding — this surfaces LLM output mismatches instead of silently
// cycling clips.
//
// The processor is a thin orchestrator over scene.SceneAssetBinder.BindClips.
// When the engine emits plain text and no scenes, it first synthesises scenes
// from clip evidence via the canonical ScenePlanner.
type ClipBindingsProcessor struct {
	binder  *scene.SceneAssetBinder
	planner *scene.ScenePlanner
	log     *zap.Logger
}

func NewClipBindingsProcessor(log *zap.Logger) *ClipBindingsProcessor {
	return &ClipBindingsProcessor{
		log:     log,
		binder:  scene.NewSceneAssetBinder(log),
		planner: scene.NewScenePlanner(log),
	}
}

func (p *ClipBindingsProcessor) Name() adapters.ProcessorName {
	return adapters.ProcessorClipBindings
}

// Policy classifies clip_bindings as BestEffort: nil/empty ClipEvidence is a
// no-op rather than a hard fail.
func (p *ClipBindingsProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return adapters.ProcessorBestEffort
}

// Process validates the media mode, materializes or normalizes scenes, binds
// accepted clips and attaches the resulting bindings to the scene surface.
func (p *ClipBindingsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input adapters.ProcessInput,
) (*adapters.PostProcessResult, error) {
	_ = ctx
	input = clearOppositeMediaBindings(plan, input)
	clipIDs, driveLinks := acceptedClipIDs(plan)

	input, hasSynthesized, synthesized := p.materializeNarrativeScenes(input, plan)
	scenes := input.SpecScene.Scenes
	explicitSegments := plan != nil && len(plan.Segments) > 0
	if explicitSegments {
		input, scenes = p.normalizeExplicitSegments(input, plan)
	}

	if len(clipIDs) == 0 {
		// Preserve the historical no-op contract when the caller did not
		// provide explicit segment slots. Explicit plans still need their
		// normalized scenes persisted for stock-only/text-only generation.
		if plan == nil || len(plan.Segments) == 0 {
			return &adapters.PostProcessResult{}, nil
		}
		if len(scenes) == 0 {
			return &adapters.PostProcessResult{}, nil
		}
		return &adapters.PostProcessResult{
			Changed:          true,
			UpdatedSpecScene: input.SpecScene,
		}, nil
	}

	// When scenes were synthesized from clip evidence they already carry fully
	// populated ClipBinding structs. Running the binder would overwrite them
	// with a minimal binding, losing ClipTitle, StartMs, EndMs and DurationMs.
	if !synthesized && !explicitSegments {
		res := p.bindClipCandidates(plan, scenes, clipIDs, driveLinks)
		if !res.Changed {
			return &adapters.PostProcessResult{}, nil
		}
		applyBinderBindings(scenes, res, plan, len(clipIDs))
	}
	applyClipOnlyKinds(plan, scenes)
	// The clip planner may synthesize a detailed binding before this processor
	// runs. That binding is authoritative for timing, but the public/document
	// surface must still carry the canonical Drive URL from ClipEvidence.
	enrichClipBindings(scenes, plan, driveLinks)

	result := &adapters.PostProcessResult{Changed: true}
	if explicitSegments {
		// Explicit segment normalization and its multi-clip bindings are
		// canonical output, not transient processor state.
		result.UpdatedSpecScene = input.SpecScene
	}
	if hasSynthesized {
		result.SynthesizedScenes = input.SpecScene.Scenes
	}
	return result, nil
}

func clearOppositeMediaBindings(plan *scriptpkg.ResolvedGenerationPlan, input adapters.ProcessInput) adapters.ProcessInput {
	if plan == nil || plan.MediaMode != scriptpkg.MediaModeClipOnly {
		return input
	}
	for i := range input.SpecScene.Scenes {
		if input.SpecScene.Scenes[i].AllowsMediaReplacement() {
			input.SpecScene.Scenes[i].Bindings.Stock = nil
		}
	}
	return input
}

func (p *ClipBindingsProcessor) materializeNarrativeScenes(input adapters.ProcessInput, plan *scriptpkg.ResolvedGenerationPlan) (adapters.ProcessInput, bool, bool) {
	if !shouldMaterializeNarrativeScenes(input, plan) {
		return input, false, false
	}
	var scenes []scriptpkg.SpecScene
	narrativeText := strings.TrimSpace(input.Text)
	if narrativeText == "" && len(input.SpecScene.Scenes) == 1 {
		narrativeText = strings.TrimSpace(input.SpecScene.Scenes[0].Text)
	}
	synthesized := false
	if narrativeText != "" {
		scenePlan := p.planner.Plan(scene.NarrativeDraft{Text: narrativeText}, plan)
		scenes = scenePlan.Scenes
		synthesized = scenePlan.Source == scene.ScenePlanSourceClipEvidence
	} else {
		scenes = p.planner.PlanFromClipEvidence(plan)
		synthesized = true
	}
	if len(scenes) > 0 {
		input.SpecScene = scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		}
	}
	return input, true, synthesized
}

func (p *ClipBindingsProcessor) normalizeExplicitSegments(input adapters.ProcessInput, plan *scriptpkg.ResolvedGenerationPlan) (adapters.ProcessInput, []scriptpkg.SpecScene) {
	scenes := input.SpecScene.Scenes
	// A clip-primary search may return one prose scene even though the
	// resolver has created one segment per accepted clip. Partition that
	// canonical prose before slot normalization.
	var clipNativeNarrative []scriptpkg.SpecScene
	if scene.RequiresClipNativePlan(plan) && len(scenes) == 1 &&
		len(plan.Segments) > 1 && strings.TrimSpace(scenes[0].Text) != "" {
		clipNativeNarrative = p.planner.Synthesizer().FromProse(
			scenes[0].Text,
			len(plan.Segments),
		)
	}
	input.SpecScene.Scenes = adapters.NormalizeScenesForExplicitSegments(scenes, plan.Segments)
	if len(clipNativeNarrative) == len(input.SpecScene.Scenes) {
		for i := range input.SpecScene.Scenes {
			if strings.TrimSpace(clipNativeNarrative[i].Text) != "" {
				input.SpecScene.Scenes[i].Text = clipNativeNarrative[i].Text
			}
		}
	}
	applyExplicitSegmentClipBindings(input.SpecScene.Scenes, plan)
	return input, input.SpecScene.Scenes
}

func (p *ClipBindingsProcessor) bindClipCandidates(plan *scriptpkg.ResolvedGenerationPlan, scenes []scriptpkg.SpecScene, clipIDs []string, driveLinks map[string]string) scene.BindClipsResult {
	reqs := make([]scene.ClipBindingRequest, 0, len(scenes))
	for _, s := range scenes {
		reqs = append(reqs, scene.ClipBindingRequest{
			SceneID:      s.ID,
			Requirements: scene.AssetRequirements{},
			Policy:       scene.ClipBindingPolicy{},
		})
	}

	// Build one candidate per accepted clip in canonical order.
	candidates := make([]scene.ClipCandidate, 0, len(clipIDs))
	for _, id := range clipIDs {
		var startMs, endMs int64
		if plan.ClipEvidence != nil {
			if detail, ok := plan.ClipEvidence.ClipDetails[id]; ok {
				startMs, endMs = detail.StartMs, detail.EndMs
			}
		}
		candidates = append(candidates, scene.ClipCandidate{
			ClipID:    id,
			DriveLink: driveLinks[id],
			StartMs:   startMs,
			EndMs:     endMs,
		})
	}

	// Distribute candidates to requests 1:1 in order.
	for i := range reqs {
		if i < len(candidates) {
			reqs[i].Candidates = []scene.ClipCandidate{candidates[i]}
		}
	}
	return p.binder.BindClips(reqs)
}

func applyBinderBindings(scenes []scriptpkg.SpecScene, res scene.BindClipsResult, plan *scriptpkg.ResolvedGenerationPlan, clipCount int) {
	for i := range scenes {
		if !scenes[i].AllowsMediaReplacement() {
			continue
		}
		if binding, ok := res.Bindings[scenes[i].ID]; ok {
			// The binder owns clip selection, while subtitle provenance comes
			// from the resolved clip evidence.
			if plan.ClipEvidence != nil && binding != nil {
				if detail, exists := plan.ClipEvidence.ClipDetails[binding.ClipID]; exists {
					binding.SubtitleLink = detail.SubtitleLink
					binding.SubtitleFileID = detail.SubtitleFileID
				}
			}
			scenes[i].Bindings.Clips = []scriptpkg.ClipBinding{*binding}
			scenes[i].Bindings.Clip = &scenes[i].Bindings.Clips[0]
			if plan.MediaMode == scriptpkg.MediaModeClipOnly {
				scenes[i].Kind = scriptpkg.SceneClip
			}
		} else if i < clipCount {
			// Safety: a scene within the clip range should always have a
			// binding; if it does not, leave it untouched.
			continue
		} else {
			scenes[i].Bindings.Clip = nil
			scenes[i].Bindings.Clips = nil
		}
	}
}

func applyClipOnlyKinds(plan *scriptpkg.ResolvedGenerationPlan, scenes []scriptpkg.SpecScene) {
	if plan == nil || plan.MediaMode != scriptpkg.MediaModeClipOnly {
		return
	}
	for i := range scenes {
		if scenes[i].AllowsMediaReplacement() && scenes[i].Bindings.Clip != nil {
			scenes[i].Kind = scriptpkg.SceneClip
		}
	}
}

// applyExplicitSegmentClipBindings projects the caller-owned clip membership
// onto the already-normalized scene slots. It deliberately does not use the
// global accepted-clip order: one segment may own zero, one, or many clips.
func applyExplicitSegmentClipBindings(scenes []scriptpkg.SpecScene, plan *scriptpkg.ResolvedGenerationPlan) {
	if plan == nil {
		for i := range scenes {
			scenes[i].Bindings.Clip = nil
			scenes[i].Bindings.Clips = nil
		}
		return
	}
	evidence := plan.ClipEvidence
	accepted := make(map[string]struct{})
	if evidence != nil {
		accepted = make(map[string]struct{}, len(evidence.AcceptedClipIDs))
		for _, clipID := range evidence.AcceptedClipIDs {
			accepted[strings.TrimSpace(clipID)] = struct{}{}
		}
	}
	for i := range scenes {
		if i >= len(plan.Segments) {
			break
		}
		segment := plan.Segments[i]
		bindings := make([]scriptpkg.ClipBinding, 0, len(segment.ClipIDs))
		for _, clipID := range segment.ClipIDs {
			clipID = strings.TrimSpace(clipID)
			if clipID == "" {
				continue
			}
			if evidence != nil && len(accepted) > 0 {
				if _, ok := accepted[clipID]; !ok {
					continue
				}
			}
			var detail scriptpkg.ClipDetail
			if evidence != nil {
				detail = evidence.ClipDetails[clipID]
			}
			binding := scriptpkg.ClipBinding{
				ClipID:          clipID,
				ClipTitle:       detail.Name,
				DriveLink:       detail.DriveLink,
				SubtitleLink:    detail.SubtitleLink,
				SubtitleFileID:  detail.SubtitleFileID,
				StartMs:         detail.StartMs,
				EndMs:           detail.EndMs,
				DurationMs:      scriptpkg.ClipDurationMs(detail.StartMs, detail.EndMs),
				TotalDurationMs: detail.TotalDurationMs,
			}
			if binding.ClipTitle == "" && evidence != nil {
				binding.ClipTitle = evidence.ClipNames[clipID]
			}
			if binding.DriveLink == "" && evidence != nil {
				binding.DriveLink = evidence.DriveLinks[clipID]
			}
			// A clip ID is an internal asset identity, not necessarily a Drive
			// file ID. Leave the location absent when evidence did not provide
			// one; inventing a plausible URL would publish an unverifiable
			// binding.
			bindings = append(bindings, binding)
		}
		scenes[i].Bindings.Clips = bindings
		scenes[i].Bindings.Clip = nil
		if len(bindings) > 0 {
			scenes[i].Bindings.Clip = &scenes[i].Bindings.Clips[0]
		}
		if kind := scriptpkg.SceneKind(strings.TrimSpace(segment.Kind)); kind.Valid() {
			scenes[i].Kind = kind
		}
		if segment.ID == scriptpkg.IntroHookSegmentID {
			scenes[i].Kind = scriptpkg.SceneIntro
		}
		if len(bindings) == 0 && strings.EqualFold(strings.TrimSpace(segment.Kind), "narration") {
			scenes[i].Kind = scriptpkg.SceneNarration
		}
	}
}

func enrichClipBindings(scenes []scriptpkg.SpecScene, plan *scriptpkg.ResolvedGenerationPlan, driveLinks map[string]string) {
	for i := range scenes {
		bindings := scenes[i].Bindings.Clips
		if len(bindings) == 0 && scenes[i].Bindings.Clip != nil {
			bindings = []scriptpkg.ClipBinding{*scenes[i].Bindings.Clip}
		}
		if len(bindings) == 0 {
			continue
		}
		for j := range bindings {
			binding := &bindings[j]
			clipID := strings.TrimSpace(binding.ClipID)
			if clipID == "" {
				continue
			}
			if link := strings.TrimSpace(driveLinks[clipID]); link != "" {
				binding.DriveLink = link
			}
			if plan != nil && plan.ClipEvidence != nil {
				detail, hasDetail := plan.ClipEvidence.ClipDetails[clipID]
				if hasDetail {
					if binding.SubtitleLink == "" {
						binding.SubtitleLink = detail.SubtitleLink
					}
					if binding.SubtitleFileID == "" {
						binding.SubtitleFileID = detail.SubtitleFileID
					}
					if binding.StartMs == 0 && binding.EndMs == 0 {
						binding.StartMs = detail.StartMs
						binding.EndMs = detail.EndMs
					}
				}
				if binding.ClipTitle == "" {
					binding.ClipTitle = strings.TrimSpace(plan.ClipEvidence.ClipNames[clipID])
				}
			}
		}
		scenes[i].Bindings.Clips = bindings
		scenes[i].Bindings.Clip = &scenes[i].Bindings.Clips[0]
	}
}

func shouldMaterializeNarrativeScenes(input adapters.ProcessInput, plan *scriptpkg.ResolvedGenerationPlan) bool {
	if len(input.SpecScene.Scenes) == 0 {
		return true
	}
	if len(input.SpecScene.Scenes) != 1 || plan == nil || plan.SingleScene {
		return false
	}
	text := strings.TrimSpace(input.Text)
	if text == "" {
		text = strings.TrimSpace(input.SpecScene.Scenes[0].Text)
	}
	if len(strings.Split(strings.TrimSpace(text), "\n\n")) >= 2 {
		return true
	}
	return plan.SegmentWords > 0 && len(strings.Fields(text)) > plan.SegmentWords
}

// acceptedClipIDs returns the canonical ordered list of accepted clip IDs and
// their drive links from the plan, respecting NumClips when set.
func acceptedClipIDs(plan *scriptpkg.ResolvedGenerationPlan) ([]string, map[string]string) {
	if plan == nil || plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return nil, nil
	}
	clipIDs := plan.ClipEvidence.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}
	return clipIDs, plan.ClipEvidence.DriveLinks
}
