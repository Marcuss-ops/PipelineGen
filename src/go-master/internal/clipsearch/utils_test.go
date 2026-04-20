package clipsearch

import "testing"

func TestKeywordFolderCandidates(t *testing.T) {
	candidates := keywordFolderCandidates("Floyd Mayweather interview")
	if len(candidates) < 2 {
		t.Fatalf("expected at least 2 candidates, got %d", len(candidates))
	}
	if candidates[0] != "floyd_mayweather_interview" {
		t.Fatalf("unexpected primary candidate: %q", candidates[0])
	}
	foundFloyd := false
	for _, c := range candidates {
		if c == "floyd" {
			foundFloyd = true
		}
	}
	if !foundFloyd {
		t.Fatalf("expected 'floyd' candidate, got %v", candidates)
	}
}

func TestNormalizeFolderComparable(t *testing.T) {
	a := normalizeFolderComparable("Floyd_Mayweather-Interview")
	b := normalizeFolderComparable("floyd mayweather interview")
	if a != b {
		t.Fatalf("expected normalized names to match, got %q vs %q", a, b)
	}
}
