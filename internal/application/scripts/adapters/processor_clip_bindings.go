package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ClipBindingsProcessor assigns clips from ClipEvidence to scenes.
// Each clip maps to exactly one scene, in the canonical order from
// plan.ClipEvidence.ClipIDs (preserving the resolver's order). Extra
// scenes beyond the clip count receive no clip binding — this
// surfaces LLM output mismatches instead of silently cycling clips.
type ClipBindingsProcessor struct {
	log *zap.Logger
}

func NewClipBindingsProcessor(log *zap.Logger) *ClipBindingsProcessor {
	return &ClipBindingsProcessor{log: log}
}

func (p *ClipBindingsProcessor) Name() string { return "clip_bindings" }

// Policy classifies clip_bindings as ProcessorBestEffort: a nil or
// empty ClipEvidence is a no-op (Process returns early with empty
// result) rather than a hard fail. Matches the in-body comment that
// the processor "is a no-op when plan.ClipEvidence is nil/empty".
// Pair with `clip_bindings` in defaultPolicyByName so the
// LookupPolicy override path stays consistent.
func (p *ClipBindingsProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *ClipBindingsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.ClipIDs) == 0 {
		return &PostProcessResult{}, nil
	}

	scenes := input.SpecScene.Scenes
	heuristicEngaged := false

	// FASE 3 (June 2026) — prose-fallback heuristic.
	//
	// When the resolution cycle landed on prose without any
	// SpecScene.scenes (small local models such as gemma2:2b /
	// gemma4:e4b commonly emit a single intro paragraph and ignore
	// the structured-output schema), the caller still passed
	// clip_ids. Without scenes, the binder has nothing to attach
	// clips to, so it returns empty and the job surfaces a
	// "clip_bindings: empty output" warning.
	//
	// Synthesise N scenes from input.Text so the binder can attach
	// clips 1:1. Heuristic only — preserves no-op semantics when
	// scenes pre-exist or text is empty. The downstream
	// persistence/document processors receive their own copy of the
	// ProcessInput envelope by value, so they keep seeing the
	// original (empty) SpecScene.Scenes; clip_bindings' local
	// synthesis is enough to silence the empty-output warning.
	if len(scenes) == 0 {
		if input.Text == "" {
			return &PostProcessResult{}, nil
		}
		n := len(plan.ClipEvidence.ClipIDs)
		if plan.NumClips > 0 && plan.NumClips < n {
			n = plan.NumClips
		}
		synthesized := buildScenesFromProse(input.Text, n)
		if len(synthesized) == 0 {
			return &PostProcessResult{}, nil
		}
		scenes = synthesized
		heuristicEngaged = true
		if p.log != nil {
			p.log.Info("clip_bindings: prose-fallback heuristic engaged",
				zap.Int("synthesized", len(synthesized)),
				zap.Int("clips", len(plan.ClipEvidence.ClipIDs)))
		}
	}

	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	// P0 #2 (June 2026): use the canonical ordered list from
	// plan.ClipEvidence.ClipIDs instead of iterating the
	// DriveLinks map + sort.Strings. The resolver's order is
	// preserved; clips bind to scenes 1:1 in arrival order.
	clipIDs := plan.ClipEvidence.ClipIDs

	// Respect NumClips limit.
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}

	// One clip per scene — no modulo cycling. Extra scenes beyond
	// the clip count get no binding. This surfaces LLM output
	// mismatches (more scenes than clips) instead of silently
	// reusing clips.
	bindCount := len(clipIDs)
	if bindCount > len(scenes) {
		bindCount = len(scenes)
	}

	for i := 0; i < bindCount; i++ {
		clipID := clipIDs[i]
		driveLink := plan.ClipEvidence.DriveLinks[clipID]

		scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
			ClipID:    clipID,
			DriveLink: driveLink,
		}
	}

	// P0 #2: extra scenes beyond the clip count get no binding.
	// Explicitly nil out any LLM-assigned stale binding so the
	// mismatch is visible.
	for i := bindCount; i < len(scenes); i++ {
		scenes[i].Bindings.Clip = nil
	}

	if p.log != nil {
		p.log.Info("clip_bindings: assigned clips to scenes",
			zap.Int("scenes", len(scenes)),
			zap.Int("clips_bound", bindCount),
			zap.Int("clips_available", len(plan.ClipEvidence.ClipIDs)),
			zap.Int("scenes_unbound", len(scenes)-bindCount),
			zap.Strings("clip_ids", clipIDs[:bindCount]))
	}

	// FASE 3 (June 2026): when the prose-fallback heuristic
	// synthesised scenes, surface them on PostProcessResult. Without
	// this the registry's IsEmpty() check would still flag the
	// binder as "returned empty output" — even though it did the
	// synthesis + 1:1 binding below. SynthesizedScenes counts as
	// observable work in PostProcessResult.IsEmpty() so the empty
	// warning does not fire.
	result := &PostProcessResult{}
	if heuristicEngaged {
		result.SynthesizedScenes = scenes
		result.Warnings = []string{
			fmt.Sprintf("clip_bindings: prose-fallback synthesised %d scenes; bound %d/%d clips",
				len(scenes), bindCount, len(clipIDs)),
		}
	}
	return result, nil
}

