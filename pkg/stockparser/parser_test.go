package stockparser

import (
	"strconv"
	"strings"
	"testing"
)

const testURL = "https://www.youtube.com/watch?v=abc123"

// TestParseTimestampClipSpecs_EmptyText verifies the empty-marker
// contract: empty / whitespace-only input returns nil (NOT an
// empty slice). godlike/07 NO-FAKE-AVAILABILITY ensures callers
// can distinguish "no timestamps found" from "parse succeeded
// with zero rows" via the nil-vs-empty-slice discriminator.
func TestParseTimestampClipSpecs_EmptyText(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace_only", "   \n\n  \t"},
		{"only_round_header", "Round 1 - Test"}, // no timestamp range = no clip yielded
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseTimestampClipSpecs(tc.in, testURL)
			if got != nil {
				t.Errorf("expected nil for input %q, got %v (len=%d)", tc.in, got, len(got))
			}
		})
	}
}

// TestParseTimestampClipSpecs_CanonicalSingleLine matches the
// user's diagnostic payload verbatim: 1 timestamp range on a
// single line, no round prefix. Round must be 0; Slug must be the
// time-range literal fallback. Title is empty (no round prefix).
func TestParseTimestampClipSpecs_CanonicalSingleLine(t *testing.T) {
	in := "[00:16:33] - [00:17:28]"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d (input=%q)", len(got), in)
	}
	c := got[0]
	if c.Round != 0 {
		t.Errorf("Round: got %d, want 0 (no Round prefix)", c.Round)
	}
	if c.StartSec != 16*60+33 {
		t.Errorf("StartSec: got %.0f, want %d", c.StartSec, 16*60+33)
	}
	if c.EndSec != 17*60+28 {
		t.Errorf("EndSec: got %.0f, want %d", c.EndSec, 17*60+28)
	}
	if c.Slug != "00-16-33_to_00-17-28" {
		t.Errorf("Slug: got %q, want %q (time-range literal fallback)", c.Slug, "00-16-33_to_00-17-28")
	}
	if c.SourceURL != testURL {
		t.Errorf("SourceURL: got %q, want %q (passed-through verbatim)", c.SourceURL, testURL)
	}
	if c.Title != "" {
		t.Errorf("Title: got %q, want empty (no Round prefix on this line)", c.Title)
	}
}

// TestParseTimestampClipSpecs_SingleLineRoundAndRange asserts the
// one-line form: "Round N - Title" prefix on the SAME line as the
// timestamp range. Round + Title must come from the line-local
// prefix, not the pending carry-forward. Title is preserved as
// raw text (no title-casing — the parser is presentation-agnostic).
func TestParseTimestampClipSpecs_SingleLineRoundAndRange(t *testing.T) {
	in := "Round 7 - Broner barcolla [00:16:33] - [00:17:28]"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(got))
	}
	c := got[0]
	if c.Round != 7 {
		t.Errorf("Round: got %d, want 7", c.Round)
	}
	if c.Title != "Broner barcolla" {
		t.Errorf("Title: got %q, want %q (raw, no title-casing)", c.Title, "Broner barcolla")
	}
	if c.Slug != "broner-barcolla" {
		t.Errorf("Slug: got %q, want %q (lowercase + hyphen convention from user diagnostic)", c.Slug, "broner-barcolla")
	}
}

// TestParseTimestampClipSpecs_MultiLineCarryForward asserts the
// MULTI-LINE form seen in the user's pasted example: Round N -
// Title on a line, then the timestamp range on the next line.
// The parser MUST carry pending context forward (Go regex has no
// look-behind) so the round + title win the assignment. The
// carry-forward pending state is one-shot: after the range
// consumes it, the next standalone line overwrites pending.
func TestParseTimestampClipSpecs_MultiLineCarryForward(t *testing.T) {
	in := `Round 1 - La fase di studio e la velocita di Pacquiao
[00:00:32] - [00:03:51]
Round 2 - Il posizionamento e i primi scambi
[00:04:07] - [00:05:45]
[00:16:33] - [00:17:28]
`
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 3 {
		t.Fatalf("expected 3 clips, got %d", len(got))
	}
	if got[0].Round != 1 || got[0].Title != "La fase di studio e la velocita di Pacquiao" {
		t.Errorf("clip[0]: Round=%d Title=%q", got[0].Round, got[0].Title)
	}
	if got[1].Round != 2 || got[1].Title != "Il posizionamento e i primi scambi" {
		t.Errorf("clip[1]: Round=%d Title=%q", got[1].Round, got[1].Title)
	}
	// clip[2] has no Round prefix AND no carry-forward (pending
	// was one-shot consumed at clip[1]).
	if got[2].Round != 0 {
		t.Errorf("clip[2] should have Round=0 (no carry-forward from line 5), got %d", got[2].Round)
	}
	if got[2].Slug != "00-16-33_to_00-17-28" {
		t.Errorf("clip[2] should use time-range literal Slug, got %q", got[2].Slug)
	}
}

