package script

import (
	"sync"
	"testing"
)

func cachedProfile() SegmentSemanticProfile {
	return SegmentSemanticProfile{
		SegmentID:                 "segment-001",
		TextHash:                  "text-hash-1",
		UnderstandingModelVersion: "gemma3:1b",
		PromptVersion:             "segment_semantics_v1",
		Topic:                     "origine dei trattori",
		Subtopics:                 []string{"agricoltura"},
		Keywords:                  []WeightedKeyword{{Value: "tractor", Confidence: 1}},
		VisualTerms:               []WeightedKeyword{{Value: "vintage tractor", Confidence: 1}},
		Entities:                  []ExtractedEntity{{Value: "Iowa", Type: "PLACE", Confidence: 0.9}},
		Retrieval:                 &RetrievalIntent{YouTube: []string{"tractor history"}},
	}
}

func TestSegmentSemanticProfileCache_ExactKeyHit(t *testing.T) {
	cache := NewSegmentSemanticProfileCache()
	profile := cachedProfile()
	if err := cache.Put(profile); err != nil {
		t.Fatalf("put profile: %v", err)
	}
	got, ok := cache.Get(profile.Key())
	if !ok {
		t.Fatal("expected exact cache hit")
	}
	if got.Topic != profile.Topic || got.Key() != profile.Key() {
		t.Fatalf("got profile %#v, want %#v", got, profile)
	}
}

func TestSegmentSemanticProfileCache_TextAndVersionChangesMiss(t *testing.T) {
	cache := NewSegmentSemanticProfileCache()
	profile := cachedProfile()
	if err := cache.Put(profile); err != nil {
		t.Fatalf("put profile: %v", err)
	}
	variants := []SegmentSemanticProfile{profile, profile, profile}
	variants[0].TextHash = "different-text"
	variants[1].UnderstandingModelVersion = "other-model"
	variants[2].PromptVersion = "other-prompt"
	for i, variant := range variants {
		if _, ok := cache.Get(variant.Key()); ok {
			t.Errorf("variant %d unexpectedly hit cache: %#v", i, variant.Key())
		}
	}
}

func TestSegmentSemanticProfileCache_InvalidateRemovesAllVersions(t *testing.T) {
	cache := NewSegmentSemanticProfileCache()
	profile := cachedProfile()
	for _, model := range []string{"gemma3:1b", "gemma3:2b"} {
		profile.UnderstandingModelVersion = model
		if err := cache.Put(profile); err != nil {
			t.Fatalf("put profile: %v", err)
		}
	}
	if removed := cache.Invalidate(profile.SegmentID); removed != 2 {
		t.Fatalf("removed %d profiles, want 2", removed)
	}
	if cache.Len() != 0 {
		t.Fatalf("cache length = %d, want 0", cache.Len())
	}
}

func TestSegmentSemanticProfileCache_DefensiveCopies(t *testing.T) {
	cache := NewSegmentSemanticProfileCache()
	profile := cachedProfile()
	if err := cache.Put(profile); err != nil {
		t.Fatalf("put profile: %v", err)
	}
	profile.Keywords[0].Value = "mutated-input"
	profile.Retrieval.YouTube[0] = "mutated-input"

	got, ok := cache.Get(cachedProfile().Key())
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Keywords[0].Value != "tractor" || got.Retrieval.YouTube[0] != "tractor history" {
		t.Fatalf("cache was affected by input mutation: %#v", got)
	}
	got.Keywords[0].Value = "mutated-output"
	got.Retrieval.YouTube[0] = "mutated-output"
	again, _ := cache.Get(cachedProfile().Key())
	if again.Keywords[0].Value != "tractor" || again.Retrieval.YouTube[0] != "tractor history" {
		t.Fatal("cache was affected by output mutation")
	}
}

func TestSegmentSemanticProfileCache_RejectsIncompleteIdentity(t *testing.T) {
	cache := NewSegmentSemanticProfileCache()
	profile := cachedProfile()
	profile.PromptVersion = ""
	if err := cache.Put(profile); err == nil {
		t.Fatal("expected incomplete identity error")
	}
	if _, err := NewSegmentSemanticProfileKey("segment", "hash", "model", ""); err == nil {
		t.Fatal("expected incomplete key error")
	}
}

func TestSegmentSemanticProfileCache_ConcurrentAccess(t *testing.T) {
	cache := NewSegmentSemanticProfileCache()
	profile := cachedProfile()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cache.Put(profile); err != nil {
				t.Errorf("put profile: %v", err)
			}
			if _, ok := cache.Get(profile.Key()); !ok {
				t.Error("expected concurrent cache hit")
			}
		}()
	}
	wg.Wait()
	if cache.Len() != 1 {
		t.Fatalf("cache length = %d, want 1", cache.Len())
	}
}
