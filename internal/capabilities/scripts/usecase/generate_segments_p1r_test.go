// Package scripts — generate_segments_p1r_test.go (PR-CS-1, FASE 8, DoD #1-#9).
//
// 12-test canonical P0j-style unit coverage of the ScriptSegment
// dimension, asserting each DoD axis at the boundary where:
//   - The engine's rendering-side functions (buildSegmentInstructions,
//     buildClipGroundingInstructions) emit deterministic contracts.
//   - The post-strip QA gate (SanitizeScriptOutput) enforces no-markup.
//   - The word-budget gate (effectiveTargetForBudgetWords) follows
//     the canonical fallback chain.
//   - The payload validator (ValidateEnvelope) rejects every
//     malformed-shape input.
//
// Each test is intentionally self-contained (its OWN plan literal
// with explicit Topic/SourceText/TargetWords) so a regression in any
// one rendering path surfaces as one failing case + one-line
// diagnosis. The tests parallel the FASE 1-7 commits but consolidate
// the DoD axes into a single P0j-style unit-coverage file; downstream
// FASE 10 (live E2E) hits the same contracts through the running
// server, not the unit test path.
//
// godlike/06 SSOT: this file IS the canonical P0.j-level unit
// coverage surface for ScriptSegment contracts in PR-CS-1. Do not
// duplicate these 12 cases elsewhere — FASE 4/5/6 test files pin
// the SAME contract at the helper-function layer (TestStrip_*,
// TestBudget_*, TestPayloadValidator_*); this file re-anchors
// them at the DoD-axis layer for operator audit + future refactor
// safety net.
package usecase

import (
	"fmt"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Helpers ─────────────────────────────────────────────────────────

// makePlanWithSegments builds a ResolvedGenerationPlan carrying the
// supplied segments. Other fields use canonical defaults so a regression
// in the helper itself surfaces loudly rather than masking a real DoD
// axis failure.
func makePlanWithSegments(segs []scriptpkg.ScriptSegment) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title:         "Unit Test Plan",
		Topic:         "unit test topic",
		Language:      "en",
		Tone:          "documentary",
		Model:         "llama3:8b",
		SourceKind:    "text",
		PromptVersion: "v1",
		PromptProfile: "default-v1",
		Segments:      segs,
	}
}

// uniqueToken returns a deterministic high-entropy token per index —
// used by TestSegments_NEWFormat_WithSourceText_EachSourceTextUsed
// to assert each segment's source_text is bound to the correct
// prompt block (a hallucinated duplicate-block would not match).
func uniqueToken(i int) string {
	return fmt.Sprintf("PM-d84c12-KS-S%d", i)
}

// ── DoD #1: no segments → no Branch-A markers ───────────────────────
// SegmentTopics has been removed. When Segments is absent, the prompt
// must contain no Branch-A markers.

func TestSegments_NoSegments_NoBranchAMarkers(t *testing.T) {
	t.Parallel()
	// No Segments, no clip evidence.
	// buildSegmentInstructions (Branch A) is gated on len(Segments)>0
	// so it returns ""; buildClipGroundingInstructions returns "" when
	// plan.HasClips() is false. The combined prompt is empty.
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:         "No Segments Plan",
		Topic:         "legacy topic",
		Language:      "en",
		Tone:          "documentary",
		Model:         "llama3:8b",
		SourceKind:    "text",
		PromptVersion: "v1",
		PromptProfile: "default-v1",
	}
	// Branch A is gated on len(Segments)>0.
	if gotA := buildSegmentInstructions(plan); gotA != "" {
		t.Fatalf("Branch A (buildSegmentInstructions) MUST be gated on len(Segments)>0; got %q for Segments-absent plan", gotA)
	}
	// Combined prompt has no Branch-A markers.
	combined := buildSegmentInstructions(plan) + buildClipGroundingInstructions(plan)
	bannedLabels := []string{
		"SEGMENT 1", "SEGMENT 2", "SEGMENT 3",
		"Source text:", "Target words:",
	}
	for _, b := range bannedLabels {
		if strings.Contains(combined, b) {
			t.Errorf("legacy format MUST NOT emit %q; got:\n%s", b, combined)
		}
	}
}