// buildScenesFromProse is the prose-fallback helper for
// ClipBindingsProcessor. It heuristically partitions the supplied
// prose into N scenes using word-level balanced distribution
// (strings.Fields → contiguous slices of size ceil(len(words)/N)).
//
// The helper is unexported and lives next to its only caller; tests
// in processor_clip_bindings_test.go cover its invariants directly.
//
// Kind assignment (matches the canonical scene-kind taxonomy in
// internal/domain/script/model_output.go):
//   - n < 3  → every scene is SceneClip (no intro/outro bleed — the
//     "every requested clip is a real narrative beat" intent wins
//     over the "frame with intro/outro" heuristic).
//   - n == 0 → returns nil (caller preserves no-op).
//   - n >= 3 → scene[0]=SceneIntro, scene[n-1]=SceneOutro, the
//     middle scenes are SceneClip.
//
// Chunks that land empty after the word distribution (rare, only
// when len(words) < n) get the placeholder "Scene {i+1}" so the
// resulting SpecScene.Validate() does not fail on the "text is
// required" rule (model_output.go:265).
func buildScenesFromProse(text string, n int) []scriptpkg.SpecScene {
	if n <= 0 {
		return nil
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	words := strings.Fields(trimmed)
	if len(words) == 0 {
		return nil
	}

	perChunk := (len(words) + n - 1) / n // ceil division
	scenes := make([]scriptpkg.SpecScene, n)
	for i := 0; i < n; i++ {
		start := i * perChunk
		end := start + perChunk
		if end > len(words) {
			end = len(words)
		}
		var chunk string
		if start < len(words) {
			chunk = strings.Join(words[start:end], " ")
		}
		if chunk == "" {
			chunk = fmt.Sprintf("Scene %d", i+1)
		}
		scenes[i] = scriptpkg.SpecScene{
			ID:    fmt.Sprintf("scene-%d", i),
			Index: i,
			Text:  chunk,
			Kind:  kindForPosition(i, n),
		}
	}
	return scenes
}

// kindForPosition encodes the position-to-kind policy used by the
// prose-fallback path in buildScenesFromProse. See model_output.go
// for the canonical SceneKind constants (SceneIntro / SceneOutro /
// SceneClip).
func kindForPosition(i, n int) scriptpkg.SceneKind {
	if n < 3 {
		return scriptpkg.SceneClip
	}
	if i == 0 {
		return scriptpkg.SceneIntro
	}
	if i == n-1 {
		return scriptpkg.SceneOutro
	}
	return scriptpkg.SceneClip
}
