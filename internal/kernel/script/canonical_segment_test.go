package script

import "testing"

func TestNormalizeCanonicalSegmentFillsSourceAndHashes(t *testing.T) {
	segment := NormalizeCanonicalSegment(CanonicalSegment{
		ID: " segment-001 ", Position: 2, Text: "  John Froelich built a tractor.  ",
	})
	if segment.ID != "segment-001" || segment.SourceText != segment.Text {
		t.Fatalf("normalized segment = %+v", segment)
	}
	if segment.TextHash == "" || segment.SourceTextHash == "" {
		t.Fatal("expected both text hashes")
	}
	if err := segment.Validate(); err != nil {
		t.Fatalf("normalized segment rejected: %v", err)
	}
}

func TestCanonicalSegmentValidationRequiresSourceHash(t *testing.T) {
	segment := CanonicalSegment{ID: "segment-001", Position: 0, Text: "text", TextHash: "hash", SourceText: "source"}
	if err := segment.Validate(); err == nil {
		t.Fatal("expected missing source_text_hash error")
	}
}

func TestCanonicalSegmentHashIsStableAcrossWhitespace(t *testing.T) {
	first := NormalizeCanonicalSegment(CanonicalSegment{ID: "s", Text: "A   stable\nsegment"})
	second := NormalizeCanonicalSegment(CanonicalSegment{ID: "s", Text: " a stable segment "})
	if first.TextHash != second.TextHash {
		t.Fatalf("hashes differ: %q vs %q", first.TextHash, second.TextHash)
	}
}
