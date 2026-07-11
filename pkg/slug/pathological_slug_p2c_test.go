// Package slug — pathological_slug_p2c_test.go (July 2026).
//
// P2.C — Input patologici (slug) test suite.
//
// USER SPEC (verbatim, July 2026): "Implementa la suite P2.C —
// Input patologici (rune-safe) su main. Testa: transcript da
// centinaia di migliaia di caratteri, emoji, accenti italiani,
// testo in cinese e arabo, caratteri null, HTML, JSON dentro
// transcript, righe molto lunghe, clip senza nome, slug con
// apostrofi, ID oltre lunghezza normale. La truncation deve
// essere RUNE-SAFE a 500 rune per clip: verifica che Unicode
// non venga corcorrupta. Lavora su main, commit frequenti,
// push."
//
// SCOPE: this file pins the SLUG-DERIVATION layer's pathological
// input contract. The canonical SlugifyTitle function lives in
// pkg/slug (the leaf package, no internal/ imports). The
// transcript + clip-name + clip-ID pathological inputs are tested
// in internal/application/scripts/usecase/pathological_inputs_p2c_test.go
// (separate test file in the usecase package — package
// boundary preserved per AGENTS.md).
//
// The existing slug_test.go covers 30+ canonical cases (Round 7
// - Broner barcolla, Italian / Spanish / Portuguese / French /
// German / Greek / Chinese titles, hyphen collapse, byte
// equivalence with the pre-extraction stock-pipeline). This
// P2.C suite covers the user-spec pathological scenarios NOT
// already pinned: apostrophes (the user-spec scenario),
// emoji-as-title, HTML-in-title, extremely long title (no max
// length cap), and null bytes.
//
// ATTESO (acceptance, per the user spec):
//
//  1. SlugifyTitle is rune-safe over Unicode (accents, CJK,
//     emoji are processed as runes, not bytes).
//  2. apostrophes (and all filesystem-unsafe chars) are
//     STRIPPED ENTIRELY (not replaced with underscores — the
//     pre-extraction SafeFolderName semantic). "Pacquiao's" →
//     "pacquiaos".
//  3. pathological inputs (huge length, emoji-only, HTML, null
//     bytes) flow through without crashing or producing
//     invalid output.
//  4. empty / whitespace-only / pure-unsafe-char inputs
//     collapse to "" (the godlike/07 NO-FAKE-AVAILABILITY
//     contract) so callers can fall through to their own
//     canonical fallback.
//
// SUT BUGS (pin current behavior; all are 2026-07 candidates
// for the "honest lock" backlog):
//
//  1. SlugifyTitle does NOT have a max length cap: a 100KB
//     title produces a 100KB slug. The risk is filesystem-
//     name length limits (ext4 caps at 255 bytes per
//     component; the stock-pipeline folder-name consumer would
//     silently produce a too-long folder name).
//  2. SlugifyTitle does NOT preserve apostrophes (intentional
//     per the strip-entirely semantic, but flagged for the
//     "do we want to preserve them for SEO?" backlog). The
//     current contract is strip-entirely; this is a deliberate
//     divergence from the pathutil.SafeFolderName
//     replace-underscore semantic.
//  3. SlugifyTitle does NOT sanitize HTML/JSON: "<script>" is
//     stripped (chars <, >, / are dropped) but the inner
//     letter content is KEPT (so "<b>x</b>" → "bx"). The risk
//     is downstream rendering if the slug is shown raw in a
//     web view (XSS via inner letter content is NOT possible
//     because the angle brackets are stripped, but a slug
//     like "scriptalert1scriptround-1" is ugly).
//  4. SlugifyTitle does NOT count grapheme clusters: a flag
//     emoji like "🇮🇹" (Italy) is 2 runes but 1 grapheme. The
//     current contract is rune-level ONLY (no grapheme cluster
//     awareness). For Drive folder names this is acceptable;
//     for SEO-friendly URLs it could be a polish item.
package slug

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ── Group 1: Apostrophes & Quotes (user-spec scenario) ─────────────────
//
// The user-spec scenario "slug con apostrofi" is the load-bearing
// case for this group. The contract: apostrophes (and all
// filesystem-unsafe chars) are STRIPPED ENTIRELY (not replaced
// with empty placeholder). "Pacquiao's" → "pacquiaos" (the 's'
// is kept as a unicode letter; the apostrophe is dropped).
func TestPathologicalSlug_P2C_ApostrophesAndQuotes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		input string
		want  string
	}{
		// The user-spec scenario.
		{"apostrophe_s", "Pacquiao's left", "pacquiaos-left"},

		// Edge positions.
		{"leading_apostrophe", "'Round 1", "round-1"},
		{"trailing_apostrophe", "Round 1'", "round-1"},
		{"double_apostrophe", "Pacquiao''s", "pacquiaos"},
		{"only_apostrophe", "'", ""},

		// Curly quotes (U+2018, U+2019) — common in pasted
		// titles from word processors / social media.
		{"curly_apostrophe", "Pacquiao\u2019s left", "pacquiaos-left"},
		{"left_quote_only", "\u2018Round 1", "round-1"},
		{"right_quote_only", "Round 1\u2019", "round-1"},
		{"curly_double_quote", "\u201cRound 1\u201d", "round-1"},

		// Multiple apostrophes mixed with other unsafe chars.
		{"apostrophe_with_ampersand", "Pacquiao's & Broner's", "pacquiaos-broners"},
		{"apostrophe_with_colon", "Round: Pacquiao's", "round-pacquiaos"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q (apostrophes stripped per godlike/07 strip-entirely)", tc.input, got, tc.want)
			}
		})
	}
}

