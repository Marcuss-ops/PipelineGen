package script_test

import (
	"testing"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
)

func TestOverlayCategoryControlsAffectFingerprintAndAreDeepCopied(t *testing.T) {
	item := script.GenerationItemV2{OverlayCategories: map[script.OverlayCategory]script.Toggle{script.OverlayCategoryImages: script.ToggleDisabled}}
	base := script.BuildFingerprint(script.FingerprintInputFromItem(item))
	item.OverlayCategories[script.OverlayCategoryImages] = script.ToggleEnabled
	if got := script.BuildFingerprint(script.FingerprintInputFromItem(item)); got == base {
		t.Fatal("run category toggle did not affect fingerprint")
	}
	item.RandomLightLeakOnClipStart = true
	if got := script.BuildFingerprint(script.FingerprintInputFromItem(item)); got == base {
		t.Fatal("clip-start light-leak toggle did not affect fingerprint")
	}
	item.RandomLightLeakOnOverlayStart = true
	if got := script.BuildFingerprint(script.FingerprintInputFromItem(item)); got == base {
		t.Fatal("overlay-start light-leak toggle did not affect fingerprint")
	}
	segment := script.ScriptSegment{OverlayCategories: map[script.OverlayCategory]script.Toggle{script.OverlayCategoryPhrases: script.ToggleDisabled}}
	cloned := script.CloneScriptSegments([]script.ScriptSegment{segment})
	cloned[0].OverlayCategories[script.OverlayCategoryPhrases] = script.ToggleEnabled
	if segment.OverlayCategories[script.OverlayCategoryPhrases] != script.ToggleDisabled {
		t.Fatal("segment category map was aliased")
	}
}

func TestBuildFingerprint_Deterministic(t *testing.T) {
	input := script.GenerationFingerprintInput{
		ContractVersion:     1,
		SourceType:          "text",
		SourceTextHash:      "sha256:abc",
		Language:            "en",
		Tone:                "documentary",
		Style:               "cinematic",
		Guidelines:          "Keep it factual.",
		TargetWords:         500,
		Model:               "llama3:8b",
		PromptVersion:       "v1",
		EditorPromptVersion: "v1",
		QAPromptVersion:     "v1",
		PlannerVersion:      "default-v1",
		GroundingPolicy:     "strict",
	}

	id1 := script.BuildFingerprint(input)
	id2 := script.BuildFingerprint(input)

	assert.Equal(t, id1, id2, "fingerprint must be deterministic")
	assert.Len(t, id1, 16, "fingerprint must be 16 hex chars")
}

func TestBuildFingerprint_DifferentFieldChanges(t *testing.T) {
	base := script.GenerationFingerprintInput{
		ContractVersion: 1,
		SourceType:      "text",
		SourceTextHash:  "sha256:abc",
		Language:        "en",
		Tone:            "documentary",
		Style:           "cinematic",
		Guidelines:      "Keep it factual.",
		TargetWords:     500,
		Model:           "llama3:8b",
		PromptVersion:   "v1",
		PlannerVersion:  "default-v1",
		GroundingPolicy: "strict",
	}

	cases := []struct {
		name    string
		mutator func(*script.GenerationFingerprintInput)
	}{
		{"language", func(i *script.GenerationFingerprintInput) { i.Language = "it" }},
		{"tone", func(i *script.GenerationFingerprintInput) { i.Tone = "dramatic" }},
		{"style", func(i *script.GenerationFingerprintInput) { i.Style = "narrative" }},
		{"guidelines", func(i *script.GenerationFingerprintInput) { i.Guidelines = "Be funny." }},
		{"target_words", func(i *script.GenerationFingerprintInput) { i.TargetWords = 800 }},
		{"model", func(i *script.GenerationFingerprintInput) { i.Model = "qwen2:7b" }},
		{"prompt_version", func(i *script.GenerationFingerprintInput) { i.PromptVersion = "v2" }},
		{"editor_prompt_version", func(i *script.GenerationFingerprintInput) { i.EditorPromptVersion = "v2" }},
		{"qa_prompt_version", func(i *script.GenerationFingerprintInput) { i.QAPromptVersion = "v2" }},
		{"planner_version", func(i *script.GenerationFingerprintInput) { i.PlannerVersion = "experimental-v3" }},
		{"grounding_policy", func(i *script.GenerationFingerprintInput) { i.GroundingPolicy = "loose" }},
		{"source_text_hash", func(i *script.GenerationFingerprintInput) { i.SourceTextHash = "sha256:def" }},
		{"source_type", func(i *script.GenerationFingerprintInput) { i.SourceType = "clips" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i1 := base
			i2 := base
			tc.mutator(&i2)
			assert.NotEqual(t, script.BuildFingerprint(i1), script.BuildFingerprint(i2),
				"changing %s must change the fingerprint", tc.name)
		})
	}
}

