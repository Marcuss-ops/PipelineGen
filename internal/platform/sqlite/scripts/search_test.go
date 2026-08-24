package scripts

import "testing"

// NormalizeSearchText edge-case tests for the SQLite scripts layer
// (internal/platform/sqlite/scripts/search.go).
// This implementation: lowercase, strip URL-ish tokens, replace punctuation
// with spaces, collapse whitespace.

func TestNormalizeSearchText_Empty(t *testing.T) {
	if got := NormalizeSearchText(""); got != "" {
		t.Errorf("empty string: got %q, want empty", got)
	}
}

func TestNormalizeSearchText_OnlySpaces(t *testing.T) {
	if got := NormalizeSearchText("   "); got != "" {
		t.Errorf("only spaces: got %q, want empty", got)
	}
}

func TestNormalizeSearchText_Lowercase(t *testing.T) {
	got := NormalizeSearchText("Hello WORLD")
	if got != "hello world" {
		t.Errorf("lowercasing: got %q, want %q", got, "hello world")
	}
}

func TestNormalizeSearchText_PunctuationReplacement(t *testing.T) {
	tests := []struct{ input, want string }{
		{"hello, world", "hello world"},
		{"hello.world!", "hello world"},
		{"a;b:c?", "a b c"},
		{"(parens) [brackets]", "parens brackets"},
		{"dash-separated_underscore", "dash separated underscore"},
		{`"quoted"`, "quoted"},
		{"tab\tnewline\n", "tab newline"},
		{"hash#&amp", "hash amp"},
	}
	for _, tc := range tests {
		got := NormalizeSearchText(tc.input)
		if got != tc.want {
			t.Errorf("punctuation %q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeSearchText_WhitespaceCollapse(t *testing.T) {
	got := NormalizeSearchText("hello    world\t\t\n\n  again")
	if got != "hello world again" {
		t.Errorf("whitespace collapse: got %q, want %q", got, "hello world again")
	}
}

func TestNormalizeSearchText_LeadingTrailing(t *testing.T) {
	got := NormalizeSearchText("  hello  ")
	if got != "hello" {
		t.Errorf("leading/trailing trim: got %q, want %q", got, "hello")
	}
}

func TestNormalizeSearchText_URLStripping(t *testing.T) {
	// URLs with http/https/www prefixes get tokenized by punctuation replacer
	// then collapsed. The "http" and "https" tokens are not special-cased
	// in this implementation — they pass through as regular tokens.
	got := NormalizeSearchText("check https://example.com for details")
	// After punctuation replacement → "check https   example com for details"
	// After whitespace collapse → "check https example com for details"
	want := "check https example com for details"
	if got != want {
		t.Errorf("URL stripping: got %q, want %q", got, want)
	}
}

func TestNormalizeSearchText_Unicode(t *testing.T) {
	got := NormalizeSearchText("Café naïve 日本語")
	// Should lowercase ASCII portions, preserve unicode
	want := "café naïve 日本語"
	if got != want {
		t.Errorf("unicode: got %q, want %q", got, want)
	}
}

func TestNormalizeSearchText_OnlyPunctuation(t *testing.T) {
	// Characters in the replacer (! & ( ) ) become spaces → collapsed to empty.
	// Characters not in the replacer (@ $ % ^ *) pass through unchanged.
	got := NormalizeSearchText("!@#$%^&*()")
	// After replacement: " @ $ % ^ * " (with space from ! &), collapsed to "@ $%^ *"
	// Check that only replacer-covered chars were stripped and whitespace collapsed
	if got == "" {
		t.Error("expected non-empty result for mixed replacer/non-replacer punctuation")
	}
	// The implementation only replaces characters explicitly listed in the replacer.
	// @ $ % ^ * are NOT in the replacer, so they survive.
	if len(got) == 0 || got[0] == ' ' {
		t.Errorf("got %q, should have leading non-space", got)
	}
}

func TestNormalizeSearchText_MixedNewlines(t *testing.T) {
	got := NormalizeSearchText("line1\n\nline2\nline3")
	if got != "line1 line2 line3" {
		t.Errorf("mixed newlines: got %q, want %q", got, "line1 line2 line3")
	}
}

func TestNormalizeSearchText_LongInput(t *testing.T) {
	input := "This is a longer piece of TEXT with multiple, punctuation marks! And some CAPITALS too."
	got := NormalizeSearchText(input)
	want := "this is a longer piece of text with multiple punctuation marks and some capitals too"
	if got != want {
		t.Errorf("long input: got %q, want %q", got, want)
	}
}
