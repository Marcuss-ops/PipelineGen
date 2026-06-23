package semantic

import "testing"

// NormalizeSearchText edge-case tests for the media/semantic layer
// (internal/media/semantic/helpers.go).
// This implementation: variadic, deduplicate tokens, sort alphabetically.

func TestNormalizeSearchText_Empty(t *testing.T) {
	if got := NormalizeSearchText(); got != "" {
		t.Errorf("empty variadic: got %q, want empty", got)
	}
}

func TestNormalizeSearchText_EmptyParts(t *testing.T) {
	if got := NormalizeSearchText("", "", ""); got != "" {
		t.Errorf("empty parts: got %q, want empty", got)
	}
}

func TestNormalizeSearchText_SinglePart(t *testing.T) {
	got := NormalizeSearchText("hello world")
	if got != "hello world" {
		t.Errorf("single part: got %q, want %q", got, "hello world")
	}
}

func TestNormalizeSearchText_Deduplication(t *testing.T) {
	got := NormalizeSearchText("hello world", "world hello")
	// Tokens: hello, world → deduplicated → sorted → "hello world"
	if got != "hello world" {
		t.Errorf("dedup: got %q, want %q", got, "hello world")
	}
}

func TestNormalizeSearchText_AlphabeticalSort(t *testing.T) {
	got := NormalizeSearchText("cat dog apple")
	if got != "apple cat dog" {
		t.Errorf("sort: got %q, want %q", got, "apple cat dog")
	}
}

func TestNormalizeSearchText_Lowercase(t *testing.T) {
	got := NormalizeSearchText("HELLO World")
	if got != "hello world" {
		t.Errorf("lowercasing: got %q, want %q", got, "hello world")
	}
}

func TestNormalizeSearchText_MultiplePartsMerged(t *testing.T) {
	got := NormalizeSearchText("apple banana", "cherry date", "elderberry")
	if got != "apple banana cherry date elderberry" {
		t.Errorf("merge: got %q, want %q", got, "apple banana cherry date elderberry")
	}
}

func TestNormalizeSearchText_WhitespaceInParts(t *testing.T) {
	got := NormalizeSearchText("  hello   world  ")
	if got != "hello world" {
		t.Errorf("whitespace: got %q, want %q", got, "hello world")
	}
}

func TestNormalizeSearchText_MixedCaseDedup(t *testing.T) {
	got := NormalizeSearchText("Hello World", "hello world")
	// lowercased + deduplicated → "hello world"
	if got != "hello world" {
		t.Errorf("mixed case dedup: got %q, want %q", got, "hello world")
	}
}

func TestNormalizeSearchText_WithNumbers(t *testing.T) {
	got := NormalizeSearchText("item 123", "test 456 item")
	if got != "123 456 item test" {
		t.Errorf("numbers: got %q, want %q", got, "123 456 item test")
	}
}
