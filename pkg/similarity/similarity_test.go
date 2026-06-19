package similarity

import (
	"testing"
)

func TestTokenSet(t *testing.T) {
	set := TokenSet("Hello World")
	if len(set) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(set))
	}
	if _, ok := set["hello"]; !ok {
		t.Error("expected 'hello' in set")
	}
	if _, ok := set["world"]; !ok {
		t.Error("expected 'world' in set")
	}
}

func TestTokenSet_Lowercase(t *testing.T) {
	set := TokenSet("HELLO WORLD")
	if _, ok := set["hello"]; !ok {
		t.Error("expected lowercase 'hello'")
	}
}

func TestTokenSet_ShortWords(t *testing.T) {
	set := TokenSet("a an the cat")
	if _, ok := set["the"]; !ok {
		t.Error("expected 'the' (3 chars)")
	}
	if _, ok := set["cat"]; !ok {
		t.Error("expected 'cat' (3 chars)")
	}
	for _, short := range []string{"a", "an"} {
		if _, ok := set[short]; ok {
			t.Errorf("did not expect '%s' (< 3 chars)", short)
		}
	}
}

func TestTokenSet_Punctuation(t *testing.T) {
	set := TokenSet("hello, world! how-are_you? (test)")
	expected := []string{"hello", "world", "how", "are", "you", "test"}
	for _, tok := range expected {
		if _, ok := set[tok]; !ok {
			t.Errorf("expected token %q", tok)
		}
	}
}

func TestTokenSet_Empty(t *testing.T) {
	set := TokenSet("")
	if len(set) != 0 {
		t.Errorf("expected empty set for empty string, got %d", len(set))
	}
}

func TestTokenSet_OnlyShort(t *testing.T) {
	set := TokenSet("a b c d")
	if len(set) != 0 {
		t.Errorf("expected empty set for short words only, got %d", len(set))
	}
}

func TestTokenSetFromTokens(t *testing.T) {
	set := TokenSetFromTokens(
		[]string{"hello world", "foo bar"},
		[]string{"baz qux"},
	)
	for _, tok := range []string{"hello", "world", "foo", "bar", "baz", "qux"} {
		if _, ok := set[tok]; !ok {
			t.Errorf("expected token %q", tok)
		}
	}
}

func TestTokenSetFromTokens_Empty(t *testing.T) {
	set := TokenSetFromTokens()
	if len(set) != 0 {
		t.Errorf("expected empty set, got %d", len(set))
	}
}

func TestJaccard(t *testing.T) {
	a := map[string]struct{}{"cat": {}, "dog": {}, "bird": {}}
	b := map[string]struct{}{"cat": {}, "dog": {}, "fish": {}}
	got := Jaccard(a, b)
	if got != 0.5 {
		t.Errorf("expected 0.5, got %f", got)
	}
}

func TestJaccard_Identical(t *testing.T) {
	a := map[string]struct{}{"cat": {}, "dog": {}}
	b := map[string]struct{}{"dog": {}, "cat": {}}
	if got := Jaccard(a, b); got != 1.0 {
		t.Errorf("expected 1.0, got %f", got)
	}
}

func TestJaccard_Empty(t *testing.T) {
	a := map[string]struct{}{}
	b := map[string]struct{}{"cat": {}}
	if got := Jaccard(a, b); got != 0 {
		t.Errorf("expected 0 for empty first set, got %f", got)
	}
	if got := Jaccard(b, a); got != 0 {
		t.Errorf("expected 0 for empty second set, got %f", got)
	}
}

func TestJaccard_NoOverlap(t *testing.T) {
	a := map[string]struct{}{"cat": {}}
	b := map[string]struct{}{"dog": {}}
	if got := Jaccard(a, b); got != 0 {
		t.Errorf("expected 0, got %f", got)
	}
}

func TestOverlapRatio(t *testing.T) {
	tests := []struct {
		name         string
		aStart, aEnd int
		bStart, bEnd int
		expected     float64
	}{
		{"same_interval", 0, 100, 0, 100, 1.0},
		{"half_overlap", 0, 100, 50, 150, 0.5},
		{"no_overlap", 0, 50, 100, 150, 0},
		{"one_within_other", 0, 100, 25, 75, 1.0},
		{"invalid_a", 100, 50, 0, 100, 0},
		{"invalid_b", 0, 100, 100, 50, 0},
		{"zero_duration", 0, 0, 0, 100, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OverlapRatio(tt.aStart, tt.aEnd, tt.bStart, tt.bEnd)
			if got != tt.expected {
				t.Errorf("OverlapRatio(%d,%d,%d,%d) = %f, want %f",
					tt.aStart, tt.aEnd, tt.bStart, tt.bEnd, got, tt.expected)
			}
		})
	}
}
