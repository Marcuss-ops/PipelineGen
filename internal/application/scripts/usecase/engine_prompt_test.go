// Package scripts — engine_prompt_test.go pins the PR-CS-1 FASE 3
// contracts for buildSegmentInstructions (Branch A). The legacy
// Branch B (SegmentTopics in buildClipGroundingInstructions) is
// already covered by TestEngineGenerate_AppendsClipGroundingInstructions
// in engine_test.go; this file is the focal lock for the new path.
//
// USER-SPEC INVARIANTS pinned here:
//
//  1. The function returns "" when plan is nil or len(Segments)==0
//     (Branch A inactive → no prompt pollution for callers that don't
//     opt into the new payload).
//
//  2. Each segment produces EXACTLY one block with header
//     "SEGMENT {i+1}\nTopic: {Topic}\nTarget words: {N}".
//     Trailing newline logic varies between Source-text-present and
//     Source-text-absent cases; the contract enforces "Topic:"
//     immediately followed by "Target words: N" on the next line.
//
//  3. target_words fallback chain is canonical and deterministic:
//     per-segment TargetWords > 0 wins, else plan.SegmentWords,
//     else plan.TargetWords, else default 80.
//
//  4. Empty / whitespace-only source_text DOES NOT emit the
//     "Source text:" line (DoD rule — must not pollute prompt with
//     a label that has no follow-up content).
//
//  5. Blank line separator between segments (DoD rule — visual break
//     so Gemma parses each block as a distinct directive).
//
//  6. Footer canonical content: continuous narrative instruction +
//     DoD-driven anti-fabrication rules + anti-marker rules.
//     Footer is emitted exactly once.
package usecase

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Branch A inactive ────────────────────────────────────────────────────

func TestEnginePrompt_BuildSegmentInstructions_NilPlan(t *testing.T) {
	t.Parallel()
	if got := buildSegmentInstructions(nil); got != "" {
		t.Fatalf("nil plan MUST return empty, got %q", got)
	}
}

func TestEnginePrompt_BuildSegmentInstructions_NoSegments_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	plan := &scriptpkg.ResolvedGenerationPlan{}
	if got := buildSegmentInstructions(plan); got != "" {
		t.Fatalf("empty Segments MUST return empty, got %q", got)
	}
}

// ── Single segment, no source_text ───────────────────────────────────────

func TestEnginePrompt_BuildSegmentInstructions_SingleSegment_NoSourceText_OmitsSourceLine(t *testing.T) {
	t.Parallel()
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{
			{Topic: "Introduzione", TargetWords: 80},
		},
	}
	got := buildSegmentInstructions(plan)
	mustContain(t, got, []string{
		"SEGMENT 1",
		"Topic: Introduzione",
		"Target words: 80",
		"Write one continuous narrative",
	})
	// Footer legitimately bans the literal "Source text:" token
	// (see "Do not print segment titles (SEGMENT 1, Topic:,
	// Source text:) in the output"). Scope the assertion to the
	// SEGMENT block only, before the footer.
	mustNotContain(t, blockOnly(t, got), []string{
		"Source text:",
	})
}

// ── Single segment with source_text ─────────────────────────────────────

func TestEnginePrompt_BuildSegmentInstructions_SingleSegment_WithSourceText_EmitsBlock(t *testing.T) {
	t.Parallel()
	srcText := "Pacquiao opened aggressively, scoring with the jab in round 1."
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{
			{Topic: "Introduzione", SourceText: srcText, TargetWords: 80},
		},
	}
	got := buildSegmentInstructions(plan)
	mustContain(t, got, []string{
		"SEGMENT 1",
		"Topic: Introduzione",
		"Target words: 80",
		"Source text:",
		srcText,
	})
}

// ── Two segments: order + blank-line separator ──────────────────────────

func TestEnginePrompt_BuildSegmentInstructions_TwoSegments_PreservesOrderAndBlankLine(t *testing.T) {
	t.Parallel()
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{
			{Topic: "Introduzione", TargetWords: 80},
			{Topic: "Conclusione", TargetWords: 100},
		},
	}
	got := buildSegmentInstructions(plan)
	if !strings.Contains(got, "SEGMENT 1") {
		t.Errorf("expected SEGMENT 1 marker, got %q", got)
	}
	if !strings.Contains(got, "SEGMENT 2") {
		t.Errorf("expected SEGMENT 2 marker, got %q", got)
	}
	idx1 := strings.Index(got, "SEGMENT 1")
	idx2 := strings.Index(got, "SEGMENT 2")
	if idx1 < 0 || idx2 < 0 || idx1 >= idx2 {
		t.Errorf("SEGMENT 1 MUST come before SEGMENT 2 (idx1=%d idx2=%d), got %q", idx1, idx2, got)
	}
	// DoD #1: "Tra un segmento e l'altro una riga vuota" — verify
	// blank line separator between segments.
	if !strings.Contains(got, "Target words: 80\n\nSEGMENT 2") {
		t.Errorf("blank-line separator MUST appear between segments, got %q", got)
	}
	if !strings.Contains(got, "Target words: 100") {
		t.Errorf("expected Target words: 100 for second segment, got %q", got)
	}
}