// ── DoD #2: NEW format — each segment's source_text tied to right block ──

func TestSegments_NEWFormat_WithSourceText_EachSourceTextUsed(t *testing.T) {
	t.Parallel()
	srcs := []string{
		uniqueToken(0) + " alpha prose unique.",
		uniqueToken(1) + " beta prose unique.",
		uniqueToken(2) + " gamma prose unique.",
	}
	plan := makePlanWithSegments([]scriptpkg.ScriptSegment{
		{Topic: "intro", SourceText: srcs[0], TargetWords: 80},
		{Topic: "body", SourceText: srcs[1], TargetWords: 200},
		{Topic: "outro", SourceText: srcs[2], TargetWords: 60},
	})
	got := buildSegmentInstructions(plan)
	if got == "" {
		t.Fatal("Branch A (Segments with SourceText) returned empty")
	}
	// DoD #2: every Source text: block contains the unique token tied
	// to the matching segment. A test that fails shows cross-segment
	// contamination (a hallucinated copy-paste) — which is the DoD #2
	// anti-fabrication guard.
	for i, src := range srcs {
		if !strings.Contains(got, src) {
			t.Errorf("segment[%d].SourceText (%q) MUST appear in prompt tied to its block; got:\n%s", i, src, got)
		}
	}
	// Each unique token must appear EXACTLY ONCE (not duplicated by
	// a hallucinated cross-block copy).
	for i, src := range srcs {
		if c := strings.Count(got, src); c != 1 {
			t.Errorf("unique token for segment[%d] appears %d times (expected 1); cross-contamination or duplication; got count=%d", i, c, c)
		}
	}
}

// ── DoD #3 + #4: MIXED — SourceText optional, order preserved via relative markers ──

func TestSegments_MIXED_SourceTextOptionalOrderPreserved(t *testing.T) {
	t.Parallel()
	plan := makePlanWithSegments([]scriptpkg.ScriptSegment{
		{Topic: "intro", TargetWords: 80},                                        // no source_text → write from topic
		{Topic: "body", SourceText: "body source unique 7y3.", TargetWords: 200}, // has source_text
		{Topic: "outro", TargetWords: 60},                                        // no source_text
	})
	got := buildSegmentInstructions(plan)
	// DoD #3 + #4: relative ordering check (idx1<idx2<idx3).
	// Using relative offsets (idx2<idxBody<idx3) instead of exact-string
	// arithmetic so a future tweak to the rendering format (extra
	// newline, blank-line count, etc.) doesn't silently break the test.
	idx1 := strings.Index(got, "SEGMENT 1")
	idx2 := strings.Index(got, "SEGMENT 2")
	idx3 := strings.Index(got, "SEGMENT 3")
	idxBody := strings.Index(got, "body source unique 7y3.")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Fatalf("MIXED — missing one or more SEGMENT N markers; got:\n%s", got)
	}
	if !(idx1 < idx2 && idx2 < idx3) {
		t.Fatalf("DoD #3: MIXED order broken (SEGMENT 1 < 2 < 3 required); idx1=%d idx2=%d idx3=%d got:\n%s", idx1, idx2, idx3, got)
	}
	// DoD #3: body SourceText MUST be inside SEGMENT 2 block (relative
	// position: idx2 < idxBody < idx3).
	if !(idx2 < idxBody && idxBody < idx3) {
		t.Fatalf("DoD #3: body SourceText MUST be inside SEGMENT 2 block; idx2=%d idxBody=%d idx3=%d got:\n%s", idx2, idxBody, idx3, got)
	}
	// DoD #3: "Source text:\n" (with trailing newline) appears
	// EXACTLY ONCE — only segment[1] has source_text; absent segments
	// do NOT emit the label. We use the newline-suffixed substring
	// "Source text:\n" (not "Source text:") so the footer banned-
	// marker reference `(SEGMENT 1, Topic:, Source text:)` is NOT
	// counted — only the actual emission carries the trailing newline.
	if c := strings.Count(got, "Source text:\n"); c != 1 {
		t.Fatalf("DoD #3: MIXED MUST emit 'Source text:\\n' exactly once (only the segment WITH source_text); got %d occurrences:\n%s", c, got)
	}
}

