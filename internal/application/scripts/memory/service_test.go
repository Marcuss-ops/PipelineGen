package memory

import (
	"strings"
	"testing"
)

func TestBuildFreshVariantPrompt_DoesNotInjectRawPreviousOutput(t *testing.T) {
	const longOldOutput = "We live in an era defined by relentless pursuit of consumption. " +
		"True freedom is found not in what we earn but in what we resist buying. " +
		"Every dollar spent on subscription waste pulls us further from financial peace. " +
		"This chapter explores needs versus wants with a simple spending filter. " +
		"The plain budget categories will help you build a simple joy fund today. " +
		"Discipline compounds quietly while lifestyle inflation compounds loudly. " +
		"Mindful spending is the bridge between today income and tomorrow freedom. " +
		"By the end of this lesson you will design a personal spending filter you can apply tonight. " +
		"Words words words words words words words words words words words words words words words words words words words words words."

	exact := &GenerationOutput{
		OutputText: longOldOutput,
		Title:      "Spending Filter Chapter",
	}

	got := BuildFreshVariantPrompt("USER REQUEST: write a fresh chapter on mindful spending", exact)

	if strings.Contains(got, "[REFERENCE_OUTPUT]") {
		t.Fatalf("BuildFreshVariantPrompt must not dump the old output as a REFERENCE_OUTPUT block, got:\n%s", got)
	}
	if strings.Contains(got, "Words words words words words words words words words words words words words words words words words words words words words.") {
		t.Fatalf("BuildFreshVariantPrompt must not embed verbatim filler, got:\n%s", got)
	}
	if !strings.Contains(got, "PREVIOUS_RUN_AVOID_LIST") {
		t.Fatalf("expected avoid-list header, got:\n%s", got)
	}
	if !strings.Contains(got, "PHRASES_TO_AVOID") {
		t.Fatalf("expected phrases-to-avoid header, got:\n%s", got)
	}
	if !strings.Contains(got, "USER REQUEST: write a fresh chapter on mindful spending") {
		t.Fatalf("expected the base user request to be preserved, got:\n%s", got)
	}

	const compactCap = 2100
	if len(got) > compactCap {
		t.Fatalf("avoid-list output is too long (%d chars, cap %d); should stay compact to prevent verbatim reuse", len(got), compactCap)
	}
}

func TestBuildFreshVariantPrompt_HandlesEmptyExact(t *testing.T) {
	got := BuildFreshVariantPrompt("USER REQUEST: do the thing", nil)
	if got != "USER REQUEST: do the thing" {
		t.Fatalf("expected passthrough when exact is nil, got %q", got)
	}

	empty := &GenerationOutput{OutputText: "   "}
	got2 := BuildFreshVariantPrompt("USER REQUEST: do the thing", empty)
	if got2 != "USER REQUEST: do the thing" {
		t.Fatalf("expected passthrough when exact output is blank, got %q", got2)
	}
}

func TestBuildEnrichedPrompt_CapsOldOutputsAndTotalSize(t *testing.T) {
	// 6 hits with long summaries, but only 2 past scripts shown (MaxMemories=2).
	// Need MaxMemoryChars low enough that the output exceeds it.
	// 2 past scripts × ~170 chars + preamble ~200 + user_request ~60 = ~430 chars.
	// Set MaxMemoryChars=300 to force truncation.
	hits := make([]MemoryHit, 6)
	for i := range hits {
		hits[i] = MemoryHit{
			Entry: MemoryEntry{
				MemoryType: MemoryTypeScriptStructure,
				Summary:    strings.Repeat("long-summary-fragment ", 50),
			},
			Source: "recent",
		}
	}

	req := MemoryGateRequest{
		Title:    "Test Topic",
		Prompt:   "some prompt",
		Language: "en",
		Policy: MemoryPolicy{
			MaxMemories:  2,
			MaxMemoryChars: 300,
		},
	}

	got := BuildEnrichedPrompt(req, hits)

	const pastScriptsHeader = "RELEVANT PAST SCRIPTS:"
	headerIdx := strings.Index(got, pastScriptsHeader)
	if headerIdx < 0 {
		t.Fatalf("expected RELEVANT PAST SCRIPTS section, got:\n%s", got)
	}
	section := got[headerIdx:]
	if nextHeader := strings.Index(section[len(pastScriptsHeader):], "\n\n"); nextHeader >= 0 {
		section = section[:len(pastScriptsHeader)+nextHeader]
	}
	bullets := strings.Count(section, "\n- [script_structure]")
	if bullets != 2 {
		t.Fatalf("expected exactly %d script_structure bullets in RELEVANT PAST SCRIPTS, got %d", req.Policy.MaxMemories, bullets)
	}
	if !strings.Contains(got, "memory context truncated") {
		t.Fatalf("expected truncation marker when prompt exceeds cap, got:\n%s", got)
	}
}

func TestDetectNearDuplicate_FlagsSimilarText(t *testing.T) {
	policy := DefaultMemoryPolicy()

	previous := []string{
		"We live in an era defined by relentless pursuit of consumption and true freedom is found not in what we earn but in what we resist buying. The plain budget categories will help you build a simple joy fund today.",
	}

	almostIdentical := "We live in an era defined by relentless pursuit of consumption and true freedom is found not in what we earn but in what we resist buying. The plain budget categories will help you build a simple joy fund today."

	score, flagged := DetectNearDuplicate(almostIdentical, previous, policy)
	if !flagged {
		t.Fatalf("expected near-duplicate flag for an almost identical text, got score=%f", score)
	}
	if score < policy.SimilarityThreshold {
		t.Fatalf("expected score >= %f, got %f", policy.SimilarityThreshold, score)
	}
}

func TestDetectNearDuplicate_DoesNotFlagFreshText(t *testing.T) {
	policy := DefaultMemoryPolicy()

	previous := []string{
		"We live in an era defined by relentless pursuit of consumption and true freedom is found not in what we earn but in what we resist buying.",
	}

	fresh := "The Romans built aqueducts to move water across hundreds of miles. Lead pipes were common in private homes, and public latrines were flushed by the constant flow from these systems. The cost of maintaining this infrastructure eventually bankrupted the empire."

	score, flagged := DetectNearDuplicate(fresh, previous, policy)
	if flagged {
		t.Fatalf("expected no flag for unrelated topic, got score=%f", score)
	}
	if score >= policy.SimilarityThreshold {
		t.Fatalf("expected score below threshold, got %f (threshold %f)", score, policy.SimilarityThreshold)
	}
}

func TestDetectNearDuplicate_EmptyPreviousOutputsReturnsZero(t *testing.T) {
	policy := DefaultMemoryPolicy()
	score, flagged := DetectNearDuplicate("anything goes here", nil, policy)
	if score != 0 || flagged {
		t.Fatalf("expected (0, false) for empty previous, got (%f, %v)", score, flagged)
	}
}
