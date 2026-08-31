package adapters

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ClipBindingsProcessor assigns clips from ClipEvidence to scenes.
// Each clip maps to exactly one scene, in the canonical order from
// plan.ClipEvidence.AcceptedClipIDs (Issue #2, June 2026: renamed
// from ClipIDs; preserving the resolver's order). Extra scenes
// beyond the clip count receive no clip binding — this surfaces
// LLM output mismatches instead of silently cycling clips.
//
// Phase 2 of postprocessor-unification (2026-07-08): the processor
// is a thin orchestrator that delegates to
// scene.SceneAssetBinder.BindClips. The binder knows only scene_id,
// requirements, candidate assets, and binding policy.
//
// P0 (July 2026): when the engine emits plain text and no scenes,
// the processor synthesises scenes from clip evidence using the
// canonical ScenePlanner.PlanFromClipEvidence before binding clips.
// This closes the live source.type=clips path that previously
// failed with CLIP_NATIVE_PLAN_UNAVAILABLE.
//
// The constructor signature is STABLE for godlike/07
// minimum-blast-radius — wire_script_postprocess.go does not need
// to change; the binder is constructed inline.
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

func (p *ClipBindingsProcessor) Name() ProcessorName { return ProcessorClipBindings }

// Policy classifies clip_bindings as ProcessorBestEffort: a nil or
// empty ClipEvidence is a no-op (Process returns early with empty
// result) rather than a hard fail. Matches the in-body comment that
// the processor "is a no-op when plan.ClipEvidence is nil/empty".
// Pair with `clip_bindings` in defaultPolicyByName so the
// LookupPolicy override path stays consistent.
func (p *ClipBindingsProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process delegates to scene.SceneAssetBinder.BindClips and applies
// the returned bindings to the input scenes. The processor owns the
// translation from SpecScene to the binder's canonical request shape
// (scene_id + requirements + candidates + policy) and back again.
func (p *ClipBindingsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	_ = ctx
	if plan != nil && plan.MediaMode == scriptpkg.MediaModeClipOnly {
		for i := range input.SpecScene.Scenes {
			if input.SpecScene.Scenes[i].AllowsMediaReplacement() {
				input.SpecScene.Scenes[i].Bindings.Stock = nil
			}
		}
	}

	clipIDs, driveLinks := acceptedClipIDs(plan)

	// P0 (July 2026): if the engine produced no scenes (plain-text
	// output mode), build them deterministically.
	hasSynthesized := false
	synthesized := false
	if shouldMaterializeNarrativeScenes(input, plan) {
		var scenes []scriptpkg.SpecScene
		narrativeText := strings.TrimSpace(input.Text)
		if narrativeText == "" && len(input.SpecScene.Scenes) == 1 {
			narrativeText = strings.TrimSpace(input.SpecScene.Scenes[0].Text)
		}
		if narrativeText != "" {
			scenePlan := p.planner.Plan(scene.NarrativeDraft{Text: narrativeText}, plan)
			scenes = scenePlan.Scenes
			hasSynthesized = true
			if scenePlan.Source == scene.ScenePlanSourceClipEvidence {
				synthesized = true
			}
		} else {
			scenes = p.planner.PlanFromClipEvidence(plan)
			hasSynthesized = true
			synthesized = true
		}

		if len(scenes) > 0 {
			input.SpecScene = scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes:  scenes,
			}
		}
	}

	scenes := input.SpecScene.Scenes
	explicitSegments := plan != nil && len(plan.Segments) > 0
	if explicitSegments {
		// A clip-primary search may return one prose scene even though the
		// resolver has created one segment per accepted clip. Partition that
		// canonical prose before slot normalization; otherwise missing slots
		// fall back to clip filenames/topics and become non-narrative scenes.
		var clipNativeNarrative []scriptpkg.SpecScene
		if scene.RequiresClipNativePlan(plan) && len(scenes) == 1 &&
			len(plan.Segments) > 1 && strings.TrimSpace(scenes[0].Text) != "" {
			clipNativeNarrative = p.planner.Synthesizer().FromProse(
				scenes[0].Text,
				len(plan.Segments),
			)
		}
		// Explicit segment payloads are authoritative even when no clip
		// evidence is present. Keep narrative slots stable for clip-only,
		// stock-only, and text-only matrix cases alike.
		input.SpecScene.Scenes = normalizeScenesForExplicitSegments(scenes, plan.Segments)
		if len(clipNativeNarrative) == len(input.SpecScene.Scenes) {
			for i := range input.SpecScene.Scenes {
				if strings.TrimSpace(clipNativeNarrative[i].Text) != "" {
					input.SpecScene.Scenes[i].Text = clipNativeNarrative[i].Text
				}
			}
		}
		applyExplicitSegmentClipBindings(input.SpecScene.Scenes, plan)
		scenes = input.SpecScene.Scenes
	}
	// A text generation may legitimately have no accepted media clips.
	// Preserve the narrative scene structure for persistence and downstream
	// planning even in that case; clip binding is optional enrichment.
	if len(clipIDs) == 0 {
		// Preserve the historical no-op contract when the caller did not
		// provide explicit segment slots. Explicit plans, however, still
		// need their normalized scenes persisted for stock-only/text-only
		// generation even when there are no clips to bind.
		if plan == nil || len(plan.Segments) == 0 {
			return &PostProcessResult{}, nil
		}
		if len(scenes) == 0 {
			return &PostProcessResult{}, nil
		}
		return &PostProcessResult{
			Changed:          true,
			UpdatedSpecScene: input.SpecScene,
		}, nil
	}

	// P0 (July 2026): when scenes were synthesized from clip evidence,
	// they already carry fully populated ClipBinding structs. Running
	// the binder would overwrite them with a minimal binding, losing
	// ClipTitle, StartMs, EndMs and DurationMs. Preserve the planner's
	// detailed bindings by skipping the binder for the synthesized path.
	if !synthesized && !explicitSegments {
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

		res := p.binder.BindClips(reqs)
		if !res.Changed {
			return &PostProcessResult{}, nil
		}

		// Apply bindings back to the original scenes. Scenes beyond the
		// clip count get their stale Bindings.Clip explicitly nil-ed
		// (P0 #2 invariant: surface LLM mismatches instead of silently
		// preserving stale bindings).
		clipCount := len(candidates)
		for i := range scenes {
			if !scenes[i].AllowsMediaReplacement() {
				continue
			}
			if binding, ok := res.Bindings[scenes[i].ID]; ok {
				// The binder owns clip selection, while subtitle provenance
				// comes from the resolved clip evidence. Preserve that
				// association when the model emitted its own scene list.
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
				// Safety: a scene within the clip range should always
				// have a binding; if it does not, leave it untouched.
				continue
			} else {
				scenes[i].Bindings.Clip = nil
				scenes[i].Bindings.Clips = nil
			}
		}
	}
	if plan.MediaMode == scriptpkg.MediaModeClipOnly {
		for i := range scenes {
			if scenes[i].AllowsMediaReplacement() && scenes[i].Bindings.Clip != nil {
				scenes[i].Kind = scriptpkg.SceneClip
			}
		}
	}
	// The clip planner may synthesize a detailed binding before this
	// processor runs.  That binding is authoritative for timing, but the
	// public/document surface must still carry the canonical Drive URL from
	// ClipEvidence.  Complete it for both synthesized and binder-produced
	// scenes so the narrative text can never detach from its source clip.
	enrichClipBindings(scenes, plan, driveLinks)

	result := &PostProcessResult{Changed: true}
	if explicitSegments {
		// Explicit segment normalization and its multi-clip bindings are
		// canonical output, not transient processor state. Return them so
		// the registry writes the exact N-scene surface to downstream
		// processors and persistence.
		result.UpdatedSpecScene = input.SpecScene
	}
	if hasSynthesized {
		result.SynthesizedScenes = input.SpecScene.Scenes
	}
	return result, nil
}

