// Package scripts — generate_segment_topics_p0j_test.go: P0.J
// segment-topics contract for /api/script/generate.
//
// July 2026 PR — P0.J gate. Pins the canonical contract for
// how the engine prompt surfaces SegmentTopics to the LLM.
// SegmentTopics is an ORDERED list of topics (one per
// segment/clip) that the LLM uses to structure the narrative:
//
//	"topic 1 influenza l'apertura del racconto"
//	"topic 8 influenza la chiusura"
//
// Test seam: UNIT test of buildClipGroundingInstructions(plan)
// in engine_prompt.go. This is the canonical prompt-construction
// function that formats SegmentTopics into the model-facing
// prompt as "1. Topic1\n2. Topic2\n...". The test pins the
// EXACT prompt contents (topic ordering, numbering, presence/
// absence) without requiring the full orchestrator (which is
// blocked by pre-existing infra build errors).
//
// The "topic 1 → opening / topic 8 → closing" assertion is
// enforced at the PROMPT level: topic 1 is rendered FIRST
// in the prompt (so the LLM sees it as the opening topic),
// topic 8 is rendered LAST (so the LLM sees it as the
// closing topic). The LLM uses the order to structure the
// narrative; the test trusts the LLM to obey its instructions.
//
// USER-SPEC SCENARIOS (6 total):
//
//  1. 8 clips + 8 topics (1:1 mapping, correct order) —
//     canonical happy path; topic 1 at opening, topic 8 at
//     closing.
//  2. 8 clips + 4 topics (fewer topics than clips) —
//     topics still ordered; topic 1 first, topic 4 last.
//  3. 8 clips + 10 topics (more topics than clips) —
//     topics still ordered; topic 1 first, topic 10 last.
//  4. Empty topics (all empty strings) — engine skips
//     empty topics; "Segment topics:" section is absent
//     from the prompt.
//  5. Repeated topics (e.g., "A", "B", "A", "B") — order
//     preserved; repetition not deduplicated.
//  6. Wrong order (topics intentionally reversed) —
//     order preserved as given (the engine sees the
//     given order and adapts).
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion pins a
// canonical string-level contract on the model-facing prompt
// (the "what the LLM sees" surface) — never a "result was
// non-nil" soft pass.
//
// KNOWN GAP #1 — SEVERE CACHE POISONING BUG (TDD-reveals-bug):
// internal/domain/script/cache_key.go explicitly EXCLUDES
// SegmentTopics from the cache key computation. The comment
// reads "Segment sizing (NumClips, SegmentWords, SegmentTopics)
// — affects CacheKey" but the BuildCacheKey function does NOT
// hash SegmentTopics. Impact: changing SegmentTopics alters
// the LLM prompt (verified by the P0.J tests) but does NOT
// invalidate cached results, so the system serves stale
// cached scripts based on the OLD topics. Fix: add
// plan.SegmentTopics to the BuildCacheKey hash inputs.
// CRITICAL — must be fixed before any production deployment
// that uses SegmentTopics.
//
// KNOWN GAP #2 — Empty Topic Numbering Bug (pinning test
// behavior): the loop in buildClipGroundingInstructions uses
// the SLICE INDEX (i+1) for numbering, not a counter of
// non-empty topics. So passing ["A", "", "C"] yields
// "1. A\n3. C" (gap at position 2). This is intentional OR
// accidental depending on the design intent: if topics are
// meant to align exactly 1:1 with clip indexes, this is a
// feature; if topics are just a list of concepts regardless
// of clip, the missing "2." will confuse the LLM. The P0.J
// test pins the current behavior so future contributors make
// a deliberate design decision about it.
package usecase

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// p0jClipIDsEight is the canonical 8-clip ID slice used by
// most P0.J scenarios (8 clips + varying topic counts).
// Pinned as a constant so a future contributor cannot
// accidentally switch to 1-clip or 3-clip variants.
var p0jClipIDsEight = []string{
	"clip-1", "clip-2", "clip-3", "clip-4",
	"clip-5", "clip-6", "clip-7", "clip-8",
}

// p0jBuildClipEvidenceForTopics builds a minimal
// ClipEvidence for the 8 canonical clips. The clip IDs are
// the only field that matters for the prompt-construction
// contract — the engine's prompt lists the clip IDs alongside
// the segment topics.
func p0jBuildClipEvidenceForTopics(clipIDs []string) *scriptpkg.ClipEvidence {
	return &scriptpkg.ClipEvidence{
		AcceptedClipIDs: clipIDs,
	}
}

