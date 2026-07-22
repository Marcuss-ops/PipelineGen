package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// fakeTopicSourceCache is a test double for scriptports.TopicSourceCache.
type fakeTopicSourceCache struct {
	data map[string]scriptpkg.ResearchCacheRecord
}

func newFakeTopicSourceCache() *fakeTopicSourceCache {
	return &fakeTopicSourceCache{data: make(map[string]scriptpkg.ResearchCacheRecord)}
}

func (f *fakeTopicSourceCache) GetResearchCache(_ context.Context, key string) (string, error) {
	rec, ok := f.data[key]
	if !ok {
		return "", nil
	}
	if !rec.ExpiresAt.IsZero() && rec.ExpiresAt.Before(time.Now().UTC()) {
		return "", nil
	}
	return rec.SourceText, nil
}

func (f *fakeTopicSourceCache) SaveResearchCache(_ context.Context, rec scriptpkg.ResearchCacheRecord) error {
	f.data[rec.Key] = rec
	return nil
}

func TestSourceEnricher_enrich_Disabled(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := newSourceEnricher(cache, zap.NewNop())

	item := &scriptpkg.GenerationItemV2{
		ID:       "item-1",
		Title:    "Test Topic",
		Language: "it",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Test Topic",
			SourceText:  "original",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		},
	}

	res, err := e.enrich(context.Background(), item)
	if err != nil {
		t.Fatalf("enrich disabled: %v", err)
	}
	if res != sourceCacheBypass {
		t.Fatalf("expected bypass, got %v", res)
	}
	if item.Source.SourceText != "original" {
		t.Fatalf("source text should not be modified when disabled")
	}
}

func TestSourceEnricher_enrich_Hit(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := newSourceEnricher(cache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:       "item-1",
		Title:    "Cached Topic",
		Language: "en",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Cached Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, TTLHours: 24},
		},
	}

	// Pre-seed the cache.
	key := computeSourceCacheKey(&item)
	cache.SaveResearchCache(context.Background(), scriptpkg.ResearchCacheRecord{
		Key:        key,
		Topic:      "Cached Topic",
		Language:   "en",
		SourceText: "cached source text",
		ExpiresAt:  time.Now().UTC().Add(time.Hour),
	})

	// Reset mutable field used by the test subject.
	item.Source.SourceText = ""
	res, err := e.enrich(context.Background(), &item)
	if err != nil {
		t.Fatalf("enrich hit: %v", err)
	}
	if res != sourceCacheHit {
		t.Fatalf("expected hit, got %v", res)
	}
	if item.Source.SourceText != "cached source text" {
		t.Fatalf("expected cached source text, got %q", item.Source.SourceText)
	}
}

func TestSourceEnricher_enrich_CacheOnlyMiss(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := newSourceEnricher(cache, zap.NewNop())

	item := &scriptpkg.GenerationItemV2{
		ID:       "item-1",
		Title:    "Missing Topic",
		Language: "en",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Missing Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeCacheOnly},
		},
	}

	_, err := e.enrich(context.Background(), item)
	if err == nil {
		t.Fatal("expected error on cache_only miss")
	}
}

func TestSourceEnricher_enrich_ForceRefresh(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := newSourceEnricher(cache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:       "item-1",
		Title:    "Cached Topic",
		Language: "en",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Cached Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeForceRefresh},
		},
	}

	key := computeSourceCacheKey(&item)
	cache.SaveResearchCache(context.Background(), scriptpkg.ResearchCacheRecord{
		Key:        key,
		Topic:      "Cached Topic",
		Language:   "en",
		SourceText: "cached source text",
		ExpiresAt:  time.Now().UTC().Add(time.Hour),
	})

	res, err := e.enrich(context.Background(), &item)
	if err != nil {
		t.Fatalf("enrich force_refresh: %v", err)
	}
	if res != sourceCacheBypass {
		t.Fatalf("expected bypass on force_refresh, got %v", res)
	}
}

func TestSourceEnricher_save(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := newSourceEnricher(cache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:       "item-1",
		Title:    "Save Topic",
		Language: "en",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Save Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, TTLHours: 12},
		},
	}

	if err := e.save(context.Background(), item, "resolved text"); err != nil {
		t.Fatalf("save: %v", err)
	}

	key := computeSourceCacheKey(&item)
	rec, ok := cache.data[key]
	if !ok {
		t.Fatal("expected record to be saved")
	}
	if rec.SourceText != "resolved text" {
		t.Fatalf("expected resolved text, got %q", rec.SourceText)
	}
	if rec.Language != "en" {
		t.Fatalf("expected language en, got %q", rec.Language)
	}

	// A second save should be idempotent (overwrite, not error).
	if err := e.save(context.Background(), item, "resolved text v2"); err != nil {
		t.Fatalf("second save: %v", err)
	}
	rec = cache.data[key]
	if rec.SourceText != "resolved text v2" {
		t.Fatalf("expected overwritten text, got %q", rec.SourceText)
	}
}

func TestSourceEnricher_save_Disabled(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := newSourceEnricher(cache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:    "item-1",
		Title: "Save Topic",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Save Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		},
	}

	if err := e.save(context.Background(), item, "resolved text"); err != nil {
		t.Fatalf("save disabled: %v", err)
	}

	if len(cache.data) != 0 {
		t.Fatal("expected no writes when cache disabled")
	}
}

func TestSourceEnricher_save_PropagatesError(t *testing.T) {
	failingCache := &failingTopicSourceCache{}
	e := newSourceEnricher(failingCache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:    "item-1",
		Title: "Save Topic",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Save Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, TTLHours: 12},
		},
	}

	if err := e.save(context.Background(), item, "resolved text"); err == nil {
		t.Fatal("expected error from failing cache")
	}
}

// failingTopicSourceCache always returns an error.
type failingTopicSourceCache struct{}

func (f *failingTopicSourceCache) GetResearchCache(context.Context, string) (string, error) {
	return "", errors.New("get failure")
}

func (f *failingTopicSourceCache) SaveResearchCache(context.Context, scriptpkg.ResearchCacheRecord) error {
	return errors.New("save failure")
}
