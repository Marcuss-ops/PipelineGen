// Package scripts — output_sanitizer_test.go (PR-CS-1, FASE 4, DoD #7).
//
// Pins SanitizeScriptOutput behaviour against the user-spec test
// list. The tests are deliberately narrow (single-artifact cases)
// so a regression surfaces with one failing case and a one-line
// diagnosis — instead of one mega-test where every regression looks
// the same.
//
// User-spec test list (10):
//  1. TestStrip_SEGMENT_N markers
//  2. TestStrip_TopicColonLine
//  3. TestStrip_SourceTextColonLine
//  4. TestStrip_clip_id
//  5. TestStrip_accepted_clip_ids
//  6. TestStrip_schema_version
//  7. TestStrip_specscene
//  8. TestStrip_MarkdownFence
//  9. TestStrip_Idempotent
//
// 10. TestStrip_PreservesContinuousText
//
// Plus 2 bonus pinning the collapseBlankLines + empty-input
// invariants (cheap, prevents regressions in steps 6 / 1 of the
// user-spec behaviour list).
package usecase

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"
	"testing"
)

// ── 1. SEGMENT N markers ───────────────────────────────────────────────

func TestStrip_SEGMENT_N(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "SEGMENT 1 capital",
			input: "SEGMENT 1\nPacquiao opened aggressively in round 1.",
			want:  "Pacquiao opened aggressively in round 1.",
		},
		{
			name:  "Segment 3 lowercase-cap",
			input: "Segment 3\nHe won by unanimous decision.",
			want:  "He won by unanimous decision.",
		},
		{
			name:  "SEGMENT no digits",
			input: "SEGMENT\nFinal bell rang at 2:30.",
			want:  "Final bell rang at 2:30.",
		},
		{
			name:  "leading hash prefix",
			input: "# SEGMENT 2\nBroner kept his guard up.",
			want:  "Broner kept his guard up.",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeScriptOutput(tc.input)
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// ── 2. Topic: directive line ──────────────────────────────────────────

func TestStrip_TopicColonLine(t *testing.T) {
	t.Parallel()
	input := "Topic: Introduzione\nPacquiao opened aggressively."
	got := SanitizeScriptOutput(input)
	want := "Pacquiao opened aggressively."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	// Defence: the directive label MUST NOT leak through even as
	// a substring of the preserved prose.
	if strings.Contains(got, "Topic:") {
		t.Fatalf("Topic: label leaked into prose: %q", got)
	}
}

// ── 3. Source text: directive line ────────────────────────────────────

func TestStrip_SourceTextColonLine(t *testing.T) {
	t.Parallel()
	input := "Source text: Pacquiao opened aggressively.\n\nHe landed the jab at 1:12 of round 1."
	got := SanitizeScriptOutput(input)
	want := "He landed the jab at 1:12 of round 1."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if strings.Contains(got, "Source text:") {
		t.Fatalf("Source text: label leaked into prose: %q", got)
	}
}

// ── 4. clip_id: JSON key line ─────────────────────────────────────────

func TestStrip_clip_id(t *testing.T) {
	t.Parallel()
	input := "Pacquiao opened aggressively.\nclip_id: abc123\nHe landed the jab."
	got := SanitizeScriptOutput(input)
	want := "Pacquiao opened aggressively.\nHe landed the jab."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if strings.Contains(got, "clip_id:") {
		t.Fatalf("clip_id: key leaked: %q", got)
	}
}

// ── 5. accepted_clip_ids anywhere in a line ───────────────────────────

func TestStrip_accepted_clip_ids(t *testing.T) {
	t.Parallel()
	input := "He kept his guard up.\naccepted_clip_ids: [\"clip-1\", \"clip-2\"]\nHe landed the jab."
	got := SanitizeScriptOutput(input)
	want := "He kept his guard up.\nHe landed the jab."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if strings.Contains(got, "accepted_clip_ids") {
		t.Fatalf("accepted_clip_ids token leaked: %q", got)
	}
}

// ── 6. schema_version substring on a line ────────────────────────────

func TestStrip_schema_version(t *testing.T) {
	t.Parallel()
	input := "The fight was historic.\nschema_version: asset.script.v1\nIt went 12 rounds."
	got := SanitizeScriptOutput(input)
	want := "The fight was historic.\nIt went 12 rounds."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if strings.Contains(got, "schema_version") {
		t.Fatalf("schema_version token leaked: %q", got)
	}
}

// ── 7. specscene substring on a line ──────────────────────────────────

func TestStrip_specscene(t *testing.T) {
	t.Parallel()
	input := "The bell rang at 2:30.\nspecscene_id: spec-intro-001\nScorecards read 116-110."
	got := SanitizeScriptOutput(input)
	want := "The bell rang at 2:30.\nScorecards read 116-110."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if strings.Contains(got, "specscene") {
		t.Fatalf("specscene token leaked: %q", got)
	}
}

// ── 8. Markdown code fences ───────────────────────────────────────────

func TestStrip_MarkdownFence(t *testing.T) {
	t.Parallel()
	input := "```\nThe fight was held in Las Vegas.\n```"
	got := SanitizeScriptOutput(input)
	want := "The fight was held in Las Vegas."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	// 4-backtick fence also drops (regex >=3 backticks).
	input4 := "````\nThe bell rang at 2:30.\n````"
	got4 := SanitizeScriptOutput(input4)
	if got4 != "The bell rang at 2:30." {
		t.Fatalf("4-backtick fence trim: want prose only, got %q", got4)
	}
}

// ── 9. Idempotency ───────────────────────────────────────────────────

func TestStrip_Idempotent(t *testing.T) {
	t.Parallel()
	input := "Pacquiao opened aggressively.\n\nHe landed the jab at 1:12 of round 1."
	once := SanitizeScriptOutput(input)
	twice := SanitizeScriptOutput(once)
	if once != twice {
		t.Fatalf("sanitizer is not idempotent: once=%q twice=%q", once, twice)
	}
	// Run twice on a heavily-bleeding input to lock idempotency
	// even under repeated application (cache-write path runs it,
	// cache-hit then re-runs it on replay).
	dirty := "SEGMENT 1\nTopic: Intro\n```\nschema_version: v1\nclip_id: x\n```\nReal prose line."
	clean := SanitizeScriptOutput(dirty)
	if SanitizeScriptOutput(clean) != clean {
		t.Fatalf("dirty->clean->clean MUST be invariant; clean=%q", clean)
	}
}

// ── 10. Preserves continuous prose ───────────────────────────────────

func TestStrip_PreservesContinuousText(t *testing.T) {
	t.Parallel()
	prose := "In the main event, Manny Pacquiao faced Adrien Broner at the MGM Grand in Las Vegas on January 19, 2019. Pacquiao opened aggressively, scoring with the jab in round 1, and secured a unanimous decision victory with scorecards of 116-110, 116-110, and 117-109."
	got := SanitizeScriptOutput(prose)
	if got != prose {
		t.Fatalf("clean prose MUST flow through unchanged\nwant %q\ngot  %q", prose, got)
	}
}

// ── 11. Bonus: collapses 3+ newlines to 1 blank ──────────────────────
// (Cheap invariant the user spec calls out as step 6 — pinned to
// prevent regressions in collapseBlankLines.)

func TestStrip_CollapsesExcessBlankLines(t *testing.T) {
	t.Parallel()
	input := "Line A.\n\n\n\n\nLine B."
	got := SanitizeScriptOutput(input)
	want := "Line A.\n\nLine B."
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

// ── 12. Bonus: empty / whitespace-only input ─────────────────────────

func TestStrip_EmptyInput(t *testing.T) {
	t.Parallel()
	cases := []string{"", "   ", "\n\n\n", "\t\n  \n"}
	for _, c := range cases {
		if got := SanitizeScriptOutput(c); got != "" {
			t.Fatalf("empty-class input MUST return \"\", got %q for input %q", got, c)
		}
	}
}