func TestBuildFingerprint_ClipIDOrderStable(t *testing.T) {
	input := script.GenerationFingerprintInput{
		ContractVersion:      1,
		SourceType:           "clips",
		ClipIDs:              []string{"clip-c", "clip-a", "clip-b"},
		ClipTranscriptHashes: []string{"hash-c", "hash-a", "hash-b"},
	}

	id1 := script.BuildFingerprint(input)

	input2 := script.GenerationFingerprintInput{
		ContractVersion:      1,
		SourceType:           "clips",
		ClipIDs:              []string{"clip-a", "clip-b", "clip-c"},
		ClipTranscriptHashes: []string{"hash-a", "hash-b", "hash-c"},
	}
	id2 := script.BuildFingerprint(input2)

	assert.Equal(t, id1, id2, "fingerprint must be stable regardless of input clip order")
}

func TestBuildFingerprint_ClipTranscriptHashesPaired(t *testing.T) {
	// Two clip IDs with different transcript hashes must produce a
	// different fingerprint than the same clip IDs with swapped
	// transcript hashes.
	input1 := script.GenerationFingerprintInput{
		ContractVersion:      1,
		SourceType:           "clips",
		ClipIDs:              []string{"clip-a", "clip-b"},
		ClipTranscriptHashes: []string{"hash-a", "hash-b"},
	}
	input2 := script.GenerationFingerprintInput{
		ContractVersion:      1,
		SourceType:           "clips",
		ClipIDs:              []string{"clip-a", "clip-b"},
		ClipTranscriptHashes: []string{"hash-b", "hash-a"},
	}

	assert.NotEqual(t, script.BuildFingerprint(input1), script.BuildFingerprint(input2),
		"swapping transcript hashes must change the fingerprint")
}

func TestFingerprintInputFromSource_TextAssemblesSourceText(t *testing.T) {
	src := script.SourceSpec{
		Type:       script.SourceText,
		Topic:      "The future of AI",
		SourceText: "Artificial intelligence is transforming society.",
		Guidelines: "Keep it factual.",
	}

	input := script.FingerprintInputFromSource(src, nil)

	assert.Equal(t, "text", input.SourceType)
	assert.NotEmpty(t, input.SourceTextHash)
	assert.Len(t, input.SourceTextHash, 64, "source text hash must be a 64-char SHA-256 hex digest")
	assert.Equal(t, "Keep it factual.", input.Guidelines)
}

func TestFingerprintInputFromSource_ClipsUsesEvidence(t *testing.T) {
	src := script.SourceSpec{
		Type:            script.SourceClips,
		ClipIDs:         []string{"clip-a", "clip-b"},
		GroundingPolicy: "strict",
	}
	ev := &script.ClipEvidence{
		AcceptedClipIDs:      []string{"clip-a", "clip-b"},
		AssembledText:        "assembled clip context",
		ClipTranscriptHashes: []string{"hash-a", "hash-b"},
	}

	input := script.FingerprintInputFromSource(src, ev)

	assert.Equal(t, "clips", input.SourceType)
	assert.Equal(t, "strict", input.GroundingPolicy)
	assert.Equal(t, []string{"clip-a", "clip-b"}, input.ClipIDs)
	assert.Equal(t, []string{"hash-a", "hash-b"}, input.ClipTranscriptHashes)
	assert.NotEmpty(t, input.SourceTextHash)
}

func TestFingerprintInputFromPlan_WiresGroundingPolicy(t *testing.T) {
	plan := script.ResolvedGenerationPlan{
		Language:          "en",
		SourceKind:        "clips",
		SourceFingerprint: "fp-abc",
		GroundingPolicy:   "loose",
		PromptProfile:     "default-v1",
		ClipEvidence: &script.ClipEvidence{
			AcceptedClipIDs:      []string{"clip-a"},
			ClipTranscriptHashes: []string{"hash-a"},
		},
	}

	input := script.FingerprintInputFromPlan(&plan)

	assert.Equal(t, "loose", input.GroundingPolicy)
	assert.Equal(t, "default-v1", input.PlannerVersion)
	assert.Equal(t, []string{"clip-a"}, input.ClipIDs)
}

func TestFingerprintInputFromPlan_WiresPromptVersions(t *testing.T) {
	plan := script.ResolvedGenerationPlan{
		Language:            "en",
		SourceKind:          "text",
		SourceFingerprint:   "fp-abc",
		PromptVersion:       "pv1",
		EditorPromptVersion: "ev1",
		QAPromptVersion:     "qv1",
		PromptProfile:       "default-v1",
	}

	input := script.FingerprintInputFromPlan(&plan)

	assert.Equal(t, "pv1", input.PromptVersion)
	assert.Equal(t, "ev1", input.EditorPromptVersion)
	assert.Equal(t, "qv1", input.QAPromptVersion)
	assert.Equal(t, 2, input.ContractVersion)
}

