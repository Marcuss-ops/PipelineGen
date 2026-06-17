package scriptcore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ── PR4.1 — Scene-aware normalization markers preservation ────────────────

func TestNormalizeScriptByScenes_MarkersPreserved(t *testing.T) {
	// The scene-aware normalizer must keep every [Clip: id] marker in
	// the output, in the original order, even after expansion.
	script := "[Clip: clip-a]\nShort.\n\n[Clip: clip-b]\nAlso short."
	// gen is nil so the function falls back to "approve" without LLM calls.
	normalized, _, action, err := NormalizeScriptByScenes(context.Background(), nil, "en", "comedy", "gemma2:9b", "topic", script, "", 900, 0)
	if err != nil {
		t.Fatalf("NormalizeScriptByScenes: %v", err)
	}
	if action != "approve" {
		t.Errorf("action = %q, want approve (no LLM)", action)
	}
	if !strings.Contains(normalized, "[Clip: clip-a]") {
		t.Errorf("normalized output missing [Clip: clip-a]: %q", normalized)
	}
	if !strings.Contains(normalized, "[Clip: clip-b]") {
		t.Errorf("normalized output missing [Clip: clip-b]: %q", normalized)
	}
	// Order must be preserved
	if strings.Index(normalized, "clip-a") > strings.Index(normalized, "clip-b") {
		t.Errorf("marker order not preserved: %q", normalized)
	}
}

func TestNormalizeScriptByScenes_NoMarkersFallsBackToLegacy(t *testing.T) {
	// A script without [Clip: ...] markers should fall back to the
	// legacy NormalizeLength behaviour (which with nil gen returns the
	// input unchanged and reports "approve").
	script := "Just plain text with no markers at all."
	normalized, _, action, err := NormalizeScriptByScenes(context.Background(), nil, "en", "comedy", "gemma2:9b", "topic", script, "", 900, 0)
	if err != nil {
		t.Fatalf("NormalizeScriptByScenes: %v", err)
	}
	if action != "approve" {
		t.Errorf("action = %q, want approve", action)
	}
	if strings.TrimSpace(normalized) != strings.TrimSpace(script) {
		t.Errorf("unstructured script should be returned unchanged")
	}
}

func TestNormalizeScriptByScenes_EmptyScript(t *testing.T) {
	normalized, wc, action, err := NormalizeScriptByScenes(context.Background(), nil, "en", "comedy", "gemma2:9b", "topic", "", "", 900, 0)
	if err != nil {
		t.Fatalf("NormalizeScriptByScenes: %v", err)
	}
	if action != "empty" {
		t.Errorf("action = %q, want empty", action)
	}
	if wc != 0 {
		t.Errorf("wordCount = %d, want 0", wc)
	}
	if normalized != "" {
		t.Errorf("normalized = %q, want empty", normalized)
	}
}

// ── PR4.2 — OUTPUT CONTRACT section in the prompt ───────────────────────

func TestBuildSourceText_ContainsOutputContract(t *testing.T) {
	pack := &ClipSourcePack{
		Clips: []ClipEvidence{
			{ClipID: "clip-a", Title: "A", Summary: "summary a"},
			{ClipID: "clip-b", Title: "B", Summary: "summary b"},
		},
	}
	plan := &NarrativePlan{
		Title:        "Test",
		NarrativeArc: "rising",
		OrderedClips: []OrderedClip{
			{ClipID: "clip-a", Role: "hook", Reason: "open strong"},
			{ClipID: "clip-b", Role: "callback", Reason: "close the loop"},
		},
	}
	opts := &ClipGenerationOptions{
		Language:    "en",
		Tone:        "comedy",
		Title:       "Test",
		Type:        "compilation",
		TargetWords: 400,
	}
	b := &ClipSourceBuilder{}
	src := b.BuildSourceText(pack, plan, opts)

	// The OUTPUT CONTRACT section must be present, before the CLIP EVIDENCE.
	if !strings.Contains(src, "=== OUTPUT CONTRACT ===") {
		t.Errorf("prompt missing OUTPUT CONTRACT section")
	}
	if !strings.Contains(src, "[Clip: clip_id]") {
		t.Errorf("OUTPUT CONTRACT missing the literal [Clip: clip_id] format example")
	}
	// Order: STRUCTURAL STRATEGY → OUTPUT CONTRACT → NON-NEGOTIABLE RULES
	idxStrategy := strings.Index(src, "=== STRUCTURAL STRATEGY ===")
	idxContract := strings.Index(src, "=== OUTPUT CONTRACT ===")
	idxRules := strings.Index(src, "=== NON-NEGOTIABLE RULES ===")
	idxEvidence := strings.Index(src, "=== CLIP EVIDENCE ===")
	if !(idxStrategy < idxContract && idxContract < idxRules && idxRules < idxEvidence) {
		t.Errorf("section order wrong: strategy=%d contract=%d rules=%d evidence=%d",
			idxStrategy, idxContract, idxRules, idxEvidence)
	}
}

