package adapters

import "testing"

// NormalizeSearchText edge-case tests for the application/scripts/memory
// layer (internal/application/scripts/memory/normalize.go).
// This implementation: lowercase, keep only alphanumeric+spaces.

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
	got := NormalizeSearchText("Hello WORLD 123")
	if got != "hello world 123" {
		t.Errorf("lowercasing: got %q, want %q", got, "hello world 123")
	}
}

func TestNormalizeSearchText_StripsPunctuation(t *testing.T) {
	got := NormalizeSearchText("hello, world! how are you?")
	if got != "hello world how are you" {
		t.Errorf("punctuation strip: got %q, want %q", got, "hello world how are you")
	}
}

func TestNormalizeSearchText_PreservesLettersAndDigits(t *testing.T) {
	got := NormalizeSearchText("a1 b2 c3 test123")
	if got != "a1 b2 c3 test123" {
		t.Errorf("alphanumeric preservation: got %q", got)
	}
}

func TestNormalizeSearchText_CollapseWhitespace(t *testing.T) {
	got := NormalizeSearchText("hello   world\n\t  again")
	if got != "hello world again" {
		t.Errorf("whitespace collapse: got %q", got)
	}
}

func TestNormalizeSearchText_OnlySymbols(t *testing.T) {
	got := NormalizeSearchText("@#$%^&*()!?")
	if got != "" {
		t.Errorf("only symbols: got %q, want empty", got)
	}
}

func TestNormalizeSearchText_UnicodeLetters(t *testing.T) {
	got := NormalizeSearchText("Café naïve résumé")
	if got != "café naïve résumé" {
		t.Errorf("unicode letters: got %q, want %q", got, "café naïve résumé")
	}
}

func TestNormalizeSearchText_MixedInput(t *testing.T) {
	// This implementation drops non-alphanumeric chars WITHOUT replacing them
	// with spaces, so adjacent words separated by e.g. "/" get concatenated.
	got := NormalizeSearchText("Hello, World! 2024 - Test/with/slashes")
	if got != "hello world 2024 testwithslashes" {
		t.Errorf("mixed input: got %q, want %q", got, "hello world 2024 testwithslashes")
	}
}

func TestNormalizeSearchText_LeadingTrailing(t *testing.T) {
	got := NormalizeSearchText("  hello world  ")
	if got != "hello world" {
		t.Errorf("leading/trailing: got %q, want %q", got, "hello world")
	}
}