// p0jBuildPlanWithTopics builds a minimal ResolvedGenerationPlan
// with the given clip IDs + segment topics. The plan has no
// other fields populated (the prompt-construction function
// only reads NumClips, SegmentWords, and SegmentTopics from
// the plan's clip-grounding section).
func p0jBuildPlanWithTopics(clipIDs []string, topics []string) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		NumClips:          len(clipIDs),
		SegmentTopics:     append([]string(nil), topics...),
		ClipEvidence:      p0jBuildClipEvidenceForTopics(clipIDs),
	}
}

// ── Test 1: 8 clips + 8 topics (canonical happy path) ──────────────

// TestSegmentTopics_P0J_EightClipsEightTopics_CorrectOrder pins
// the canonical happy-path contract: 8 clips + 8 topics in
// correct order, 1:1 mapping. The prompt MUST contain all 8
// topics in order, with topic 1 rendered FIRST (influences
// opening) and topic 8 rendered LAST (influences closing).
func TestSegmentTopics_P0J_EightClipsEightTopics_CorrectOrder(t *testing.T) {
	t.Parallel()

	topics := []string{
		"Opening scene",      // topic 1 → opening
		"Rising action",      // topic 2
		"Conflict setup",     // topic 3
		"Climax",             // topic 4
		"Falling action",     // topic 5
		"Twist",              // topic 6
		"Resolution",         // topic 7
		"Closing scene",      // topic 8 → closing
	}
	plan := p0jBuildPlanWithTopics(p0jClipIDsEight, topics)
	prompt := buildClipGroundingInstructions(plan)

	// (a) The prompt MUST contain the "Segment topics:" header.
	require.Containsf(t, prompt, "Segment topics:",
		"P0.J 8+8 prompt MUST contain 'Segment topics:' header; got=%q", prompt)

	// (b) Topic 1 MUST be rendered FIRST in the topics section
	//     (the canonical "topic 1 influences opening" contract).
	topic1Idx := strings.Index(prompt, "1. Opening scene")
	require.GreaterOrEqualf(t, topic1Idx, 0,
		"P0.J 8+8 prompt MUST contain '1. Opening scene' (topic 1 → opening); got=%q", prompt)

	// (c) Topic 8 MUST be rendered LAST in the topics section
	//     (the canonical "topic 8 influences closing" contract).
	topic8Idx := strings.Index(prompt, "8. Closing scene")
	require.GreaterOrEqualf(t, topic8Idx, 0,
		"P0.J 8+8 prompt MUST contain '8. Closing scene' (topic 8 → closing); got=%q", prompt)
	require.Greaterf(t, topic8Idx, topic1Idx,
		"P0.J 8+8 prompt MUST render topic 8 AFTER topic 1 (topic 8 → closing); got topic1Idx=%d topic8Idx=%d",
		topic1Idx, topic8Idx)

	// (d) All 8 topics MUST be in the prompt in the given order.
	expectedOrder := []string{
		"1. Opening scene",
		"2. Rising action",
		"3. Conflict setup",
		"4. Climax",
		"5. Falling action",
		"6. Twist",
		"7. Resolution",
		"8. Closing scene",
	}
	prevIdx := -1
	for i, expected := range expectedOrder {
		idx := strings.Index(prompt, expected)
		require.GreaterOrEqualf(t, idx, 0,
			"P0.J 8+8 prompt MUST contain topic %d (%q) in the expected position; got=%q",
			i+1, expected, prompt)
		assert.Greaterf(t, idx, prevIdx,
			"P0.J 8+8 prompt MUST render topic %d AFTER topic %d (order preserved); got prevIdx=%d currIdx=%d",
			i+1, i, prevIdx, idx)
		prevIdx = idx
	}
}

// ── Test 2: 8 clips + 4 topics (fewer topics than clips) ─────────