func TestBuildSourceText_OutputContractMentionsNarrationWhenAllowed(t *testing.T) {
	// Story mode allows narration scenes → the OUTPUT CONTRACT should
	// mention [Narration: opening] / [Narration: closing].
	pack := &ClipSourcePack{
		Clips: []ClipEvidence{{ClipID: "clip-a", Title: "A", Summary: "x"}},
	}
	plan := &NarrativePlan{
		Title:        "Story",
		NarrativeArc: "arc",
		OrderedClips: []OrderedClip{{ClipID: "clip-a", Role: "inciting incident", Reason: "x"}},
	}
	opts := &ClipGenerationOptions{
		Language: "en", Tone: "narrative", Type: "story", TargetWords: 300,
	}
	b := &ClipSourceBuilder{}
	src := b.BuildSourceText(pack, plan, opts)
	if !strings.Contains(src, "[Narration: opening]") {
		t.Errorf("story mode prompt should mention [Narration: opening] in OUTPUT CONTRACT")
	}
}

// ── PR4.3 — OrderedClip enrichment ──────────────────────────────────────

func TestOrderedClip_JSONRoundTrip(t *testing.T) {
	// The new fields (Purpose, ComedicAngle, TargetWords) must round-trip
	// through JSON so the planner can return them and the writer can
	// read them.
	original := OrderedClip{
		ClipID:       "clip-a",
		Role:         "hook",
		Reason:       "open strong",
		Purpose:      "Set up the compilation theme",
		ComedicAngle: "The visual gag is the punchline",
		TargetWords:  150,
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"purpose":"Set up the compilation theme"`) {
		t.Errorf("JSON missing purpose: %s", raw)
	}
	if !strings.Contains(string(raw), `"comedic_angle":"The visual gag is the punchline"`) {
		t.Errorf("JSON missing comedic_angle: %s", raw)
	}
	if !strings.Contains(string(raw), `"target_words":150`) {
		t.Errorf("JSON missing target_words: %s", raw)
	}

	var decoded OrderedClip
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Purpose != original.Purpose {
		t.Errorf("purpose round-trip: got %q, want %q", decoded.Purpose, original.Purpose)
	}
	if decoded.ComedicAngle != original.ComedicAngle {
		t.Errorf("comedic_angle round-trip: got %q, want %q", decoded.ComedicAngle, original.ComedicAngle)
	}
	if decoded.TargetWords != original.TargetWords {
		t.Errorf("target_words round-trip: got %d, want %d", decoded.TargetWords, original.TargetWords)
	}
}

func TestOrderedClip_OldFormatStillParses(t *testing.T) {
	// Plans written before PR4 do not have the new fields. The new struct
	// must accept them as zero values (omitempty means they're dropped
	// on marshal, so old plans just produce an OrderedClip with empty
	// Purpose/ComedicAngle/TargetWords).
	oldJSON := `{"clip_id":"clip-a","role":"hook","reason":"old format"}`
	var oc OrderedClip
	if err := json.Unmarshal([]byte(oldJSON), &oc); err != nil {
		t.Fatalf("Unmarshal old format: %v", err)
	}
	if oc.ClipID != "clip-a" || oc.Role != "hook" || oc.Reason != "old format" {
		t.Errorf("old format fields wrong: %+v", oc)
	}
	if oc.Purpose != "" || oc.ComedicAngle != "" || oc.TargetWords != 0 {
		t.Errorf("old format should leave new fields zero: %+v", oc)
	}
}

func TestFallbackNarrativePlan_PopulatesTargetWords(t *testing.T) {
	// When the planner LLM fails, fallbackNarrativePlan should distribute
	// the target word count across clips so the writer has a per-scene
	// budget even without planner enrichment.
	pack := &ClipSourcePack{
		Clips: []ClipEvidence{
			{ClipID: "clip-a"}, {ClipID: "clip-b"}, {ClipID: "clip-c"}, {ClipID: "clip-d"},
		},
	}
	opts := &ClipGenerationOptions{
		Title:       "Test",
		TargetWords: 400, // 100 per scene
	}
	strategy := ResolveStrategy("compilation")
	plan := fallbackNarrativePlan(pack, opts, strategy)
	if plan == nil {
		t.Fatal("fallbackNarrativePlan returned nil")
	}
	if len(plan.OrderedClips) != 4 {
		t.Fatalf("plan has %d ordered clips, want 4", len(plan.OrderedClips))
	}
	// 400 words / 4 clips = 100 per clip, +20% on opening/closing → 120.
	// Verify the per-clip budget distribution, not just the sum, so the
	// +20% bump contract is locked in.
	wantPerClip := []int{120, 100, 100, 120}
	totalTarget := 0
	for i, oc := range plan.OrderedClips {
		if oc.TargetWords != wantPerClip[i] {
			t.Errorf("clip[%d] %s TargetWords = %d, want %d", i, oc.ClipID, oc.TargetWords, wantPerClip[i])
		}
		totalTarget += oc.TargetWords
	}
	// Total should be at least the original target; opening/closing bump
	// means it can be slightly higher.
	if totalTarget < 400 {
		t.Errorf("sum of fallback TargetWords = %d, want >= 400", totalTarget)
	}
}

// ── PR4.1 — sceneBudget helper ───────────────────────────────────────────

func TestSceneBudget_OpeningAndClosingBump(t *testing.T) {
	// 4 clip scenes, per-clip base = 100. Opening (idx 0) and closing
	// (idx 3) get the 1.2x bump, middle two stay at 100.
	cases := []struct {
		idx, total int
		want       int
	}{
		{0, 4, 120}, // opening
		{1, 4, 100}, // middle
		{2, 4, 100}, // middle
		{3, 4, 120}, // closing
	}
	for _, c := range cases {
		got := sceneBudget(100, c.idx, c.total)
		if got != c.want {
			t.Errorf("sceneBudget(100, %d, %d) = %d, want %d", c.idx, c.total, got, c.want)
		}
	}
}

func TestSceneBudget_SingleClipGetsBump(t *testing.T) {
	// With 1 clip, that clip is both opening and closing, so it gets
	// the 1.2x bump (not double-bumped).
	got := sceneBudget(200, 0, 1)
	if got != 240 {
		t.Errorf("sceneBudget(200, 0, 1) = %d, want 240", got)
	}
}

func TestSceneBudget_ZeroClipsReturnsBase(t *testing.T) {
	// Degenerate case: no clip scenes. The caller should not reach
	// this branch in practice (it falls back to perScene = targetWords),
	// but the helper must not divide by zero.
	got := sceneBudget(100, 0, 0)
	if got != 100 {
		t.Errorf("sceneBudget(100, 0, 0) = %d, want 100 (no division-by-zero)", got)
	}
}

// ── PR4.1 — assembleScene helper ─────────────────────────────────────────

func TestAssembleScene(t *testing.T) {
	cases := []struct {
		name string
		in   ParsedScene
		body string
		want string
	}{
		{
			name: "preamble: body returned as-is, no marker prefix",
			in:   ParsedScene{Kind: "preamble", Text: "Some text."},
			body: "Some text.",
			want: "Some text.",
		},
		{
			name: "clip: marker + body",
			in:   ParsedScene{Kind: "clip", Marker: "[Clip: clip-a]", Text: "Body."},
			body: "Body.",
			want: "[Clip: clip-a]\nBody.",
		},
		{
			name: "narration: marker + body",
			in:   ParsedScene{Kind: "narration", Marker: "[Narration: opening]", Text: "Body."},
			body: "Body.",
			want: "[Narration: opening]\nBody.",
		},
		{
			name: "empty marker: body returned as-is",
			in:   ParsedScene{Kind: "clip", Marker: "", Text: "Body."},
			body: "Body.",
			want: "Body.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := assembleScene(c.in, c.body); got != c.want {
				t.Errorf("assembleScene = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildNarrativePlanPrompt_AsksForEnrichment(t *testing.T) {
	// The planner user prompt must ask for purpose, comedic_angle and
	// target_words per clip. Otherwise the LLM won't return them and
	// the writer can't use them.
	pack := &ClipSourcePack{
		Clips: []ClipEvidence{{ClipID: "clip-a", Title: "A", Summary: "x"}},
	}
	opts := &ClipGenerationOptions{Language: "en", Tone: "comedy", TargetWords: 200}
	strategy := ResolveStrategy("compilation")
	prompt := buildNarrativePlanPrompt(pack, opts, strategy)

	if !strings.Contains(prompt, "PURPOSE") {
		t.Errorf("planner prompt missing PURPOSE field request")
	}
	if !strings.Contains(prompt, "COMEDIC_ANGLE") && !strings.Contains(prompt, "comedic_angle") {
		t.Errorf("planner prompt missing comedic_angle / narrative_angle field request")
	}
	if !strings.Contains(prompt, "TARGET_WORDS") {
		t.Errorf("planner prompt missing TARGET_WORDS field request")
	}
}

// ── PR4 — version bump ──────────────────────────────────────────────────

func TestPlannerPromptVersionBumped(t *testing.T) {
	// PR4 changes the planner prompt + OrderedClip shape, so the
	// fingerprint must use a new version. If this assertion ever fails
	// the cached memory gate will return stale plans.
	if PlannerPromptVersion != "v2" {
		t.Errorf("PlannerPromptVersion = %q, want v2 (PR4 bumped it)", PlannerPromptVersion)
	}
}