// ── Whitespace-only source_text omits the Source-text line ──────────────

func TestEnginePrompt_BuildSegmentInstructions_WhitespaceSourceText_OmitsSourceLine(t *testing.T) {
	t.Parallel()
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{
			{Topic: "Introduzione", SourceText: "   \n\t", TargetWords: 80},
		},
	}
	got := buildSegmentInstructions(plan)
	// Footer legitimately bans the literal "Source text:" token
	// as a label example; scope the assertion to the SEGMENT block
	// only via the blockOnly helper.
	if strings.Contains(blockOnly(t, got), "Source text:") {
		t.Errorf("whitespace-only source_text MUST omit Source text: line, got block:\n%s", blockOnly(t, got))
	}
}

// ── target_words fallback chain (DoD #6 mirror) ──────────────────────────

func TestEnginePrompt_BuildSegmentInstructions_TargetWordsFallbackChain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		segTarget  int
		planSeg    int
		planTarget int
		wantTarget int
	}{
		{"seg_target_preferred", 50, 200, 1000, 50},
		{"plan_segment_words_fallback", 0, 200, 1000, 200},
		{"plan_target_words_fallback", 0, 0, 800, 800},
		{"default_floor_80", 0, 0, 0, 80},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := &scriptpkg.ResolvedGenerationPlan{
				SegmentWords: tc.planSeg,
				TargetWords:  tc.planTarget,
				Segments: []scriptpkg.ScriptSegment{
					{Topic: "X", TargetWords: tc.segTarget},
				},
			}
			got := buildSegmentInstructions(plan)
			want := "Target words: " + itoaSimple(tc.wantTarget)
			if !strings.Contains(got, want) {
				t.Errorf("expected %q in prompt, got:\n%s", want, got)
			}
		})
	}
}

// ── Footer canonical contents ───────────────────────────────────────────

func TestEnginePrompt_BuildSegmentInstructions_FooterContainsDoDRules(t *testing.T) {
	t.Parallel()
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{{Topic: "X", TargetWords: 80}},
	}
	got := buildSegmentInstructions(plan)
	// Footer MUST be emitted exactly once: continuous narrative
	// instruction + DoD anti-fabrication + anti-marker rule.
	mustContain(t, got, []string{
		"Write one continuous narrative.",
		"Follow the segment order strictly",
		"Do not skip, merge, or reorder topics",
		"Each segment must treat exclusively the subject named in its Topic",
		"Because this request declares one single-scene segment, write between 180 and 260 words",
		"Do not invent names, dates, scores, results, or events",
		"Do not print segment titles",
		"Do not include markers like clip_id, accepted_clip_ids, JSON, Markdown code fences, schema_version, or specscene",
		"Output only the script text.",
	})
	// Footer MUST NOT repeat interior block directives (no duplication).
	if strings.Count(got, "Write one continuous narrative") > 1 {
		t.Errorf("footer MUST be emitted exactly once, got %q", got)
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────

// blockOnly extracts the per-segment block portion of the prompt,
// before the canonical footer marker, so that narrow "MUST NOT
// contain" assertions don't accidentally match the footer
// (which legitimately bans "Source text:" as a labelled token — see
// "Do not print segment titles (SEGMENT 1, Topic:, Source text:) in
// the output"). The footer marker is the literal
// "\n\nWrite one continuous narrative" emitted exactly once.
func blockOnly(t *testing.T, full string) string {
	t.Helper()
	block, _, ok := strings.Cut(full, "\n\nWrite one continuous narrative")
	if !ok {
		t.Fatalf("prompt did not contain the canonical footer marker; got:\n%s", full)
	}
	return block
}

func mustContain(t *testing.T, haystack string, needles []string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("expected to contain %q, got:\n%s", n, haystack)
		}
	}
}

func mustNotContain(t *testing.T, haystack string, needles []string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			t.Errorf("MUST NOT contain %q, got:\n%s", n, haystack)
		}
	}
}
