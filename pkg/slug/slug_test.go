// PR-SLUG-HELPER-EXTRACT (July 2026): hermetic TDD test surface
// for the canonical SlugifyTitle helper. The test asserts the
// byte-equivalence contract between the parser-side and
// stock-pipeline call sites: identical input → identical output.
// 10+ canonical cases + 5+ edge cases + 1 byte-equivalence
// assertion against the legacy deriveSlug / slugifyTitle
// pre-extraction output.
package slug

import "testing"

// TestSlugifyTitle_Canonical asserts the 10+ canonical titles
// map to the expected canonical slug. The titles mirror the
// pre-extraction test fixtures in pkg/stockparser and
// internal/capabilities/assets/providers/stock/stockpipeline so
// both packages' regression tests stay byte-stable after the
// extraction lands.
func TestSlugifyTitle_Canonical(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		// Canonical happy path — the user diagnostic example.
		{"RoundHyphenTitle", "Round 7 - Broner barcolla", "round-7-broner-barcolla"},

		// Italian boxing canon (the parser is multilingual).
		{"ItalianRound", "Round 1 - La fase di studio", "round-1-la-fase-di-studio"},
		{"ItalianRound2", "Round 2 - Il posizionamento", "round-2-il-posizionamento"},

		// Pacquiao/Broner (the stockpipeline E2E canonical).
		{"PacuiaoBroner", "Pacquiao lands a clean left hand", "pacquiao-lands-a-clean-left-hand"},

		// Single-word title (no spaces, no hyphens).
		{"SingleWord", "Uppercut", "uppercut"},

		// Already-lowercase (no transform needed).
		{"AlreadyLower", "round 1", "round-1"},

		// Mixed case (transformation needed).
		{"MixedCase", "RoUnD 7", "round-7"},

		// Numeric (Round 12 / Round 1).
		{"NumericRound", "Round 12 (final)", "round-12-final"},

		// Multi-word with internal punctuation.
		{"MultiWordPunct", "Round 7: Broner barcolla", "round-7-broner-barcolla"},

		// Underscore (preserved, NOT escaped).
		{"WithUnderscore", "Pacquiao_Vs_Broner", "pacquiao_vs_broner"},

		// Multiple consecutive spaces (collapsed to single hyphen).
		{"MultipleSpaces", "round   7", "round-7"},

		// Title with leading/trailing whitespace (trimmed).
		{"LeadingTrailingWS", "  Round 7  ", "round-7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSlugifyTitle_EdgeCases asserts the edge cases return ""
// (NOT "untitled" or any other placeholder) so callers can
// fall through to their own canonical fallback.
func TestSlugifyTitle_EdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // always "" — the godlike/07 NO-FAKE-AVAILABILITY contract
	}{
		{"Empty", "", ""},
		{"WhitespaceOnly", "   ", ""},
		{"TabsOnly", "\t\t", ""},
		{"NewlineOnly", "\n\n", ""},
		{"AllUnsafePunct", "!!!", ""},
		{"AllColons", ":::", ""},
		{"AllSlashes", "///", ""},
		{"AllDots", "...", ""},
		{"MixedUnsafeOnly", "!@#$%^&*()", ""},
		{"OnlyDashes", "---", ""},         // step 5 trims to ""
		{"OnlyUnderscores", "___", "___"}, // underscores are KEPT (not unsafe)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q (empty fallback per godlike/07)", tc.input, got, tc.want)
			}
		})
	}
}

