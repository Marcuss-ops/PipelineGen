package adapters

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

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
			input.SpecScene.Scenes[i].Bindings.Stock = nil
		}
	}

	clipIDs, driveLinks := acceptedClipIDs(plan)

	// P0 (July 2026): if the engine produced no scenes (plain-text
	// output mode), build them deterministically.
	hasSynthesized := false
	synthesized := false
	if len(input.SpecScene.Scenes) == 0 {
		var scenes []scriptpkg.SpecScene
		if len(input.Text) > 0 {
			scenePlan := p.planner.Plan(scene.NarrativeDraft{Text: input.Text}, plan)
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
	if plan != nil && len(plan.Segments) > 0 {
		// Explicit segment payloads are authoritative even when no clip
		// evidence is present. Keep narrative slots stable for clip-only,
		// stock-only, and text-only matrix cases alike.
		input.SpecScene.Scenes = normalizeScenesForExplicitSegments(scenes, plan.Segments)
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
	if !synthesized {
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
				scenes[i].Bindings.Clip = binding
				if plan.MediaMode == scriptpkg.MediaModeClipOnly {
					scenes[i].Kind = scriptpkg.SceneClip
				}
			} else if i < clipCount {
				// Safety: a scene within the clip range should always
				// have a binding; if it does not, leave it untouched.
				continue
			} else {
				scenes[i].Bindings.Clip = nil
			}
		}
	}
	if plan.MediaMode == scriptpkg.MediaModeClipOnly {
		for i := range scenes {
			if scenes[i].Bindings.Clip != nil {
				scenes[i].Kind = scriptpkg.SceneClip
			}
		}
	}

	result := &PostProcessResult{Changed: true}
	if hasSynthesized {
		result.SynthesizedScenes = input.SpecScene.Scenes
	}
	return result, nil
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