// ── Group 2: Emoji in title ────────────────────────────────────────────
//
// Emoji are NOT unicode letters (per unicode.IsLetter), so they're
// treated as filesystem-unsafe and STRIPPED. A title of pure emoji
// collapses to "" (the all-unsafe-chars edge case, which is the
// godlike/07 NO-FAKE-AVAILABILITY contract — caller falls through
// to its own canonical fallback).
func TestPathologicalSlug_P2C_EmojiInTitle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		input string
		want  string
	}{
		{"single_emoji_only", "🥊", ""},
		{"emoji_plus_text", "🥊 Round 1", "round-1"},
		{"emoji_between_text", "Round 🥊 1", "round-1"},
		{"multiple_emoji_prefix", "🥊👊🤜 Round 1", "round-1"},
		{"multiple_emoji_suffix", "Round 1 🥊👊🤜", "round-1"},
		{"emoji_pure_unsafe", "🥊👊🤜", ""},
		{"emoji_surrounded_by_spaces", "  🥊  ", ""},

		// SUT BUG 4: flag emoji (regional indicators) count as
		// 2 runes but 1 grapheme. The function does NOT collapse
		// grapheme clusters. 🇮🇹 → "it" after the regional
		// indicators are dropped (they're not letters).
		{"flag_emoji_not_preserved", "🇮🇹 Italia", "italia"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q (emoji stripped per IsLetter check)", tc.input, got, tc.want)
			}
		})
	}
}

// ── Group 3: HTML in title ─────────────────────────────────────────────
//
// HTML tags have their angle brackets / slashes / parens stripped
// (filesystem-unsafe), but the LETTER content of the tags is KEPT.
// This is a side-effect of the strip-entirely semantic: <b>Round
// 1</b> → "bround-1b" (the 'b' is a unicode letter, kept; the
// '<' / '>' are unsafe, dropped).
//
// For the canonical SEO-friendly use case (titles with embedded
// HTML from scraped pages), this is a known wart. The risk is
// downstream rendering (a slug like "scriptalert1scriptround-1"
// is ugly but NOT an XSS vector because the angle brackets are
// stripped).
func TestPathologicalSlug_P2C_HTMLInTitle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		input string
		want  string
	}{
		// SUT BUG 3: angle brackets dropped, tag-name letters
		// KEPT. <b>Round 1</b> → "bround-1b" (the 'b' is a
		// unicode letter, kept).
		{"angle_brackets_only", "<>", ""},
		{"bold_tag_letters_kept", "<b>Round 1</b>", "bround-1b"},
		{"h1_tag_letters_kept", "<h1>Title</h1>", "h1titleh1"},

		// Script tag: <, >, (, ), / are all dropped; letters
		// and digits are kept. alert(1) → "alert1".
		{"script_tag", "<script>alert(1)</script>Round 1", "scriptalert1scriptround-1"},

		// HTML entities: & and ; are dropped. "Round&nbsp;1" →
		// "roundnbsp1" (the "nbsp" letters are kept).
		{"nbsp_entity", "Round&nbsp;1", "roundnbsp1"},

		// Attribute values: =, ", ' are dropped. "href=x" → "hrefx".
		{"attr_quotes", "<a href=\"x\">link</a>", "a-hrefxlinka"},

		// Realistic scrape: tags + entities + slashes. Space
		// before `="` is collapsed; &#39; entity becomes "39".
		{"realistic_scrape",
			"<div class=\"title\">Round 1: Pacquiao&#39;s</div>", "div-classtitleround-1-pacquiao39sdiv"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q (angle brackets stripped, letters kept per SUT BUG 3)", tc.input, got, tc.want)
			}
		})
	}
}