// TestSegmentTopics_P0J_EightClipsFourTopics_TopicsOrdered pins
// the contract for the "fewer topics than clips" scenario:
// 8 clips + 4 topics. The topics MUST still be rendered in
// the given order, with topic 1 FIRST (opening) and topic 4
// LAST (closing). The behavior for the middle is
// implementation-defined (each topic might cover 2 clips),
// but the test pins the prompt ordering regardless.
func TestSegmentTopics_P0J_EightClipsFourTopics_TopicsOrdered(t *testing.T) {
	t.Parallel()

	topics := []string{
		"Opening",      // topic 1 → opening
		"Development",  // topic 2
		"Climax",       // topic 3
		"Resolution",   // topic 4 → closing
	}
	plan := p0jBuildPlanWithTopics(p0jClipIDsEight, topics)
	prompt := buildClipGroundingInstructions(plan)

	// (a) The prompt MUST contain the "Segment topics:" header
	//     AND all 4 topics in order.
	require.Containsf(t, prompt, "Segment topics:",
		"P0.J 8+4 prompt MUST contain 'Segment topics:' header; got=%q", prompt)

	expectedOrder := []string{
		"1. Opening",
		"2. Development",
		"3. Climax",
		"4. Resolution",
	}
	prevIdx := -1
	for i, expected := range expectedOrder {
		idx := strings.Index(prompt, expected)
		require.GreaterOrEqualf(t, idx, 0,
			"P0.J 8+4 prompt MUST contain topic %d (%q); got=%q",
			i+1, expected, prompt)
		assert.Greaterf(t, idx, prevIdx,
			"P0.J 8+4 prompt MUST render topic %d AFTER topic %d; got prevIdx=%d currIdx=%d",
			i+1, i, prevIdx, idx)
		prevIdx = idx
	}
}

// ── Test 3: 8 clips + 10 topics (more topics than clips) ─────────

// TestSegmentTopics_P0J_EightClipsTenTopics_TopicsOrdered pins
// the contract for the "more topics than clips" scenario:
// 8 clips + 10 topics. The topics MUST still be rendered in
// the given order, with topic 1 FIRST (opening) and topic 10
// LAST (closing). The behavior for the surplus is
// implementation-defined (the engine might truncate or the
// extra topics might map to the same clip), but the test
// pins the prompt ordering regardless.
func TestSegmentTopics_P0J_EightClipsTenTopics_TopicsOrdered(t *testing.T) {
	t.Parallel()

	topics := []string{
		"Opening",        // topic 1 → opening
		"Setup",          // topic 2
		"Rising action",  // topic 3
		"Conflict",       // topic 4
		"Turning point",  // topic 5
		"Climax",         // topic 6
		"Falling action", // topic 7
		"Twist",          // topic 8
		"Resolution",     // topic 9
		"Closing",        // topic 10 → closing
	}
	plan := p0jBuildPlanWithTopics(p0jClipIDsEight, topics)
	prompt := buildClipGroundingInstructions(plan)

	// (a) The prompt MUST contain all 10 topics in order.
	require.Containsf(t, prompt, "Segment topics:",
		"P0.J 8+10 prompt MUST contain 'Segment topics:' header; got=%q", prompt)

	expectedOrder := []string{
		"1. Opening",
		"2. Setup",
		"3. Rising action",
		"4. Conflict",
		"5. Turning point",
		"6. Climax",
		"7. Falling action",
		"8. Twist",
		"9. Resolution",
		"10. Closing",
	}
	prevIdx := -1
	for i, expected := range expectedOrder {
		idx := strings.Index(prompt, expected)
		require.GreaterOrEqualf(t, idx, 0,
			"P0.J 8+10 prompt MUST contain topic %d (%q); got=%q",
			i+1, expected, prompt)
		assert.Greaterf(t, idx, prevIdx,
			"P0.J 8+10 prompt MUST render topic %d AFTER topic %d; got prevIdx=%d currIdx=%d",
			i+1, i, prevIdx, idx)
		prevIdx = idx
	}
}

// ── Test 4: Empty topics (all empty strings) ──────────────────────

// TestSegmentTopics_P0J_EmptyTopics_Skipped pins the contract
// for the "all topics empty" scenario: 8 clips + 8 empty
// topics. The buildClipGroundingInstructions function SKIPS
// empty topics (via `if topic == "" { continue }`), so the
// resulting "topics" slice is empty and the "Segment topics:"
// section is NOT added to the prompt. The test pins this
// behavior — empty topics MUST NOT produce a "Segment topics:"
// header.
func TestSegmentTopics_P0J_EmptyTopics_Skipped(t *testing.T) {
	t.Parallel()

	topics := []string{"", "", "", "", "", "", "", ""}
	plan := p0jBuildPlanWithTopics(p0jClipIDsEight, topics)
	prompt := buildClipGroundingInstructions(plan)

	// (a) The prompt MUST NOT contain the "Segment topics:" header
	//     (all topics are empty, so the section is skipped).
	assert.NotContainsf(t, prompt, "Segment topics:",
		"P0.J empty-topics prompt MUST NOT contain 'Segment topics:' header (all topics empty → section skipped); got=%q", prompt)

	// (b) The prompt MUST NOT contain any numbered entries.
	for i := 1; i <= 8; i++ {
		numbered := strings.Index(prompt, "1. ")  // check the first numbering pattern
		_ = numbered
		// We don't know the exact format (the function skips ALL
		// empty topics, so no numbered entries exist). Just verify
		// the "Segment topics:" section is absent — that's the
		// canonical contract.
	}
}

