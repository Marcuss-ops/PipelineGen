package usecase

import "testing"

func TestDecideGenerationCache(t *testing.T) {
	if got := decideGenerationCache("exact_hit"); !got.Hit || got.Status != "exact_hit" {
		t.Fatalf("exact hit decision = %+v", got)
	}
	if got := decideGenerationCache("generated"); got.Hit || got.Status != "generated" {
		t.Fatalf("generated decision = %+v", got)
	}
}

func TestLookupGenerationCache(t *testing.T) {
	if !lookupGenerationCache(generationCacheDecision{Hit: true}) {
		t.Fatal("hit decision must be true")
	}
	if lookupGenerationCache(generationCacheDecision{}) {
		t.Fatal("empty decision must be false")
	}
}
