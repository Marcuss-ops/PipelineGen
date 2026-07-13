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
// scene.SceneAssetBinder.BindClips. The prose-fallback helper,
// JSON-envelope cleanup, sentence splitter, kind-for-position
// mapping, and the 1:1 binding loop all moved to the scene
// package (godlike/06 SSOT one canonical owner per fact).
//
// The constructor signature is STABLE for godlike/07
// minimum-blast-radius — wire_script_postprocess.go does not need
// to change; the binder is constructed inline.
type ClipBindingsProcessor struct {
	binder *scene.SceneAssetBinder
	log    *zap.Logger
}

func NewClipBindingsProcessor(log *zap.Logger) *ClipBindingsProcessor {
	return &ClipBindingsProcessor{
		log:    log,
		binder: scene.NewSceneAssetBinder(log),
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

// Process delegates to scene.SceneAssetBinder.BindClips and maps
// the result back to PostProcessResult. The binder mutates
// input.SpecScene.Scenes in-place when scenes pre-exist OR returns
// the synthesized scene list via BindClipsResult.SynthesizedScenes
// when the prose-fallback heuristic engages. The processor only
// owns the adapter-envelope translation layer (per godlike/06
// SSOT — adapters cannot import usecase + scene package types
// freely because of cycle risk; the postprocessor layer is the
// canonical seam).
func (p *ClipBindingsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	res := p.binder.BindClips(input.SpecScene.Scenes, input.Text, plan)
	if !res.Changed {
		return &PostProcessResult{}, nil
	}

	// FASE 3 (June 2026) preserved verbatim: when the prose-fallback
	// heuristic synthesised scenes, the registry's IsEmpty() gate at
	// postprocessor_document.go would otherwise flag the binder as
	// "returned empty output". SynthesizedScenes counts as
	// observable work so the empty warning does not fire.
	//
	// P1 #10 (June 2026) preserved verbatim: when scenes pre-existed
	// (heuristic NOT engaged), the binder mutated every scene's
	// Bindings.Clip field — Changed=true prevents the false
	// "empty-output" warning for the normal model-output path too.
	result := &PostProcessResult{
		Changed:           true,
		SynthesizedScenes: res.SynthesizedScenes,
		Warnings:          res.Warnings,
	}
	if len(result.SynthesizedScenes) > 0 && len(input.SpecScene.Scenes) > 0 {
		for i := range result.SynthesizedScenes {
			if i >= len(input.SpecScene.Scenes) {
				break
			}
			result.SynthesizedScenes[i].Bindings = input.SpecScene.Scenes[i].Bindings
		}
	}
	// When the synthesized list is non-nil, mutate the input
	// envelope so the downstream document/persistence processors
	// observe the synthesized scene list (matches the
	// pre-Phase-2 in-place mutation in processor_clip_bindings.go).
	if len(res.SynthesizedScenes) > 0 {
		input.SpecScene.Scenes = res.SynthesizedScenes
		_ = ctx // ambient ctx unused in the binder delegation path
	}
	return result, nil
}