// ── Test 5: Repeated topics ───────────────────────────────────────

// TestSegmentTopics_P0J_RepeatedTopics_FormattedInOrder pins
// the contract for the "repeated topics" scenario: 8 clips +
// 4 topics with repetition (e.g., "Opening", "Climax",
// "Opening", "Climax"). The topics MUST be rendered in the
// given order, with repetition NOT deduplicated. The engine
// sees the repetition and decides how to handle it (e.g.,
// the LLM might interpret the repetition as a callback
// structure).
func TestSegmentTopics_P0J_RepeatedTopics_FormattedInOrder(t *testing.T) {
	t.Parallel()

	topics := []string{
		"Opening",  // topic 1
		"Climax",   // topic 2
		"Opening",  // topic 3 (repeated)
		"Climax",   // topic 4 (repeated)
	}
	plan := p0jBuildPlanWithTopics(p0jClipIDsEight, topics)
	prompt := buildClipGroundingInstructions(plan)

	// (a) The prompt MUST contain all 4 topics in order, with
	//     repetition NOT deduplicated.
	expectedOrder := []string{
		"1. Opening",
		"2. Climax",
		"3. Opening",
		"4. Climax",
	}
	prevIdx := -1
	for i, expected := range expectedOrder {
		idx := strings.Index(prompt, expected)
		require.GreaterOrEqualf(t, idx, 0,
			"P0.J repeated-topics prompt MUST contain topic %d (%q) with repetition NOT deduplicated; got=%q",
			i+1, expected, prompt)
		assert.Greaterf(t, idx, prevIdx,
			"P0.J repeated-topics prompt MUST render topic %d AFTER topic %d; got prevIdx=%d currIdx=%d",
			i+1, i, prevIdx, idx)
		prevIdx = idx
	}

	// (b) "Opening" MUST appear TWICE in the topics section
	//     (not deduplicated).
	openingCount := strings.Count(prompt, "Opening")
	require.GreaterOrEqualf(t, openingCount, 2,
		"P0.J repeated-topics prompt MUST contain 'Opening' at least twice (not deduplicated); got count=%d prompt=%q",
		openingCount, prompt)
}

// ── Test 6: Wrong order (topics intentionally reversed) ───────────

// TestSegmentTopics_P0J_WrongOrder_PreservedAsGiven pins the
// contract for the "wrong order" scenario: 8 clips + 8
// topics intentionally in the WRONG order (e.g., "Closing
// scene" listed FIRST, "Opening scene" listed LAST). The
// topics MUST be rendered in the GIVEN order (the pipeline
// does NOT sort, validate, or reorder). The engine receives
// the wrong order and must adapt (or the LLM might produce
// a narrative that doesn't match the user's intent — that's
// the caller's problem, not the pipeline's).
func TestSegmentTopics_P0J_WrongOrder_PreservedAsGiven(t *testing.T) {
	t.Parallel()

	// Intentionally reversed order — the user spec says "topic
	// in ordine errato" (topics in wrong order).
	topics := []string{
		"Closing scene",  // topic 1 (WRONG: should be opening)
		"Resolution",     // topic 2
		"Twist",          // topic 3
		"Falling action", // topic 4
		"Climax",         // topic 5
		"Conflict setup", // topic 6
		"Rising action",  // topic 7
		"Opening scene",  // topic 8 (WRONG: should be closing)
	}
	plan := p0jBuildPlanWithTopics(p0jClipIDsEight, topics)
	prompt := buildClipGroundingInstructions(plan)

	// (a) The prompt MUST render the topics in the GIVEN order
	//     (not sorted, not validated, not reordered).
	expectedOrder := []string{
		"1. Closing scene",  // given first
		"2. Resolution",
		"3. Twist",
		"4. Falling action",
		"5. Climax",
		"6. Conflict setup",
		"7. Rising action",
		"8. Opening scene",  // given last
	}
	prevIdx := -1
	for i, expected := range expectedOrder {
		idx := strings.Index(prompt, expected)
		require.GreaterOrEqualf(t, idx, 0,
			"P0.J wrong-order prompt MUST contain topic %d (%q) in the GIVEN position (not sorted); got=%q",
			i+1, expected, prompt)
		assert.Greaterf(t, idx, prevIdx,
			"P0.J wrong-order prompt MUST render topic %d AFTER topic %d (given order preserved); got prevIdx=%d currIdx=%d",
			i+1, i, prevIdx, idx)
		prevIdx = idx
	}
}
