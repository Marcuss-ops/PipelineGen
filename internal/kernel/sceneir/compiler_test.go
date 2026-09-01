package sceneir

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// mediterraneanGreekSaladSegment is the canonical golden-fixture segment
// used across all SceneIR regression tests. It mirrors the LIVE-test
// input that lost its ID (mediterranean-* → scene-N) and had its source
// text contaminated by creative narration ("Get ready to dive...").
func mediterraneanGreekSaladSegment() script.CanonicalSegment {
	return script.CanonicalSegment{
		ID:       "mediterranean-01-greek-salad",
		Position: 0,
		Text:     "Greek salad contains tomatoes, feta cheese and olives.",
	}
}

// TestCompilerPreservesExplicitIdentity pins the deepest LIVE-test bug:
// the canonical mediterranean segment ID must survive compilation verbatim
// (it must NOT become scene-1), and Position + SourceText must be carried
// unchanged. This is the SOURCE IDENTITY IS IMMUTABLE rule.
func TestCompilerPreservesExplicitIdentity(t *testing.T) {
	input := mediterraneanGreekSaladSegment()

	ir, err := Compile(CompileInput{Segment: input})
	require.NoError(t, err)

	require.Equal(t, "mediterranean-01-greek-salad", ir.SegmentID,
		"segment_id must be preserved verbatim (mediterranean-* must not become scene-N)")
	require.Equal(t, 0, ir.Position, "position must be preserved")
	require.Equal(t, input.Text, ir.SourceText,
		"source_text must equal the canonical input text")
	require.NotEmpty(t, ir.SourceTextHash,
		"source_text_hash must be stamped by the compiler")
	require.Equal(t, script.ComputeCanonicalSegmentTextHash(input.Text), ir.SourceTextHash,
		"source_text_hash must match the canonical hash of the source text")
}

// TestGeneratedNarrationCannotModifySourceText pins the rule that the LLM
// may rewrite NarrationText but may NEVER touch SourceText. The creative
// narration "Get ready to dive into the vibrant world of Greek cuisine..."
// must land ONLY in NarrationText; SourceText must stay the canonical
// "Greek salad contains tomatoes, feta cheese and olives." The test also
// verifies the cryptographic tamper check catches a rewritten SourceText.
func TestGeneratedNarrationCannotModifySourceText(t *testing.T) {
	input := mediterraneanGreekSaladSegment()
	creativeNarration := "Get ready to dive into the vibrant world of Greek cuisine..."

	ir, err := Compile(CompileInput{
		Segment:           input,
		NarrationOverride: creativeNarration,
	})
	require.NoError(t, err)

	// Narration divergence is allowed and detected.
	require.Equal(t, creativeNarration, ir.NarrationText,
		"narration override must land in narration_text")
	require.NotEqual(t, ir.SourceText, ir.NarrationText,
		"narration_text must NOT equal source_text when the LLM rewrote it")
	require.True(t, ir.IsNarrationDivergence(),
		"narration divergence must be detected")

	// SourceText is immutable: still the canonical input text, and its
	// stamped hash still matches a fresh hash of the current SourceText.
	require.Equal(t, input.Text, ir.SourceText,
		"source_text must NOT be contaminated by the creative narration")
	require.False(t, ir.SourceTextTampered(),
		"source_text_hash must still match a fresh hash of source_text")

	// Tampered SourceText must be caught by the cryptographic check.
	tampered := ir
	tampered.SourceText = creativeNarration
	require.True(t, tampered.SourceTextTampered(),
		"a rewritten source_text must be detected as tampered")

	// Identity snapshot comparison must also catch the tampering.
	err = tampered.VerifyIdentity(ir.Identity())
	require.Error(t, err, "VerifyIdentity must reject a tampered source_text")
	violation, ok := err.(IdentityViolation)
	require.True(t, ok, "VerifyIdentity must return an IdentityViolation")
	require.Equal(t, "source_text", violation.Field)
}