// TestParseTimestampClipSpecs_MMSSTimestamps asserts the MM:SS
// form (common for short clips). pkg/textutil.ParseTimestamp
// already supports both; the regex still requires the bracket
// delimiter so "[00:32] - [01:27]" yields 32 + 87 seconds.
func TestParseTimestampClipSpecs_MMSSTimestamps(t *testing.T) {
	in := "[00:32] - [01:27]"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(got))
	}
	if got[0].StartSec != 32 || got[0].EndSec != 87 {
		t.Errorf("MM:SS parse: StartSec=%v EndSec=%v want 32/87", got[0].StartSec, got[0].EndSec)
	}
}

// TestParseTimestampClipSpecs_EndashEmdashSeparators asserts
// user-pasted typography variants on the separator. The user's
// diagnostic example uses ASCII "-"; but en-dash / em-dash also
// appear in rich-text pastes.
func TestParseTimestampClipSpecs_EndashEmdashSeparators(t *testing.T) {
	cases := []string{
		"[00:00:32] - [00:03:51]", // ASCII hyphen-minus
		"[00:00:32] – [00:03:51]", // en dash U+2013
		"[00:00:32] — [00:03:51]", // em dash U+2014
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := ParseTimestampClipSpecs(in, testURL)
			if len(got) != 1 {
				t.Fatalf("expected 1 clip, got %d (input=%q)", len(got), in)
			}
			if got[0].StartSec != 32 || got[0].EndSec != 231 {
				t.Errorf("StartSec=%v EndSec=%v want 32/231 (input=%q)", got[0].StartSec, got[0].EndSec, in)
			}
		})
	}
}

// TestParseTimestampClipSpecs_MalformedAtomPreservedLine verifies
// the godlike/07 NO-FAKE-AVAILABILITY contract: a line containing
// a *syntactically valid* timestamp range that produces zero
// seconds (00:00:00 - 00:00:00) is preserved in the output
// (StartSec=0 + EndSec=0) rather than silently dropped. The
// downstream stock planner's validation rejects zero-durations,
// surfacing the failure to the operator — but the parser MUST
// NOT pretend the line wasn't there.
//
// Note: the regex requires digit-only atoms (xx:yy:zz with
// letters doesn't match), so the malformed-but-matching form is
// "[00:00:00] - [00:00:00]" — the inverted-range check zeros
// the seconds and the line is still appended.
func TestParseTimestampClipSpecs_MalformedAtomPreservedLine(t *testing.T) {
	in := `[00:00:32] - [00:03:51]
[00:00:00] - [00:00:00]
[00:08:00] - [00:09:00]
`
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 3 {
		t.Fatalf("expected 3 clips (line preserved), got %d", len(got))
	}
	// Healthy neighbors: clip[0] and clip[2] parse cleanly.
	if got[0].StartSec != 32 || got[0].EndSec != 231 {
		t.Errorf("clip[0]: StartSec=%v EndSec=%v", got[0].StartSec, got[0].EndSec)
	}
	if got[2].StartSec != 480 || got[2].EndSec != 540 {
		t.Errorf("clip[2]: StartSec=%v EndSec=%v", got[2].StartSec, got[2].EndSec)
	}
	// Malformed line: zero-zero sentinel (parser still surfaces
	// the line so downstream validation catches the bug).
	if got[1].StartSec != 0 || got[1].EndSec != 0 {
		t.Errorf("clip[1]: zero-duration line should be StartSec=0 EndSec=0, got %v / %v", got[1].StartSec, got[1].EndSec)
	}
}

// TestParseTimestampClipSpecs_InvertedRange asserts the
// end<start defense: an inverted range like "[00:03:51] - [00:00:32]"
// is zeroed out so the downstream stock planner's validation
// fails the explicit planner call with a typed ErrPlannerBudgetTooSmall.
func TestParseTimestampClipSpecs_InvertedRange(t *testing.T) {
	got := ParseTimestampClipSpecs("[00:03:51] - [00:00:32]", testURL)
	if len(got) != 1 || got[0].StartSec != 0 || got[0].EndSec != 0 {
		t.Fatalf("inverted range should be zero-zeroed, got %v", got)
	}
}

// TestParseTimestampClipSpecs_AlternateRoundKeywords confirms the
// parser accepts non-English "Round" translations (Italian
// "turno", Spanish "asalto", French "manche") — boxing-relevant
// in those languages. The case-insensitive flag handles "Round"
// / "round" / "ROUND" uniformly. German "Runde" is intentionally
// NOT in the regex (boxing is rare in Germany; the user's domain
// is the Italian/Spanish/French boxing canon).
func TestParseTimestampClipSpecs_AlternateRoundKeywords(t *testing.T) {
	cases := []struct {
		keyword string
		want    int
	}{
		{"Round", 7},
		{"round", 7},
		{"ROUND", 7},
		{"turno", 12},
		{"asalto", 3},
		{"manche", 5},
	}
	for _, tc := range cases {
		t.Run(tc.keyword, func(t *testing.T) {
			in := tc.keyword + " " + strconv.Itoa(tc.want) + " [00:16:33] - [00:17:28]"
			got := ParseTimestampClipSpecs(in, testURL)
			if len(got) != 1 || got[0].Round != tc.want {
				t.Fatalf("keyword %q: got Round=%d (clips=%d), want %d", tc.keyword, got[0].Round, len(got), tc.want)
			}
		})
	}
}