// applyExplicitSegmentClipBindings projects the caller-owned clip membership
// onto the already-normalized scene slots. It deliberately does not use the
// global accepted-clip order: one segment may own zero, one, or many clips,
// and that per-segment order is the editorial contract.
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
					// Missing or excluded IDs remain unbound even when they
					// still appear in the caller's segment metadata.
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
			if binding.ClipTitle == "" {
				if evidence != nil {
					binding.ClipTitle = evidence.ClipNames[clipID]
				}
			}
			if binding.DriveLink == "" {
				if evidence != nil {
					binding.DriveLink = evidence.DriveLinks[clipID]
				}
			}
			// A clip ID is an internal asset identity, not necessarily a
			// Google Drive file ID. Leave the location absent when the
			// canonical evidence/registry did not provide one; inventing a
			// plausible URL would publish an unverifiable binding.
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

func shouldMaterializeNarrativeScenes(input ProcessInput, plan *scriptpkg.ResolvedGenerationPlan) bool {
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

// acceptedClipIDs returns the canonical ordered list of accepted clip
// IDs and their drive links from the plan. It respects NumClips when
// set.
func acceptedClipIDs(plan *scriptpkg.ResolvedGenerationPlan) ([]string, map[string]string) {
	if plan == nil || plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return nil, nil
	}

	clipIDs := plan.ClipEvidence.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}

	driveLinks := plan.ClipEvidence.DriveLinks
	return clipIDs, driveLinks
}