// ── DoD #4: Order — 5 segments A,B,C,D,E preserved in render ──

func TestSegments_Order_PreservesInputOrder(t *testing.T) {
	t.Parallel()
	plan := makePlanWithSegments([]scriptpkg.ScriptSegment{
		{Topic: "A", TargetWords: 80},
		{Topic: "B", TargetWords: 100},
		{Topic: "C", TargetWords: 120},
		{Topic: "D", TargetWords: 140},
		{Topic: "E", TargetWords: 160},
	})
	got := buildSegmentInstructions(plan)
	// DoD #4: monotonic index for SEGMENT N markers.
	type marker struct {
		no     int
		idx    int
		letter string
	}
	markers := make([]marker, 0, 5)
	for i := 1; i <= 5; i++ {
		m := fmt.Sprintf("SEGMENT %d", i)
		idx := strings.Index(got, m)
		if idx < 0 {
			t.Fatalf("missing %s marker; got:\n%s", m, got)
		}
		markers = append(markers, marker{no: i, idx: idx, letter: string(rune('A' + i - 1))})
	}
	for i := 1; i < len(markers); i++ {
		if markers[i].idx <= markers[i-1].idx {
			t.Fatalf("DoD #4: order NOT preserved at index %d (idx[%d]=%d <= idx[%d]=%d); got:\n%s",
				i, i, markers[i].idx, i-1, markers[i-1].idx, got)
		}
	}
	// DoD #4: each marker is followed by the matching Topic letter
	// in the input order.
	for _, m := range markers {
		afterMarker := got[m.idx:]
		if !strings.Contains(afterMarker, "Topic: "+m.letter) {
			t.Errorf("DoD #4: SEGMENT %d MUST be followed by 'Topic: %s'; got:\n%s", m.no, m.letter, got)
		}
	}
}

// ── DoD #5: No invention — footer bans hallucinated names/dates ──

func TestSegments_NoInvention_NoHallucinatedTopic(t *testing.T) {
	t.Parallel()
	plan := makePlanWithSegments([]scriptpkg.ScriptSegment{
		{Topic: "Pacquiao vs Broner", TargetWords: 500},
	})
	got := buildSegmentInstructions(plan)
	// DoD #5 unit lock: the canonical footer MUST include the
	// anti-fabrication rule for names/dates. The phrasing is operator-
	// visible in dashboard audit so it's intact, not paraphrased.
	requiredFragments := []string{
		"Do not invent names",
		"Do not invent",
		"names, dates, scores",
	}
	for _, frag := range requiredFragments {
		if !strings.Contains(got, frag) {
			t.Errorf("DoD #5 anti-invention footer MUST contain %q; got:\n%s", frag, got)
		}
	}
	// Symmetric: the footer MUST ban markers (defends DoD #7
	// post-strip contract at the prompt side).
	if !strings.Contains(got, "Do not print segment titles") {
		t.Errorf("DoD #5 footer MUST ban segment-title markers for post-strip sanity; got:\n%s", got)
	}
}

// ── DoD #6 unit: budget fallback chain ──

