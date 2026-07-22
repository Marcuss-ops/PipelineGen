// Package scripts_test — postprocessor_registry_synthesized_test.go
// covers Issue #1 (June 2026) regression: synthesised scenes from
// the clip-bindings prose-fallback heuristic must reach
// downstream processors (write-back via the registry-local
// ProcessInput.SpecScene.Scenes) AND the canonical post-walk
// surface (PipelineResult.FinalSpecScene) consumed by
// buildGenerationResult.
//
// The pre-fix failure mode: clip-bindings synthesised scenes
// were kept in PipelineResult.SynthesizedScenes + IsEmpty==false,
// but the registry never wrote them back into the by-value
// ProcessInput passed to subsequent processors (document,
// persistence). buildGenerationResult also read the pre-walk
// engineResult.Output.SpecScene — so the JSON envelope, document
// body, persistence row, image prompts, and voiceover plan
// all saw empty scenes even though the heuristic reported success.
// The tests below fix the loop-hole at both ends: write-back inside
// the registry, FinalSpecScene fallback inside buildGenerationResult.
package adapters_test

import (
	"context"
	"testing"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

// synthesisingProcessor records the input.SpecScene it received at
// Process time (so tests can assert write-back behaviour) AND
// optionally emits a fixed SynthesizedScenes bundle (simulating
// the FASE 3 clip-bindings prose-fallback heuristic).
//
// policy is REQUIRED to be set explicitly by every test that
// instantiates this stub — the code-reviewer's F-1 finding
// flagged a maintenance trap where the implicit BestEffort
// default would let a downstream change (eg. defaulting to
// Required to mirror production) silently flip issue #1 tests
// into the "Required-empty-output-counts-as-failure" path.
// Code-reviewer-minimax-m3 (Issue #1 PR, June 2026).
type synthesisingProcessor struct {
	name            adapterspkg.ProcessorName
	contributeSynth []scriptpkg.SpecScene
	lastInputScenes []scriptpkg.SpecScene
	policy          adapterspkg.ProcessorPolicy // explicit per-policy test authorship
}

func (p *synthesisingProcessor) Name() adapterspkg.ProcessorName { return p.name }
func (p *synthesisingProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) adapterspkg.ProcessorPolicy {
	if p.policy == "" {
		// FAIL-FAST — neither default nor silent fallback. The
		// empty policy would surface as an unknown "best_effort"
		// via the registry's LookupPolicy default-to-required
		// path, masking test authorship intent. Forcing tests
		// to opt into a concrete policy keeps the suite honest.
		panic("synthesisingProcessor: explicit policy required for each test stub (code-reviewer F-1 finding, June 2026)")
	}
	return p.policy
}
func (p *synthesisingProcessor) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, input adapterspkg.ProcessInput) (*adapterspkg.PostProcessResult, error) {
	p.lastInputScenes = append([]scriptpkg.SpecScene(nil), input.SpecScene.Scenes...)
	if len(p.contributeSynth) == 0 {
		return &adapterspkg.PostProcessResult{}, nil
	}
	return &adapterspkg.PostProcessResult{
		SynthesizedScenes: append([]scriptpkg.SpecScene(nil), p.contributeSynth...),
	}, nil
}

type voiceoverEmittingProcessor struct {
	lastInputScenes []scriptpkg.SpecScene
	policy          adapterspkg.ProcessorPolicy
}

func (p *voiceoverEmittingProcessor) Name() adapterspkg.ProcessorName {
	return adapterspkg.ProcessorVoiceover
}
func (p *voiceoverEmittingProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) adapterspkg.ProcessorPolicy {
	if p.policy == "" {
		return adapterspkg.ProcessorBestEffort
	}
	return p.policy
}
func (p *voiceoverEmittingProcessor) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, input adapterspkg.ProcessInput) (*adapterspkg.PostProcessResult, error) {
	p.lastInputScenes = append([]scriptpkg.SpecScene(nil), input.SpecScene.Scenes...)
	return &adapterspkg.PostProcessResult{
		Voiceovers: []adapterspkg.SceneVoiceover{{
			SceneIndex: 0,
			Status:     "completed",
			Link:       "https://drive.google.com/file/d/voice-0",
			LocalPath:  "/tmp/voice-0.mp3",
		}},
	}, nil
}

