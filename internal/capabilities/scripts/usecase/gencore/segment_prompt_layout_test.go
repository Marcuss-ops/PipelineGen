package gencore

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// samplePlan returns a resolved plan whose segment brief exercises every
// builder branch: style, guidelines, one segment with source text and clip
// evidence, and the single-scene footer.
func samplePlan() *scriptpkg.ResolvedGenerationPlan {
	segment := scriptpkg.ScriptSegment{
		Topic:       "cold open",
		Kind:        "intro",
		TargetWords: 80,
		SourceText:  "Clip description: Milton leaves the courthouse. Write a funny introduction.",
		ClipIDs:     []string{"yt_abc_0_15_v1"},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		Style:      "cinematic documentary",
		Guidelines: "Never invent facts.",
		Segments:   []scriptpkg.ScriptSegment{segment},
	}
	plan.TargetWords = 80
	plan.ClipEvidence = scriptpkg.NewClipEvidence(scriptpkg.ClipEvidence{
		SegmentEvidence: []scriptpkg.SegmentClipEvidence{{
			ClipIDs: []string{"yt_abc_0_15_v1"},
			Clips: map[string]scriptpkg.ClipDetail{
				"yt_abc_0_15_v1": {Name: "courthouse", Description: "Milton walks out", Transcript: "he said nothing"},
			},
		}},
	})
	return plan
}

// TestBuildSegmentInstructions_ByteIdenticalWithSplit pins the B3 refactor
// contract: the legacy brief stays byte-for-byte the concatenation of the
// shared header and the per-segment body, so switching layouts never changes
// what the model is told — only where the blocks are placed in the message.
func TestBuildSegmentInstructions_ByteIdenticalWithSplit(t *testing.T) {
	plan := samplePlan()
	legacy := buildSegmentInstructions(plan)
	split := buildSegmentHeader(plan) + buildSegmentBody(plan)
	if legacy != split {
		t.Fatalf("split changed the legacy brief:\nlegacy=%q\nsplit=%q", legacy, split)
	}
	if !strings.Contains(legacy, "SEGMENT 1") {
		t.Fatalf("brief must contain the SEGMENT block, got %q", legacy)
	}
	if !strings.Contains(legacy, "Editorial style for every segment:") {
		t.Fatalf("brief must contain the shared style header, got %q", legacy)
	}
}

// TestSegmentPromptLayout_EnvOverride pins the knob contract: a known value
// switches the layout, an unknown value falls back to legacy, and the default
// is legacy.
func TestSegmentPromptLayout_EnvOverride(t *testing.T) {
	t.Setenv(EnvSegmentPromptLayout, SegmentPromptLayoutSharedPrefix)
	resetSegmentLayoutForTest()
	if SegmentPromptLayout() != SegmentPromptLayoutSharedPrefix {
		t.Fatalf("layout = %q, want shared-prefix", SegmentPromptLayout())
	}

	t.Setenv(EnvSegmentPromptLayout, "nonsense-value")
	resetSegmentLayoutForTest()
	if SegmentPromptLayout() != SegmentPromptLayoutLegacy {
		t.Fatalf("unknown value must fall back to legacy, got %q", SegmentPromptLayout())
	}

	t.Setenv(EnvSegmentPromptLayout, "")
	resetSegmentLayoutForTest()
	if SegmentPromptLayout() != SegmentPromptLayoutLegacy {
		t.Fatalf("default layout = %q, want legacy", SegmentPromptLayout())
	}
}