// TestCompilerProducesVisualProfileForEveryScene pins the LIVE-test
// metric: semantic_profiles = 0/5. A compiled SceneIR must ALWAYS carry a
// non-empty SemanticProfile (Subject + at least one VisualTerm), even when
// the entity extractor / small LLM produced nothing. The test compiles all
// five golden-fixture segments and asserts 5/5 profiles with 0 missing.
func TestCompilerProducesVisualProfileForEveryScene(t *testing.T) {
	segments := []script.CanonicalSegment{
		{ID: "mediterranean-01-greek-salad", Position: 0, Text: "Greek salad contains tomatoes, feta cheese and olives."},
		{ID: "mediterranean-02-hummus", Position: 1, Text: "Hummus is traditionally made with chickpeas, tahini, lemon juice and olive oil."},
		{ID: "mediterranean-03-sardines", Position: 2, Text: "Grilled sardines are seasoned with lemon and fresh herbs."},
		{ID: "mediterranean-04-shakshuka", Position: 3, Text: "Shakshuka features eggs poached in tomatoes and peppers."},
		{ID: "mediterranean-05-paella", Position: 4, Text: "Seafood paella combines shrimp, mussels and rice."},
	}

	var irs []SceneIR
	for _, seg := range segments {
		ir, err := Compile(CompileInput{Segment: seg})
		require.NoError(t, err)
		irs = append(irs, ir)
	}

	missing, total := MissingProfileCount(irs)
	require.Equal(t, 5, total)
	require.Equal(t, 0, missing, "every compiled scene must have a non-empty visual_profile")

	// Each profile must have a non-empty subject and at least one term.
	for i, ir := range irs {
		visual := script.BuildSegmentVisualProfile(ir.Profile)
		require.NotEmpty(t, visual.Subject, "scene %d (%s) must have a subject", i, ir.SegmentID)
		require.NotEmpty(t, visual.Terms, "scene %d (%s) must have at least one visual term", i, ir.SegmentID)
		require.NoError(t, ir.Validate(), "scene %d (%s) must pass structural validation", i, ir.SegmentID)
	}
}

// TestCompileRejectsInvalidCanonicalSegment pins the fail-closed contract:
// the compiler never silently fixes up an invalid canonical segment. An
// empty segment_id or empty text must surface as ErrCompileInputInvalid.
func TestCompileRejectsInvalidCanonicalSegment(t *testing.T) {
	cases := []struct {
		name string
		seg  script.CanonicalSegment
	}{
		{"empty id", script.CanonicalSegment{Position: 0, Text: "text"}},
		{"empty text", script.CanonicalSegment{ID: "seg-1", Position: 0}},
		{"negative position", script.CanonicalSegment{ID: "seg-1", Position: -1, Text: "text"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(CompileInput{Segment: tc.seg})
			require.ErrorIs(t, err, ErrCompileInputInvalid)
		})
	}
}

// TestNarrationDefaultsToSourceTextWhenNoOverride pins that, when no LLM
// narration override is provided, NarrationText defaults to the immutable
// SourceText. This is the non-divergent path: query planners may freely
// consume either surface because they are identical.
func TestNarrationDefaultsToSourceTextWhenNoOverride(t *testing.T) {
	input := mediterraneanGreekSaladSegment()
	ir, err := Compile(CompileInput{Segment: input})
	require.NoError(t, err)
	require.Equal(t, ir.SourceText, ir.NarrationText,
		"narration_text must default to source_text when no override is provided")
	require.False(t, ir.IsNarrationDivergence(),
		"no override means no narration divergence")
}

// TestMinimalProfileIsSourceGrounded pins that the minimal SemanticProfile
// the compiler synthesizes when the extractor produced nothing is still
// grounded in the source text: the subject is the trimmed source text (or
// its first noun chunk), never an invented value. This guarantees the
// visual_profile is never null AND never hallucinated.
func TestMinimalProfileIsSourceGrounded(t *testing.T) {
	input := mediterraneanGreekSaladSegment()
	ir, err := Compile(CompileInput{Segment: input})
	require.NoError(t, err)
	visual := script.BuildSegmentVisualProfile(ir.Profile)
	require.NotEmpty(t, visual.Subject)
	require.NotEmpty(t, visual.Terms)
	// The subject must be a substring of, or equal to, the source text
	// (no invented subjects like "Mediterranean" or "ready").
	require.True(t,
		strings.Contains(strings.ToLower(ir.SourceText), strings.ToLower(visual.Subject)) ||
			strings.Contains(strings.ToLower(visual.Subject), strings.ToLower(ir.SourceText)) ||
			visual.Subject == strings.TrimSpace(input.Text),
		"subject %q must be grounded in the source text %q", visual.Subject, ir.SourceText)
}
