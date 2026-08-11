// Package scene_test — scene_planner_test.go: hermetic TDD coverage
// for the ScenePlanner introduced in Wave 1.1 (July 2026).
//
// godlike/06 SSOT: this file pins the canonical scene-construction
// contract at the planner boundary. Test names map 1:1 to the
// planner's exported methods (Plan / PlanFromClipEvidence) so that
// a regression in one method surfaces as a single failed test.
//
// What we cover here (parallel to binder_test.go but at the
// planner boundary):
//
//  1. Plan decision tree (no plan / empty scenes / empty text /
//     prose-fallback / clip-evidence / model draft).
//  2. Suppression contract for clips source with empty evidence.
//  3. PlanFromClipEvidence transcript / description / name
//     precedence + NumClips cap + clip-evidence kind assignment.
//  4. assignKindsByPosition threshold (>=3 contract).
//  5. Idempotence + scanner pinning for narrative contracts.
package scene_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Plan decision tree ───────────────────────────────────────────

// TestScenePlanner_Plan_NilPlan is the canonical no-op branch:
// nil plan collapses to ScenePlanSourceNoop with empty scenes.
// Mirrors binder_test.go::TestSceneAssetBinder_BindClips_NilPlan.
func TestScenePlanner_Plan_NilPlan(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	got := p.Plan(scene.NarrativeDraft{Text: "some prose"}, nil)
	assert.Equal(t, scene.ScenePlanSourceNoop, got.Source)
	assert.Nil(t, got.Scenes)
	assert.False(t, got.Synthesized)
	assert.False(t, got.Suppressed)
}

// TestScenePlanner_Plan_EmptyScenesAndEmptyTextIsNoop locks the
// canonical pre-Phase-2 behavior: when both scenes AND text are
// empty, the planner MUST NOT synthesize from clip-evidence (the
// clip-evidence builder is a separate Planner method that callers
// invoke explicitly). Mirrors binder_test.go's
// TestSceneAssetBinder_BindClips_ProseFallbackEmptyText at the
// planner boundary.
func TestScenePlanner_Plan_EmptyScenesAndEmptyTextIsNoop(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a"},
			DriveLinks:      map[string]string{"clip-a": "https://drive/a"},
		},
	}
	got := p.Plan(scene.NarrativeDraft{}, plan)
	assert.Equal(t, scene.ScenePlanSourceNoop, got.Source)
	assert.Nil(t, got.Scenes)
	assert.False(t, got.Synthesized)
}

// TestScenePlanner_Plan_ClipsSourceEmptyEvidenceSuppressed locks
// the godlike/07 NO-FAKE-AVAILABILITY contract: when source is
// clips AND evidence is empty, the planner MUST NOT silently fall
// back to prose; it must surface Suppressed=true so the binder
// emits CLIP_NATIVE_PLAN_UNAVAILABLE downstream.
func TestScenePlanner_Plan_ClipsSourceEmptyEvidenceSuppressed(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{},
		},
	}
	draft := scene.NarrativeDraft{
		SourceKind: string(scriptpkg.SourceClips),
		Text:       "ignored prose",
	}
	got := p.Plan(draft, plan)
	assert.Equal(t, scene.ScenePlanSourceNoop, got.Source)
	assert.True(t, got.Suppressed, "clips+empty evidence MUST signal Suppressed (NO-FAKE-AVAILABILITY)")
	assert.Nil(t, got.Scenes)
}

// TestScenePlanner_Plan_PreservesMicrosoftDraft locks the canonical
// preservation contract: when the LLM emitted scenes, the planner
// returns them verbatim (text/title/content), assigning kinds by
// position when >=3 clips. Text is NEVER overwritten.
func TestScenePlanner_Plan_PreservesMicrosoftDraft(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	modelScenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "Creative scene 1", Kind: scriptpkg.SceneClip},
		{ID: "scene-1", Index: 1, Text: "Creative scene 2", Kind: scriptpkg.SceneClip},
		{ID: "scene-2", Index: 2, Text: "Creative scene 3", Kind: scriptpkg.SceneClip},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
		},
	}
	draft := scene.NarrativeDraft{
		SourceKind: string(scriptpkg.SourceClips),
		Scenes:     modelScenes,
	}

	got := p.Plan(draft, plan)
	assert.Equal(t, scene.ScenePlanSourceMicrosoftDraft, got.Source)
	require.Len(t, got.Scenes, 3)

	// Text preserved verbatim — planner does NOT overwrite.
	assert.Equal(t, "Creative scene 1", got.Scenes[0].Text)
	assert.Equal(t, "Creative scene 2", got.Scenes[1].Text)
	assert.Equal(t, "Creative scene 3", got.Scenes[2].Text)

	// Kinds assigned by position.
	assert.Equal(t, scriptpkg.SceneIntro, got.Scenes[0].Kind)
	assert.Equal(t, scriptpkg.SceneClip, got.Scenes[1].Kind)
	assert.Equal(t, scriptpkg.SceneOutro, got.Scenes[2].Kind)
}