func TestSegments_Budget_FallbackChain_OK(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sp   scriptpkg.ScriptSpec
		want int
	}{
		{
			name: "first_segment_target_wins",
			sp: scriptpkg.ScriptSpec{
				Segments:     []scriptpkg.ScriptSegment{{Topic: "x", TargetWords: 50}},
				SegmentWords: 200,
				TargetWords:  1000,
			},
			want: 50,
		},
		{
			name: "segment_words_when_no_segment_target",
			sp: scriptpkg.ScriptSpec{
				Segments:     []scriptpkg.ScriptSegment{{Topic: "x", TargetWords: 0}},
				SegmentWords: 200,
				TargetWords:  1000,
			},
			want: 200,
		},
		{
			name: "target_words_when_no_segment_target_or_segment_words",
			sp: scriptpkg.ScriptSpec{
				Segments:    []scriptpkg.ScriptSegment{{Topic: "x", TargetWords: 0}},
				TargetWords: 800,
			},
			want: 800,
		},
		{
			name: "default_80_when_no_targets_at_all",
			sp: scriptpkg.ScriptSpec{
				Segments: []scriptpkg.ScriptSegment{{Topic: "x", TargetWords: 0}},
			},
			want: 80,
		},
		{
			name: "first_segment_with_target_wins_over_segment_words",
			sp: scriptpkg.ScriptSpec{
				Segments: []scriptpkg.ScriptSegment{
					{Topic: "x", TargetWords: 0},
					{Topic: "y", TargetWords: 75},
				},
				SegmentWords: 200,
				TargetWords:  1000,
			},
			want: 75, // first non-zero TargetWords in the slice
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := makePlanWithSegments(tc.sp.Segments)
			plan.SegmentWords = tc.sp.SegmentWords
			plan.TargetWords = tc.sp.TargetWords
			got := effectiveTargetForBudgetWords(plan)
			if got != tc.want {
				t.Fatalf("effectiveTargetForBudgetWords want %d got %d (chain: per-seg > SegmentWords > TargetWords > 80)", tc.want, got)
			}
		})
	}
}

// ── DoD #7 unit: SanitizeScriptOutput strips non-prose artifacts ──

func TestSegments_OutputSanitizer_StripsArtifacts(t *testing.T) {
	t.Parallel()
	// Input mimics a "leaky" Gemma output that includes prompt-echo,
	// GIT-style markers, JSON literals, schema_version, specscene.
	dirty := "SEGMENT 1\nTopic: Intro\nTarget words: 80\n\n```\nschema_version: v1\nclip_id: abc\nspecscene_id: spec-intro-001\n```\n\nThe fight was held in Las Vegas on January 19, 2019. Pacquiao opened aggressively."
	got := SanitizeScriptOutput(dirty)
	// All banned tokens must be absent from the cleaned output.
	banned := []string{"SEGMENT 1\n", "Topic: Intro\n", "Target words: 80\n", "```", "schema_version", "clip_id", "specscene"}
	for _, b := range banned {
		if strings.Contains(got, b) {
			t.Errorf("DoD #7 strip MUST remove %q; got:\n%s", b, got)
		}
	}
	// Continuous prose line must survive untouched.
	if !strings.Contains(got, "The fight was held in Las Vegas on January 19, 2019. Pacquiao opened aggressively.") {
		t.Errorf("prose MUST survive sanitizer untouched; got:\n%s", got)
	}
}

// ── DoD #8.a: Payload validator rejects empty Segments list ──

func TestSegments_PayloadValidator_RejectsEmpty(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "empty-segs",
				Title: "Empty Segments",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords: 100,
					// Explicit empty list (`segments: []`) — distinct
					// from absent which silently defaults.
					Segments: []scriptpkg.ScriptSegment{},
				},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	if err == nil {
		t.Fatal("DoD #8.a MUST reject explicit `segments: []`; got nil")
	}
	var pie *scriptpkg.PlanInvalidError
	if !errAs(err, &pie) {
		t.Fatalf("DoD #8.a should surface as PlanInvalidError (semantic); got %T: %v", err, err)
	}
	if !containsAny(pie.Details, "segments must not be empty") {
		t.Fatalf("DoD #8.a detail MUST mention empty-segment rejection; got %v", pie.Details)
	}
}

// ── DoD #8.b: Payload validator rejects empty topic ──

