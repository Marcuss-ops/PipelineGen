package script_test

import (
	"testing"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
)

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