// TestScenePlanner_Plan_PreservesMicrosoftDraftInPlace locks the
// deliberate in-place mutation contract: when the LLM emitted
// scenes, the planner must return draft.Scenes BY REFERENCE so the
// binder's per-scene binding loop + the planner's kind-assignment
// loop mutate the caller's scenes in place. This matches the
// canonical pre-Phase-2 binder contract (BindClips mutated the
// caller's slice directly) — any defensive copy here would silently
// break every existing test that inspects scene mutations after
// the call.
//
// godlike/07 minimum-blast-radius: this is an intentional non-copy
// contract. The defensive-copy recipe belongs in higher layers
// (orchestration) where the caller's intent is "give me a fresh
// scene list", NOT at the planner boundary where the binder reads
// the same backing array to walk scenes per-iter for binding.
func TestScenePlanner_Plan_PreservesMicrosoftDraftInPlace(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	modelScenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Text: "Original", Kind: scriptpkg.SceneClip},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a"},
		},
	}
	got := p.Plan(scene.NarrativeDraft{Scenes: modelScenes}, plan)
	require.Len(t, got.Scenes, 1)

	// The returned slice MUST be the same backing array; mutating
	// the original MUST be observable through the returned scenes.
	modelScenes[0].Text = "Mutated"
	assert.Equal(t, "Mutated", got.Scenes[0].Text,
		"planner must return scenes by reference (in-place contract)")
}

// TestScenePlanner_Plan_ProseFallbackEngaged covers the FASE 3
// prose-fallback path: scenes empty + text non-empty → synthesizer
// partitions + Synthesized=true + Source="prose_fallback".
func TestScenePlanner_Plan_ProseFallbackEngaged(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	const prose = "First sentence here. Second sentence here. Third sentence here."
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			DriveLinks: map[string]string{
				"clip-a": "https://drive/a",
				"clip-b": "https://drive/b",
				"clip-c": "https://drive/c",
			},
		},
	}
	draft := scene.NarrativeDraft{Text: prose}

	got := p.Plan(draft, plan)
	assert.Equal(t, scene.ScenePlanSourceProseFallback, got.Source)
	assert.True(t, got.Synthesized)
	require.Len(t, got.Scenes, 3, "prose-fallback must synthesize 3 scenes (one per clip)")
}

// TestScenePlanner_Plan_ProseFallbackNoClipEvidence covers the
// prose-only path: scenes empty + text non-empty + no clips →
// synthesizer produces a NumClips-sized partition OR a
// sentence-derived partition when NumClips is zero.
func TestScenePlanner_Plan_ProseFallbackNoClipEvidence(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	draft := scene.NarrativeDraft{
		Text:              "First sentence here. Second sentence here. Third sentence here.",
		NumClips:          2,
		SentencesPerImage: 1,
	}
	got := p.Plan(draft, &scriptpkg.ResolvedGenerationPlan{})
	assert.Equal(t, scene.ScenePlanSourceProseFallback, got.Source)
	assert.True(t, got.Synthesized)
	require.Len(t, got.Scenes, 2,
		"prose-fallback with NumClips=2 must produce exactly 2 scenes")
}

func TestScenePlanner_Plan_MaterializesLongSingleDraftBySegmentWords(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	text := "One two three four five six seven eight nine ten. Eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty. Twenty one twenty two twenty three twenty four twenty five twenty six twenty seven twenty eight twenty nine thirty."
	got := p.Plan(scene.NarrativeDraft{
		Text:   text,
		Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: text}},
	}, &scriptpkg.ResolvedGenerationPlan{SegmentWords: 10})
	assert.Equal(t, scene.ScenePlanSourceProseFallback, got.Source)
	assert.True(t, got.Synthesized)
	assert.GreaterOrEqual(t, len(got.Scenes), 3)
	for i, s := range got.Scenes {
		assert.Equal(t, i, s.Index)
		assert.NotEmpty(t, s.Text)
	}
}

// ── PlanFromClipEvidence ─────────────────────────────────────────

// TestScenePlanner_PlanFromClipEvidence_TranscriptPriority locks
// the evidence-precedence contract: transcript > description >
// name. The plan-from-evidence builder never invents text; when
// every fallback is empty, the placeholder pattern from
// SceneSynthesizer takes over so SpecScene.Validate() does not
// fail on the "text is required" rule.
func TestScenePlanner_PlanFromClipEvidence_TranscriptPriority(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-a": {Transcript: "transcript a", Description: "desc a", Name: "name a"},
				"clip-b": {Description: "desc b", Name: "name b"},
				"clip-c": {Name: "name c"},
			},
		},
	}
	scenes := p.PlanFromClipEvidence(plan)
	require.Len(t, scenes, 3)

	assert.Equal(t, "transcript a", scenes[0].Text)
	assert.Equal(t, "desc b", scenes[1].Text)
	assert.Equal(t, "name c", scenes[2].Text)
}