func TestSegments_PayloadValidator_RejectsEmptyTopic(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "empty-topic",
				Title: "Empty Topic",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords: 100,
					Segments: []scriptpkg.ScriptSegment{
						{Topic: "intro"},
						{Topic: ""},    // blank topic — DO #8.b reject
						{Topic: "   "}, // whitespace-only also rejected
					},
				},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	if err == nil {
		t.Fatal("DoD #8.b MUST reject blank topic; got nil")
	}
	var pie *scriptpkg.PlanInvalidError
	if !errAs(err, &pie) {
		t.Fatalf("DoD #8.b should surface as PlanInvalidError; got %T: %v", err, err)
	}
	if !containsAny(pie.Details, "topic is required") {
		t.Fatalf("DoD #8.b detail MUST mention topic-required rejection; got %v", pie.Details)
	}
	// Operator clarity: the failing segment index must be in the detail.
	if !containsAny(pie.Details, "[1]") {
		t.Errorf("DoD #8.b detail MUST include the offending segment index; got %v", pie.Details)
	}
}

// ── DoD #8.d: Payload validator rejects negative TargetWords ──

func TestSegments_PayloadValidator_RejectsNegativeTargetWords(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "neg-target",
				Title: "Negative Target",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				// Anti-regression note for future-investigators:
				// this pins the FASE 6 conjunction
				// `target_words<=0 && len(Segments)==0` — NOT just
				// `target_words<0`. Do not simplify without
				// re-reading PR-CS-1 / FASE 6 commit message.
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: -5},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	if err == nil {
		t.Fatal("DoD #8.d MUST reject negative TargetWords (no Segments); got nil")
	}
	var pve *scriptpkg.PayloadValidationError
	if !errAs(err, &pve) {
		t.Fatalf("DoD #8.d should surface as PayloadValidationError (config-aware); got %T: %v", err, err)
	}
	if pve.Code != "INVALID_TARGET_WORDS" {
		t.Fatalf("DoD #8.d code MUST be INVALID_TARGET_WORDS; got %s", pve.Code)
	}
	if pve.Extra.ActualTargetWords != -5 {
		t.Errorf("DoD #8.d extra MUST surface actual value; got %v", pve.Extra.ActualTargetWords)
	}
}

// ── DoD #8.e: Payload validator rejects too many segments ──

func TestSegments_PayloadValidator_RejectsTooManySegments(t *testing.T) {
	t.Parallel()
	// Default cap is 50 (WithDefaults clamps MaxSegmentsCap<=0 to 50).
	v := NewDefaultPayloadValidator()
	segs := make([]scriptpkg.ScriptSegment, 51)
	for i := range segs {
		segs[i] = scriptpkg.ScriptSegment{Topic: fmt.Sprintf("topic_%d", i)}
	}
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "too-many",
				Title: "Too Many",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords: 100,
					Segments:    segs, // 51 > cap 50
				},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	if err == nil {
		t.Fatal("DoD #8.e MUST reject 51 segments > MaxSegmentsCap=50; got nil")
	}
	var pve *scriptpkg.PayloadValidationError
	if !errAs(err, &pve) {
		t.Fatalf("DoD #8.e should surface as PayloadValidationError; got %T: %v", err, err)
	}
	if pve.Code != "TOO_MANY_SEGMENTS" {
		t.Fatalf("DoD #8.e code MUST be TOO_MANY_SEGMENTS; got %s", pve.Code)
	}
	if pve.Extra.ActualSegments != 51 || pve.Extra.MaxSegmentsCap != 50 {
		t.Errorf("DoD #8.e extras MUST report actual_segments=51 max=50; got %v", pve.Extra)
	}
}

// ── Helpers (test-local): error extraction + slice contains ──

// errAs is a tiny wrapper around errors.As that lets each test keep
// its typed-error extraction on one line without importing errors
// at the test site. Supports the 2 typed errors produced by
// payload_validator + generation_validator (FASE 6).
func errAs(err error, target any) bool {
	if err == nil {
		return false
	}
	switch t := target.(type) {
	case **scriptpkg.PlanInvalidError:
		pie, ok := err.(*scriptpkg.PlanInvalidError)
		if !ok {
			return false
		}
		*t = pie
		return true
	case **scriptpkg.PayloadValidationError:
		pve, ok := err.(*scriptpkg.PayloadValidationError)
		if !ok {
			return false
		}
		*t = pve
		return true
	default:
		return false
	}
}

// containsAny returns true when needle appears in any element of haystack.
func containsAny(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// equalStringSlice is an order-preserving shallow equal-check.
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