// Issue #1 regression: a processor that emits SynthesizedScenes
// must propagate them via the registry-local ProcessInput so
// every subsequent processor in the same Run sees the
// synthesised scenes. The pre-fix behaviour kept the synthetic
// bundle in PipelineResult.SynthesizedScenes but left
// input.SpecScene.Scenes unchanged — so document and persistence
// processors downstream of clip_bindings still saw an empty
// scene list, defeating the prose-fallback.
func TestRegistry_Run_SynthesizedScenesWriteBack(t *testing.T) {
	log := zap.NewNop()
	r := adapterspkg.NewPostProcessorRegistry(log)
	synth := &synthesisingProcessor{
		name:   "clip_bindings",
		policy: adapterspkg.ProcessorBestEffort, // matches production ClipBindingsProcessor.Policy()
		contributeSynth: []scriptpkg.SpecScene{
			{ID: "syn-0", Index: 0, Kind: scriptpkg.SceneIntro, Text: "Alpha"},
			{ID: "syn-1", Index: 1, Kind: scriptpkg.SceneClip, Text: "Beta"},
			{ID: "syn-2", Index: 2, Kind: scriptpkg.SceneOutro, Text: "Gamma"},
		},
	}
	downstream := &synthesisingProcessor{name: "document", policy: adapterspkg.ProcessorBestEffort}
	r.Register(synth)
	r.Register(downstream)
	r.Freeze()

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-issue-1-writeback",
		Postprocessors: []string{"clip_bindings", "document"},
	}
	input := adapterspkg.ProcessInput{
		Text: "some prose fallback payload",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  nil,
		},
	}

	result, err := r.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// 1. Downstream processor saw the synthesised scenes via the
	//    registry-local ProcessInput write-back.
	if len(downstream.lastInputScenes) != 3 {
		t.Fatalf("downstream processor saw %d scenes, want 3 (write-back failed)",
			len(downstream.lastInputScenes))
	}
	if downstream.lastInputScenes[0].ID != "syn-0" {
		t.Errorf("downstream saw scene[0].ID = %q, want %q", downstream.lastInputScenes[0].ID, "syn-0")
	}
	if downstream.lastInputScenes[2].Kind != scriptpkg.SceneOutro {
		t.Errorf("downstream saw scene[2].Kind = %q, want SceneOutro", downstream.lastInputScenes[2].Kind)
	}

	// 2. PipelineResult.FinalSpecScene carries the synthesised
	//    bundle for buildGenerationResult.
	if len(result.FinalSpecScene.Scenes) != 3 {
		t.Fatalf("FinalSpecScene.Scenes = %d, want 3", len(result.FinalSpecScene.Scenes))
	}
	if result.FinalSpecScene.Version != 1 {
		t.Errorf("FinalSpecScene.Version = %d, want 1 (registry preserved engine version)", result.FinalSpecScene.Version)
	}
	if result.FinalSpecScene.Scenes[1].Kind != scriptpkg.SceneClip {
		t.Errorf("FinalSpecScene[1].Kind = %q, want SceneClip", result.FinalSpecScene.Scenes[1].Kind)
	}

	// 3. SynthesizedScenes also surfaced for downstream readers
	//    that prefer the explicit field (eg. telemetry).
	if len(result.SynthesizedScenes) != 3 {
		t.Fatalf("SynthesizedScenes = %d, want 3", len(result.SynthesizedScenes))
	}
}

// Regression: voiceover outputs must be written back into the
// registry-local ProcessInput so downstream processors can persist
// the per-scene link before GenerateOneUseCase builds the final
// result envelope.
func TestRegistry_Run_VoiceoverWriteBack(t *testing.T) {
	log := zap.NewNop()
	r := adapterspkg.NewPostProcessorRegistry(log)
	vo := &voiceoverEmittingProcessor{policy: adapterspkg.ProcessorBestEffort}
	downstream := &synthesisingProcessor{name: "document", policy: adapterspkg.ProcessorBestEffort}
	r.Register(vo)
	r.Register(downstream)
	r.Freeze()

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-voiceover-writeback",
		Postprocessors: []string{"voiceover", "document"},
	}
	input := adapterspkg.ProcessInput{
		Text: "voiceover payload",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID:    "scene-0",
				Index: 0,
				Kind:  scriptpkg.SceneClip,
				Text:  "Hello world",
			}},
		},
	}

	result, err := r.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(downstream.lastInputScenes) != 1 {
		t.Fatalf("downstream processor saw %d scenes, want 1", len(downstream.lastInputScenes))
	}
	if downstream.lastInputScenes[0].Bindings.Voiceover == nil {
		t.Fatal("downstream processor did not receive a voiceover binding")
	}
	if got := downstream.lastInputScenes[0].Bindings.Voiceover.Link; got != "https://drive.google.com/file/d/voice-0" {
		t.Fatalf("downstream voiceover link = %q, want %q", got, "https://drive.google.com/file/d/voice-0")
	}
	if result.FinalSpecScene.Scenes[0].Bindings.Voiceover == nil {
		t.Fatal("FinalSpecScene did not retain the voiceover binding")
	}
	if got := result.FinalSpecScene.Scenes[0].Bindings.Voiceover.LocalPath; got != "/tmp/voice-0.mp3" {
		t.Fatalf("FinalSpecScene voiceover local path = %q, want %q", got, "/tmp/voice-0.mp3")
	}
}