// TestSlugifyTitle_MultiByteUnicode asserts the helper handles
// multi-byte unicode (Spanish / Italian / accented chars) per
// the leaf rule that the package is unicode-first.
func TestSlugifyTitle_MultiByteUnicode(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"SpanishTilde", "Mañana de boxeo", "mañana-de-boxeo"},
		{"ItalianAccent", "Veredito finale", "veredito-finale"},
		{"PortugueseCedilla", "Posição brasileira", "posição-brasileira"},
		{"FrenchCircumflex", "Tranche finale", "tranche-finale"},
		{"GermanUmlaut", "Runde fünf", "runde-fünf"},
		{"GreekLetter", "αγώνας", "αγώνας"},
		{"ChineseSimple", "回合 七", "回合-七"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSlugifyTitle_NoUnderscoreEscapeArtifacts is the
// load-bearing regression guard that locks the strip-entirely
// semantic. The pre-extraction parser-side deriveSlug REPLACED
// unsafe chars with underscores (e.g. "Round: 1" → "round__1");
// the canonical post-extraction helper STRIPS them entirely
// (e.g. "Round: 1" → "round-1"). This test asserts the new
// contract and prevents future agents from re-introducing the
// pathutil.SafeFolderName "replace with underscore" semantic
// (which would carry escape artifacts into the slug).
func TestSlugifyTitle_NoUnderscoreEscapeArtifacts(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // MUST NOT contain "_" as escape artifact
	}{
		{"Colon", "Round: 1", "round-1"},
		{"Parens", "Round 1 (final)", "round-1-final"},
		{"Ampersand", "Box & Roll", "box-roll"},
		{"Slash", "Box / Roll", "box-roll"},
		{"Backslash", "Box \\ Roll", "box-roll"},
		{"Period", "Round 1. Final", "round-1-final"},
		{"Apostrophe", "Pacquiao's left", "pacquiaos-left"},
		{"Exclamation", "Round 1!", "round-1"},
		{"Question", "Round 1?", "round-1"},
		{"Percent", "100% box", "100-box"},
		{"Dollar", "$100 fight", "100-fight"},
		{"Hash", "Round #1", "round-1"},
		{"At", "@round 7", "round-7"},
		{"Mixed", "Box: Roll & Counter! 7?", "box-roll-counter-7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q (no underscore escape per godlike/07 strip-entirely semantic)", tc.input, got, tc.want)
			}
		})
	}
}

// TestSlugifyTitle_HyphenCollapse asserts the collapse-consecutive-
// hyphens step is load-bearing (catches the " - " → "---" case
// that the pre-extraction parser-side deriveSlug left as-is).
func TestSlugifyTitle_HyphenCollapse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"SingleSpaceDashSpace", "Round 7 - Broner", "round-7-broner"},
		{"DoubleSpaceDashSpace", "Round  7  -  Broner", "round-7-broner"},
		{"MultipleDashes", "a---b", "a-b"},
		{"QuadrupleDashes", "a----b", "a-b"},
		{"Pathological", "----------", ""},      // all hyphens → "" after trim
		{"WithSpacesAndDashes", "  -  -  ", ""}, // mixed → ""
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSlugifyTitle_ByteEquivalenceWithLegacy is the
// cross-package byte-equivalence lock. The canonical post-
// extraction helper MUST produce byte-identical output to the
// pre-extraction stock-pipeline slugifyTitle (the chosen SSOT
// semantic — strip-entirely + collapse-hyphens + trim-edges).
// The pre-extraction parser-side deriveSlug produced DIFFERENT
// output (SafeFolderName replace-underscore + no-collapse + no-trim)
// and is migrated to match the helper as part of this PR.
func TestSlugifyTitle_ByteEquivalenceWithLegacy(t *testing.T) {
	// Pre-extraction slugifyTitle (stock pipeline) reference
	// output, computed by the inlined algorithm. The post-
	// extraction helper MUST produce identical output for
	// these inputs (the chosen SSOT semantic).
	legacyStockPipeline := []struct {
		input string
		want  string
	}{
		{"Round 7 - Broner barcolla", "round-7-broner-barcolla"},
		{"Round 1 - La fase di studio", "round-1-la-fase-di-studio"},
		{"Pacquiao lands a clean left hand", "pacquiao-lands-a-clean-left-hand"},
		{"Uppercut", "uppercut"},
		{"round 1", "round-1"},
		{"RoUnD 7", "round-7"},
		{"Round 12 (final)", "round-12-final"},
		{"Round 7: Broner barcolla", "round-7-broner-barcolla"},
		{"Pacquiao_Vs_Broner", "pacquiao_vs_broner"},
		{"round   7", "round-7"},
		{"  Round 7  ", "round-7"},
		{"Round 1!", "round-1"},
		{"Round 1?", "round-1"},
		{"100% box", "100-box"},
		{"Round #1", "round-1"},
		{"Mañana de boxeo", "mañana-de-boxeo"},
		{"Veredito finale", "veredito-finale"},
	}
	for _, tc := range legacyStockPipeline {
		t.Run("legacy/"+tc.input, func(t *testing.T) {
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q (must match pre-extraction slugifyTitle byte-for-byte)", tc.input, got, tc.want)
			}
		})
	}
}