func TestEntityExtractionPoliciesChangeFingerprint(t *testing.T) {
	item := script.GenerationItemV2{
		Source: script.SourceSpec{Type: script.SourceText, Topic: "topic"},
		ScriptParams: script.ScriptSpec{Segments: []script.ScriptSegment{{
			ID: "scene-1", Topic: "scene", ExtractEntities: script.ToggleDisabled,
		}}},
		Output: script.OutputSpec{ExtractEntities: script.ToggleDisabled},
	}
	disabled := script.BuildFingerprint(script.FingerprintInputFromItem(item))
	item.Output.ExtractEntities = script.ToggleEnabled
	runEnabled := script.BuildFingerprint(script.FingerprintInputFromItem(item))
	assert.NotEqual(t, disabled, runEnabled, "run-level extraction policy affects downstream outputs")

	item.Output.ExtractEntities = script.ToggleDisabled
	item.ScriptParams.Segments[0].ExtractEntities = script.ToggleEnabled
	segmentEnabled := script.BuildFingerprint(script.FingerprintInputFromItem(item))
	assert.NotEqual(t, disabled, segmentEnabled, "per-segment extraction policy affects downstream outputs")

	plan := script.ResolvedGenerationPlan{
		ExtractEntities: script.ToggleDisabled,
		Segments: []script.ScriptSegment{{
			ID: "scene-1", Topic: "scene", ExtractEntities: script.ToggleDisabled,
		}},
	}
	planDisabled := script.BuildFingerprint(script.FingerprintInputFromPlan(&plan))
	plan.ExtractEntities = script.ToggleEnabled
	planRunEnabled := script.BuildFingerprint(script.FingerprintInputFromPlan(&plan))
	assert.NotEqual(t, planDisabled, planRunEnabled, "resolved-plan fingerprint includes the run toggle")
	plan.ExtractEntities = script.ToggleDisabled
	plan.Segments[0].ExtractEntities = script.ToggleEnabled
	planSegmentEnabled := script.BuildFingerprint(script.FingerprintInputFromPlan(&plan))
	assert.NotEqual(t, planDisabled, planSegmentEnabled, "resolved-plan fingerprint includes segment overrides")

	legacyDefault := script.GenerationItemV2{
		Source: script.SourceSpec{Type: script.SourceText, Topic: "topic"},
	}
	legacyExplicitDefault := legacyDefault
	legacyExplicitDefault.Output.ExtractEntities = script.ToggleDefault
	assert.Equal(t,
		script.BuildFingerprint(script.FingerprintInputFromItem(legacyDefault)),
		script.BuildFingerprint(script.FingerprintInputFromItem(legacyExplicitDefault)),
		"omitted and explicit default preserve legacy fingerprint identity",
	)
}

func TestFingerprintInputFromPlanDeepCopiesSegmentSlices(t *testing.T) {
	plan := script.ResolvedGenerationPlan{Segments: []script.ScriptSegment{{
		ID: "scene-1", Topic: "scene", ClipIDs: []string{"clip-a"}, StockClipIDs: []string{"clip-stock"},
	}}}
	input := script.FingerprintInputFromPlan(&plan)
	input.Segments[0].ClipIDs[0] = "changed"
	input.Segments[0].StockClipIDs[0] = "changed"
	assert.Equal(t, "clip-a", plan.Segments[0].ClipIDs[0])
	assert.Equal(t, "clip-stock", plan.Segments[0].StockClipIDs[0])
}

func TestSourceTextVerbatimIsFingerprintInput(t *testing.T) {
	item := script.GenerationItemV2{
		Source: script.SourceSpec{Type: script.SourceText, Topic: "existing script"},
		ScriptParams: script.ScriptSpec{
			Segments: []script.ScriptSegment{{ID: "scene-one", Topic: "first", SourceText: "Exact text."}},
		},
	}
	generated := script.FingerprintInputFromItem(item)
	item.ScriptParams.SourceTextVerbatim = true
	verbatim := script.FingerprintInputFromItem(item)
	assert.False(t, generated.SourceTextVerbatim)
	assert.True(t, verbatim.SourceTextVerbatim)
	assert.NotEqual(t, script.BuildFingerprint(generated), script.BuildFingerprint(verbatim))

	plan := script.ResolvedGenerationPlan{Segments: item.ScriptParams.Segments}
	planGenerated := script.FingerprintInputFromPlan(&plan)
	plan.SourceTextVerbatim = true
	planVerbatim := script.FingerprintInputFromPlan(&plan)
	assert.NotEqual(t, script.BuildFingerprint(planGenerated), script.BuildFingerprint(planVerbatim))
}