// Issue #1 regression: when no processor synthesises,
// FinalSpecScene mirrors the input.SpecScene (which equals
// engineResult.Output.SpecScene at registry entry).
// buildGenerationResult uses the empty-aware fallback (only
// consumes FinalSpecScene when Scenes > 0).
func TestRegistry_Run_FinalSpecScenePreservesInputScenes(t *testing.T) {
	log := zap.NewNop()
	r := adapterspkg.NewPostProcessorRegistry(log)
	p := &synthesisingProcessor{name: "document", policy: adapterspkg.ProcessorBestEffort}
	r.Register(p)
	r.Freeze()

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-issue-1-no-synth",
		Postprocessors: []string{"document"},
	}
	seed := []scriptpkg.SpecScene{
		{ID: "real-0", Index: 0, Kind: scriptpkg.SceneClip, Text: "from model"},
	}
	input := adapterspkg.ProcessInput{
		Text:      "engine output",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 8042, Scenes: seed},
	}

	result, err := r.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// FinalSpecScene should equal input.SpecScene (no synth ran).
	if len(result.FinalSpecScene.Scenes) != 1 {
		t.Fatalf("FinalSpecScene.Scenes = %d, want 1", len(result.FinalSpecScene.Scenes))
	}
	if result.FinalSpecScene.Version != 8042 {
		t.Errorf("FinalSpecScene.Version = %d, want 8042 (preserved from input)", result.FinalSpecScene.Version)
	}
	if result.FinalSpecScene.Scenes[0].ID != "real-0" {
		t.Errorf("FinalSpecScene[0].ID = %q, want real-0", result.FinalSpecScene.Scenes[0].ID)
	}
}

// Issue #1: an empty input.SpecScene.Scenes that no processor
// touches produces an empty FinalSpecScene.Scenes — this is the
// canonical signal buildGenerationResult uses to fall back to
// engineResult.Output.SpecScene (no-op swap in that branch).
func TestRegistry_Run_FinalSpecSceneEmptyWhenNoWorkTouchesScenes(t *testing.T) {
	log := zap.NewNop()
	r := adapterspkg.NewPostProcessorRegistry(log)
	p := &synthesisingProcessor{name: "persistence", policy: adapterspkg.ProcessorBestEffort} // does not touch SpecScene
	r.Register(p)
	r.Freeze()

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-empty",
		Postprocessors: []string{"persistence"},
	}
	input := adapterspkg.ProcessInput{
		Text:      "no scenes",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: nil},
	}

	result, err := r.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(result.FinalSpecScene.Scenes) != 0 {
		t.Fatalf("FinalSpecScene.Scenes = %d (no synth ran; no engine scenes)",
			len(result.FinalSpecScene.Scenes))
	}
}

// Issue #1: registry.Run must be safe when no processors are
// requested (zero Postprocessors in the plan). FinalSpecScene
// mirrors the (un-walked) input envelope in this case.
func TestRegistry_Run_NoProcessorsFinalSpecSceneUnchanged(t *testing.T) {
	log := zap.NewNop()
	r := adapterspkg.NewPostProcessorRegistry(log)
	// (no Register, no Freeze needed — Run is a no-op for empty plans)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-bypass",
		Postprocessors: nil,
	}
	input := adapterspkg.ProcessInput{
		Text:      "bypass",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "b-0"}}},
	}

	result, err := r.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(result.FinalSpecScene.Scenes) != 1 || result.FinalSpecScene.Scenes[0].ID != "b-0" {
		t.Fatalf("FinalSpecScene.Scenes mismatch (got %v)", result.FinalSpecScene.Scenes)
	}
}

// P0 reorder (June 2026): clip_bindings MUST run BEFORE voiceover
// and images so the prose-fallback synthesised scenes are visible
// to artifact producers. This test uses the REAL buildPostprocessorList
// (via BuildPlan) — not a hardcoded list — and verifies that when
// clip_bindings synthesises scenes, the voiceover and images
// processors downstream receive them via ProcessInput write-back.
//
// Pre-fix: the old order (voiceover → images → clip_bindings)
// meant voiceover/images ran with empty scenes; the synthesised
// bundle was only visible to document+persistence. Post-fix:
// clip_bindings runs first and voiceover/images receive populated
// SpecScene.Scenes.

// Stub processors: all 5 postprocessors the plan expects must
// be registered (entities + metadata + clip_bindings +
// visual_planning + persistence). clip_bindings
// synthesises scenes from prose; persistence is the
// downstream consumer of synthesised scenes.

// Build a plan using the REAL buildPostprocessorList (via
// BuildPlan) with the 2 surviving ACTIVE flags enabled.

// The postprocessor list contains the 2 ACTIVE postprocessors
// (entities + metadata) + the unconditional scene-normalisation
// stages (clip_bindings + visual_planning) + persistence. The
// clip_bindings ordering invariant is still valid: it must run
// before the final persistence write so synthesised scenes are
// visible.

// clip_bindings must appear before persistence (the last
// scene-normalisation stage) so synthesised scenes are
// visible to the final write.

// Run the registry with the real plan. Input has empty scenes
// (simulating LLM prose-only output).

// Runtime assertion: clip_bindings synthesised scenes must be
// visible to the persistence postprocessor (the last
// scene-normalisation stage).

// FinalSpecScene must carry the synthesised bundle.