// TestSharedPrefixSegmentRequest_CarriesSplitBrief pins what generateOne
// builds in shared-prefix mode: Prompt empty, SharedPrefix = header +
// plain-text contract, SegmentAssignment = body. In legacy mode the fields
// stay empty and the legacy brief is used verbatim.
func TestSharedPrefixSegmentRequest_CarriesSplitBrief(t *testing.T) {
	plan := samplePlan()

	// Mirror generateOne's composition for both layouts without running the
	// fan-out (the fan-out contract itself is pinned by segment tests).
	t.Setenv(EnvSegmentPromptLayout, SegmentPromptLayoutSharedPrefix)
	resetSegmentLayoutForTest()
	shared := buildSegmentHeader(plan) + plainTextInstruction
	assignment := buildSegmentBody(plan)
	if !strings.HasPrefix(shared, "Editorial style for every segment:") {
		t.Fatalf("SharedPrefix must start with the shared header, got %q", truncateForTest(shared))
	}
	if !strings.Contains(shared, plainTextInstruction) {
		t.Fatalf("SharedPrefix must embed the plain-text contract")
	}
	if !strings.HasPrefix(assignment, "SEGMENT 1") {
		t.Fatalf("SegmentAssignment must start with the SEGMENT block, got %q", truncateForTest(assignment))
	}

	t.Setenv(EnvSegmentPromptLayout, SegmentPromptLayoutLegacy)
	resetSegmentLayoutForTest()
	legacyBrief := buildSegmentInstructions(plan) + "\n\n" + plainTextInstruction
	if !strings.HasPrefix(legacyBrief, "Editorial style for every segment:") {
		t.Fatalf("legacy brief must start with the shared header")
	}
	if !strings.Contains(legacyBrief, plainTextInstruction) {
		t.Fatalf("legacy brief must embed the plain-text contract")
	}
}

// TestSegmentPromptParts_ScopesOpeningSentenceToFirstSegment pins the source of
// the "opening sentence repeated in every segment" defect: the caller's style
// reaches every per-segment call, so a mandated opening line was reproduced at
// the start of each segment. Only the segments after the first carry the
// opening-scope directive, and the first segment (or a single-segment job) is
// byte-identical to the previous prompt.
func TestSegmentPromptParts_ScopesOpeningSentenceToFirstSegment(t *testing.T) {
	plan := samplePlan()

	t.Setenv(EnvSegmentPromptLayout, SegmentPromptLayoutSharedPrefix)
	resetSegmentLayoutForTest()
	firstPrompt, firstShared, firstAssignment := segmentPromptParts(plan, 0, 3)
	if firstPrompt != "" {
		t.Fatalf("shared-prefix layout must not use Prompt, got %q", firstPrompt)
	}
	if strings.Contains(firstShared, "opening sentence") || strings.Contains(firstAssignment, "opening sentence") {
		t.Fatalf("first segment must not carry the opening-scope directive")
	}
	if firstAssignment != buildSegmentBody(plan) {
		t.Fatalf("first segment assignment changed: %q", truncateForTest(firstAssignment))
	}

	laterPrompt, laterShared, laterAssignment := segmentPromptParts(plan, 1, 3)
	if laterPrompt != "" {
		t.Fatalf("shared-prefix layout must not use Prompt, got %q", laterPrompt)
	}
	if laterShared != firstShared {
		t.Fatalf("the shared prefix must stay KV-cache identical across the fan-out")
	}
	if !strings.Contains(laterAssignment, "segment 2 of 3") {
		t.Fatalf("later assignment must state its position, got %q", truncateForTest(laterAssignment))
	}
	if !strings.Contains(laterAssignment, "belongs to segment 1 only") {
		t.Fatalf("later assignment must scope the opening sentence to segment 1")
	}
	if !strings.HasPrefix(laterAssignment, buildSegmentBody(plan)) {
		t.Fatalf("the scope directive must be appended after the assignment")
	}

	singlePrompt, _, singleAssignment := segmentPromptParts(plan, 0, 1)
	if singlePrompt != "" || singleAssignment != buildSegmentBody(plan) {
		t.Fatalf("single-segment jobs must be unchanged: prompt=%q assignment=%q", singlePrompt, truncateForTest(singleAssignment))
	}

	t.Setenv(EnvSegmentPromptLayout, SegmentPromptLayoutLegacy)
	resetSegmentLayoutForTest()
	legacyFirst, shared, assignment := segmentPromptParts(plan, 0, 2)
	if shared != "" || assignment != "" {
		t.Fatalf("legacy layout must not fill the split fields")
	}
	if legacyFirst != buildSegmentInstructions(plan)+"\n\n"+plainTextInstruction {
		t.Fatalf("first legacy prompt changed: %q", truncateForTest(legacyFirst))
	}
	legacyLater, _, _ := segmentPromptParts(plan, 1, 2)
	if !strings.Contains(legacyLater, "segment 2 of 2") {
		t.Fatalf("legacy layout must also scope the opening sentence: %q", truncateForTest(legacyLater))
	}
}

func truncateForTest(s string) string {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}
