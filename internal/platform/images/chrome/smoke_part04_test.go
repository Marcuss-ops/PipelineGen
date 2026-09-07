package chrome

import (
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"strings"
	"testing"
)

// ── P1.2 (July 2026): Direct imggeneration.ComposePrompt unit test ────────────────────
//
// Unit-level pinning of the contract documented in
// internal/capabilities/images/generation/prompt.go. Asserts:
//   - empty style + empty negative → composed == raw (no mutation)
//   - any null style → omit `[style: ...]`
//   - any null negative → omit `[negative: ...]`
//   - multi-word negatives: `,` → `;` (no truncation)
//   - WasCompressed is HARDCODED false (P1.2 policy)
//   - composed_len = original_len + len(style_affix) + len(negative_affix)
//   - no `…` Unicode ellipsis, no `...` ASCII ellipsis inside the prompt
//   - 400-char prompt arrives whole (no first-period split)
func TestPromptComposer_DirectCall_FormatContract(t *testing.T) {
	// (a) No style, no negative: composed MUST equal raw (no mutation).
	r := imggeneration.ComposePrompt("a peaceful valley at dawn", "", "")
	if r.Composed != "a peaceful valley at dawn" {
		t.Fatalf("compose (a): empty affixes; want unchanged prompt; got %q", r.Composed)
	}
	if r.WasCompressed {
		t.Fatal("compose (a): WasCompressed MUST be false (P1.2 policy); got true")
	}
	if r.StyleAffix != "" || r.NegativeAffix != "" {
		t.Fatalf("compose (a): empty affixes; want StyleAffix=NegativeAffix=\"\"; got %q / %q",
			r.StyleAffix, r.NegativeAffix)
	}
	if r.ComposedLen != r.OriginalLen {
		t.Fatalf("compose (a): empty affixes; want ComposedLen==OriginalLen; got %d vs %d",
			r.ComposedLen, r.OriginalLen)
	}

	// (b) Style only: composed = prompt + ` [style: X]`.
	r = imggeneration.ComposePrompt("a starlit desert at night", "cinematic", "")
	if !strings.HasPrefix(r.Composed, "a starlit desert at night") {
		t.Fatalf("compose (b): composed must START with raw prompt; got %q", r.Composed)
	}
	if !strings.Contains(r.Composed, "[style: cinematic]") {
		t.Fatalf("compose (b): composed must contain style suffix; got %q", r.Composed)
	}
	if r.NegativeAffix != "" {
		t.Fatalf("compose (b): empty negative; want NegativeAffix=\"\"; got %q", r.NegativeAffix)
	}
	if r.StyleAffix != " [style: cinematic]" {
		t.Fatalf("compose (b): StyleAffix shape wrong; want %q; got %q", " [style: cinematic]", r.StyleAffix)
	}
	if r.ComposedLen != r.OriginalLen+len(r.StyleAffix) {
		t.Fatalf("compose (b): ComposedLen=%d != OriginalLen=%d + StyleAffixLen=%d",
			r.ComposedLen, r.OriginalLen, len(r.StyleAffix))
	}

	// (c) Negative only: composed = prompt + ` [negative: do not include ...]`.
	// Multi-word negatives: `,` → `;`.
	r = imggeneration.ComposePrompt("a misty forest with sunlight", "", "text, watermark, blurry")
	if !strings.HasPrefix(r.Composed, "a misty forest with sunlight") {
		t.Fatalf("compose (c): composed must START with raw prompt; got %q", r.Composed)
	}
	if !strings.Contains(r.Composed, "[negative: do not include text;watermark;blurry]") {
		t.Fatalf("compose (c): composed must contain negative directive with `,`→`;` transform; got %q", r.Composed)
	}
	if r.StyleAffix != "" {
		t.Fatalf("compose (c): empty style; want StyleAffix=\"\"; got %q", r.StyleAffix)
	}
	if r.ComposedLen != r.OriginalLen+len(r.NegativeAffix) {
		t.Fatalf("compose (c): ComposedLen=%d != OriginalLen=%d + NegativeAffixLen=%d",
			r.ComposedLen, r.OriginalLen, len(r.NegativeAffix))
	}

	// (d) Style + negative: full format.
	r = imggeneration.ComposePrompt("a snow-capped peak at sunrise", "watercolor", "low quality")
	wantSuffix := " [style: watercolor] [negative: do not include low quality]"
	if r.Composed != "a snow-capped peak at sunrise"+wantSuffix {
		t.Fatalf("compose (d): full format mismatch; want %q; got %q",
			"a snow-capped peak at sunrise"+wantSuffix, r.Composed)
	}
	if r.ComposedLen != r.OriginalLen+len(r.StyleAffix)+len(r.NegativeAffix) {
		t.Fatalf("compose (d): ComposedLen invariant broken; got %d vs %d+%d+%d",
			r.ComposedLen, r.OriginalLen, len(r.StyleAffix), len(r.NegativeAffix))
	}

	// (e) 400-char prompt arrives WHOLE (no truncation, no first-period
	// split). Same shape used by the smoke fixture above (longPrompt
	// is re-derived to keep the test unit-testable without io.Pipe).
	sentence := "a vintage airport runway at night. "
	parts := []string{
		strings.Repeat(sentence, 18),
		"dim runway beacons flicker along the tarmac. ",
		"a 747 approaches with cabin lights in three rows of windows.",
	}
	longPrompt := strings.Join(parts, "")
	if len(longPrompt) < 400 {
		t.Fatalf("compose (e) setup: want >= 400 chars; got %d", len(longPrompt))
	}
	r = imggeneration.ComposePrompt(longPrompt, "cinematic", "text, watermark")
	if !strings.HasPrefix(r.Composed, longPrompt) {
		t.Fatalf("compose (e): composed MUST START with the raw 400-char text (no truncation); got prefix of %d vs want %d",
			min(len(r.Composed), len(longPrompt)), len(longPrompt))
	}
	// Verify all 3 sentences are present in the composed form.
	for _, s := range parts {
		if !strings.Contains(r.Composed, s) {
			t.Fatalf("compose (e): composed missing sentence %q (first-period split re-emerged)", s)
		}
	}
	if strings.Contains(r.Composed, "\u2026") || strings.Contains(r.Composed, "...") {
		t.Fatal("compose (e): truncation marker detected in composed prompt (legacy MAX_PROMPT_LEN path re-emerged)")
	}
}