// ── Group 4: Extremely long title (no max length cap) ──────────────────
//
// SUT BUG 1: SlugifyTitle does NOT have a max length cap. A 100K-
// char title produces a 100K-char slug. The risk is filesystem-
// name length limits (ext4 caps at 255 bytes per component). The
// test pins the current behavior; a follow-up PR could add a
// max-length cap (e.g., SlugifyTitleWithMax) and the test would
// need to be updated to match.
func TestPathologicalSlug_P2C_ExtremelyLong(t *testing.T) {
	t.Parallel()

	t.Run("100K_ascii_uncapped_SUT_BUG_1", func(t *testing.T) {
		t.Parallel()
		input := strings.Repeat("a", 100000)
		got := SlugifyTitle(input)
		// SUT BUG 1: no max length cap.
		if got != input {
			t.Errorf("SlugifyTitle(100K 'a') len = %d, want %d (no max length cap, SUT BUG 1)", len(got), len(input))
		}
	})

	t.Run("100K_CJK_uncapped_SUT_BUG_1", func(t *testing.T) {
		t.Parallel()
		input := strings.Repeat("世", 100000)
		got := SlugifyTitle(input)
		// SUT BUG 1: no max length cap. CJK is preserved in full.
		if utf8.RuneCountInString(got) != 100000 {
			t.Errorf("SlugifyTitle(100K CJK) rune count = %d, want 100000 (no max length cap, SUT BUG 1)", utf8.RuneCountInString(got))
		}
	})

	t.Run("100K_mixed_unicode_uncapped", func(t *testing.T) {
		t.Parallel()
		// 100K chars alternating: ASCII + Italian accents +
		// CJK + emoji. The emoji are dropped (not letters),
		// so the resulting slug is ~75K chars (100K - 25K
		// emoji). This pins the rune-safety at scale + the
		// mixed-script performance.
		input := strings.Repeat("aà世🥊", 25000) // 100K total chars
		got := SlugifyTitle(input)
		// Expected: "aà世" repeated 25000 times = 75000 runes.
		// Each "a" = 1 byte, "à" = 2 bytes, "世" = 3 bytes
		// → 6 bytes per char, 25000 reps = 150000 bytes.
		wantRunes := 25000 * 3 // a + à + 世 (emoji dropped)
		if utf8.RuneCountInString(got) != wantRunes {
			t.Errorf("SlugifyTitle(100K mixed) rune count = %d, want %d", utf8.RuneCountInString(got), wantRunes)
		}
		// Verify rune-safety at scale.
		if !utf8.ValidString(got) {
			t.Errorf("SlugifyTitle(100K mixed) is NOT valid UTF-8 — codepoint got split at scale")
		}
	})
}

// ── Group 5: Null bytes in title ───────────────────────────────────────
//
// Null bytes (\x00) are NOT unicode letters/digits/hyphen/
// underscore/space, so they're STRIPPED. The remaining text is
// slugified. All-null inputs collapse to "" (the
// all-unsafe-chars edge case).
func TestPathologicalSlug_P2C_NullBytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		input string
		want  string
	}{
		{"null_in_middle", "Pac\x00quiao", "pacquiao"},
		{"null_at_start", "\x00Round 1", "round-1"},
		{"null_at_end", "Round 1\x00", "round-1"},
		{"null_between_words", "Round\x00 1", "round-1"},
		{"all_null", "\x00\x00\x00", ""},

		// JSON with nulls (e.g. {"a": null}) — the JSON
		// structural chars are stripped, the letters and the
		// "null" keyword are kept.
		{"json_with_null", `{"a": null}`, "a-null"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := SlugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("SlugifyTitle(%q) = %q, want %q (null bytes stripped per non-letter rule)", tc.input, got, tc.want)
			}
		})
	}
}