// TestParseTimestampClipSpecs_ColonSeparatorAfterRound asserts
// the "Round 1: Title" form (colon instead of dash) is accepted.
// The regex's `[-:.]?` separator allows colons, but a future
// tightening of that class would silently break this — the test
// guards the contract.
func TestParseTimestampClipSpecs_ColonSeparatorAfterRound(t *testing.T) {
	in := "Round 1: Broner barcolla [00:16:33] - [00:17:28]"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(got))
	}
	if got[0].Round != 1 {
		t.Errorf("Round: got %d, want 1", got[0].Round)
	}
	if got[0].Title != "Broner barcolla" {
		t.Errorf("Title: got %q, want %q", got[0].Title, "Broner barcolla")
	}
}

// TestParseTimestampClipSpecs_CRLFLineEndings locks the Windows /
// Google Docs CRLF contract. TrimSpace handles the \r, so the
// carry-forward + match logic should work byte-equivalent to LF.
func TestParseTimestampClipSpecs_CRLFLineEndings(t *testing.T) {
	in := "Round 1 - Broner barcolla\r\n[00:16:33] - [00:17:28]\r\n"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("CRLF: expected 1 clip, got %d", len(got))
	}
	if got[0].Round != 1 || got[0].Title != "Broner barcolla" {
		t.Errorf("CRLF: Round=%d Title=%q", got[0].Round, got[0].Title)
	}
}

// TestParseTimestampClipSpecs_WhitespaceOnlyTitle_NoUntitledLeak
// locks the SafeFolderName("untitled") guard: when the user
// pastes "Round 1 -    " (whitespace-only title), the parser MUST
// NOT fall back to the literal "untitled" leaf — the time-range
// literal Slug is the canonical fallback.
func TestParseTimestampClipSpecs_WhitespaceOnlyTitle_NoUntitledLeak(t *testing.T) {
	in := "Round 1 -     [00:00:32] - [00:03:51]"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(got))
	}
	if got[0].Title != "" {
		t.Errorf("Title should be empty after TrimSpace, got %q", got[0].Title)
	}
	if got[0].Slug == "untitled" {
		t.Errorf("Slug leaked 'untitled' literal — should fall back to time-range literal %q", got[0].Slug)
	}
	if got[0].Slug != "00-00-32_to_00-03-51" {
		t.Errorf("Slug: got %q, want %q", got[0].Slug, "00-00-32_to_00-03-51")
	}
}

// TestParseTimestampClipSpecs_SecondRangeOnSameLineDropped locks
// the documented "one range per line" limitation. A line with
// TWO timestamp ranges yields only the FIRST; the second is
// silently ignored (this is a godlike/07 NO-FAKE-AVAILABILITY
// trade-off — a future PR can switch to FindAllStringIndex if
// multi-range lines become common).
func TestParseTimestampClipSpecs_SecondRangeOnSameLineDropped(t *testing.T) {
	in := "[00:00:32] - [00:03:51] [00:05:00] - [00:06:00]"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("expected 1 clip (one range per line), got %d", len(got))
	}
	if got[0].StartSec != 32 || got[0].EndSec != 231 {
		t.Errorf("first range should be 32-231, got %v - %v", got[0].StartSec, got[0].EndSec)
	}
}

// TestParseTimestampClipSpecs_LongSlugFromLongTitle asserts that
// a long title doesn't exceed the Drive leaf-name cap (255
// bytes). pathutil.SafeFolderName keeps the leaf verbatim (no
// trim-suffix) so a 240-char title produces a 240-char slug, well
// within Drive's 255-byte limit. The test guards against a future
// "add TrimSuffix" optimization that would silently drop the
// last word.
func TestParseTimestampClipSpecs_LongSlugFromLongTitle(t *testing.T) {
	long := strings.Repeat("boxing-clip-", 20) // 240 chars
	in := "Round 1 - " + long + " [00:00:32] - [00:03:51]"
	got := ParseTimestampClipSpecs(in, testURL)
	if len(got) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(got))
	}
	if !strings.HasPrefix(got[0].Slug, "boxing-clip-") {
		t.Errorf("Slug should preserve raw title ('boxing-clip-...'), got %q", got[0].Slug)
	}
	if len(got[0].Slug) > 255 {
		t.Errorf("Slug exceeds Drive leaf-name cap (255 bytes), got len=%d", len(got[0].Slug))
	}
}
