package scripts

import "testing"

// TestCacheKey_NormalizesWhitespaceAndLang pins the P1-6 anti-muda contract:
// whitespace/case variants of the same text (and lang) share one cache entry
// instead of paying a second LLM call.
func TestCacheKey_NormalizesWhitespaceAndLang(t *testing.T) {
	base := cacheKey("Hello world", "pt-br")
	variants := []struct{ text, lang string }{
		{"  Hello world  ", "pt-br"},
		{"Hello   world", "pt-br"},
		{"HELLO WORLD", "pt-br"},
		{"Hello world", "PT-BR"},
		{"  hello   WORLD ", " pt-br "},
	}
	for _, v := range variants {
		if got := cacheKey(v.text, v.lang); got != base {
			t.Fatalf("cacheKey(%q,%q) differs from canonical key", v.text, v.lang)
		}
	}
	if other := cacheKey("Hello world!", "pt-br"); other == base {
		t.Fatal("different text must NOT share a key")
	}
	if other := cacheKey("Hello world", "es"); other == base {
		t.Fatal("different language must NOT share a key")
	}
}