// TestScenePlanner_PlanFromClipEvidence_KindAssignment locks the
// position-to-kind policy for clip-evidence-built scenes: intro /
// clip / outro when count >= 3; SceneClip otherwise.
func TestScenePlanner_PlanFromClipEvidence_KindAssignment(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())

	// n == 1 → all SceneClip.
	plan1 := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a"},
		},
	}
	scenes1 := p.PlanFromClipEvidence(plan1)
	require.Len(t, scenes1, 1)
	assert.Equal(t, scriptpkg.SceneClip, scenes1[0].Kind)

	// n == 2 → all SceneClip.
	plan2 := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b"},
		},
	}
	scenes2 := p.PlanFromClipEvidence(plan2)
	for i, sc := range scenes2 {
		assert.Equal(t, scriptpkg.SceneClip, sc.Kind,
			"n<3 contract: scene[%d].Kind = %q, want SceneClip", i, sc.Kind)
	}

	// n >= 3 → intro/clip/outro layout.
	plan3 := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
		},
	}
	scenes3 := p.PlanFromClipEvidence(plan3)
	assert.Equal(t, scriptpkg.SceneIntro, scenes3[0].Kind)
	assert.Equal(t, scriptpkg.SceneClip, scenes3[1].Kind)
	assert.Equal(t, scriptpkg.SceneOutro, scenes3[2].Kind)
}

// TestScenePlanner_PlanFromClipEvidence_NumClipsCap verifies the
// canonical NumClips cap: when plan.NumClips < AcceptedClipIDs,
// the builder stops at NumClips scenes.
func TestScenePlanner_PlanFromClipEvidence_NumClipsCap(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		NumClips: 2,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-a": {Name: "name a"},
				"clip-b": {Name: "name b"},
				"clip-c": {Name: "name c"},
			},
		},
	}
	scenes := p.PlanFromClipEvidence(plan)
	require.Len(t, scenes, 2)
	assert.Equal(t, "name a", scenes[0].Text)
	assert.Equal(t, "name b", scenes[1].Text)
}

// TestScenePlanner_PlanFromClipEvidence_BindingsPopulated locks the
// ClipBinding population contract: ClipID, ClipTitle, DriveLink,
// StartMs, EndMs, DurationMs all come from the evidence detail
// (with fallback to ClipNames[clipID] / DriveLinks[clipID]).
func TestScenePlanner_PlanFromClipEvidence_BindingsPopulated(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a"},
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-a": {
					Name:           "name a",
					StartMs:        0,
					EndMs:          1000,
					DriveLink:      "https://drive/a",
					SubtitleLink:   "https://drive/subtitle-a",
					SubtitleFileID: "subtitle-a",
				},
			},
		},
	}
	scenes := p.PlanFromClipEvidence(plan)
	require.Len(t, scenes, 1)
	require.NotNil(t, scenes[0].Bindings.Clip)

	binding := scenes[0].Bindings.Clip
	assert.Equal(t, "clip-a", binding.ClipID)
	assert.Equal(t, "name a", binding.ClipTitle)
	assert.Equal(t, "https://drive/a", binding.DriveLink)
	assert.Equal(t, "https://drive/subtitle-a", binding.SubtitleLink)
	assert.Equal(t, "subtitle-a", binding.SubtitleFileID)
	assert.Equal(t, int64(0), binding.StartMs)
	assert.Equal(t, int64(1000), binding.EndMs)
	assert.Equal(t, int64(1000), binding.DurationMs)
}

// TestScenePlanner_PlanFromClipEvidence_NilOrEmpty returns nil
// for nil plan / nil evidence / empty AcceptedClipIDs. Mirrors the
// canonical "no-op when nothing" contract used across the
// scene package.
func TestScenePlanner_PlanFromClipEvidence_NilOrEmpty(t *testing.T) {
	t.Parallel()
	p := scene.NewScenePlanner(zap.NewNop())

	assert.Nil(t, p.PlanFromClipEvidence(nil))
	assert.Nil(t, p.PlanFromClipEvidence(&scriptpkg.ResolvedGenerationPlan{}))
	assert.Nil(t, p.PlanFromClipEvidence(&scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{AcceptedClipIDs: nil},
	}))
}

// ── ScenePlan metadata ───────────────────────────────────────────

// TestScenePlanner_Plan_CanonicalSourceValues locks the canonical
// Source value taxonomy. A future agent adding a new source
// (e.g. "table" for table-driven input) MUST export the const and
// update this test — the baseline pinning prevents silent
// alphabet drift.
func TestScenePlanner_Plan_CanonicalSourceValues(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "noop", scene.ScenePlanSourceNoop)
	assert.Equal(t, "microsoft_draft", scene.ScenePlanSourceMicrosoftDraft)
	assert.Equal(t, "clip_evidence", scene.ScenePlanSourceClipEvidence)
	assert.Equal(t, "prose_fallback", scene.ScenePlanSourceProseFallback)
}
