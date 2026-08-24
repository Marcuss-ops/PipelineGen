package normalizer

import (
	"context"
	"testing"
)

func TestDefaultNormalizer_BasicNormalization(t *testing.T) {
	n := NewDefaultNormalizer()
	ctx := context.Background()

	res, err := n.Normalize(ctx, "it", "  I Maya COSTRUIRONO Città. ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "i maya costruirono città"
	if res.Normalized != want {
		t.Errorf("normalized = %q, want %q", res.Normalized, want)
	}
	if res.Original != "I Maya COSTRUIRONO Città." {
		t.Errorf("original = %q, want %q", res.Original, "I Maya COSTRUIRONO Città.")
	}
	if res.Fingerprint == "" {
		t.Error("fingerprint is empty")
	}
}

func TestDefaultNormalizer_DeterministicFingerprint(t *testing.T) {
	n := NewDefaultNormalizer()
	ctx := context.Background()

	a, _ := n.Normalize(ctx, "IT", "I Maya costruirono città.  ")
	b, _ := n.Normalize(ctx, "it", "  i MAYA costruirono città")

	if a.Fingerprint != b.Fingerprint {
		t.Errorf("same phrase must yield same fingerprint: %q vs %q", a.Fingerprint, b.Fingerprint)
	}
}

func TestDefaultNormalizer_DifferentInputsDifferentFingerprint(t *testing.T) {
	n := NewDefaultNormalizer()
	ctx := context.Background()

	a, _ := n.Normalize(ctx, "it", "i maya")
	b, _ := n.Normalize(ctx, "it", "i maya costruirono città")

	if a.Fingerprint == b.Fingerprint {
		t.Error("different phrases must yield different fingerprints")
	}
}

func TestDefaultNormalizer_EmptyPhrase(t *testing.T) {
	n := NewDefaultNormalizer()
	_, err := n.Normalize(context.Background(), "it", "   ")
	if err == nil {
		t.Error("expected error for empty phrase")
	}
}

func TestDefaultNormalizer_EmptyLanguage(t *testing.T) {
	n := NewDefaultNormalizer()
	_, err := n.Normalize(context.Background(), "", "i maya")
	if err == nil {
		t.Error("expected error for empty language")
	}
}
