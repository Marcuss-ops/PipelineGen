package usecase

import (
	"context"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

func TestSourceEnricher_Enrich_Disabled(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

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

	res, err := e.Enrich(context.Background(), item)
	if err != nil {
		t.Fatalf("enrich disabled: %v", err)
	}
	if res != scriptports.EnrichBypass {
		t.Fatalf("expected bypass, got %v", res)
	}
	if item.Source.SourceText != "original" {
		t.Fatalf("source text should not be modified when disabled")
	}
}

func TestSourceEnricher_Enrich_Hit(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

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
	res, err := e.Enrich(context.Background(), &item)
	if err != nil {
		t.Fatalf("enrich hit: %v", err)
	}
	if res != scriptports.EnrichHit {
		t.Fatalf("expected hit, got %v", res)
	}
	if item.Source.SourceText != "cached source text" {
		t.Fatalf("expected cached source text, got %q", item.Source.SourceText)
	}
}

func TestSourceEnricher_Enrich_CacheOnlyMiss(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

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

	_, err := e.Enrich(context.Background(), item)
	if err == nil {
		t.Fatal("expected error on cache_only miss")
	}
}

func TestSourceEnricher_Enrich_ForceRefresh(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

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

	res, err := e.Enrich(context.Background(), &item)
	if err != nil {
		t.Fatalf("enrich force_refresh: %v", err)
	}
	if res != scriptports.EnrichBypass {
		t.Fatalf("expected bypass on force_refresh, got %v", res)
	}
}

func TestSourceEnricher_Enrich_ClipsBypassCache(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:       "item-clip",
		Title:    "Clip Topic",
		Language: "en",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceClips,
			Topic:       "Clip Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, TTLHours: 24},
		},
	}

	key := computeSourceCacheKey(&item)
	cache.SaveResearchCache(context.Background(), scriptpkg.ResearchCacheRecord{
		Key:        key,
		Topic:      "Clip Topic",
		Language:   "en",
		SourceText: "cached clip source text",
		ExpiresAt:  time.Now().UTC().Add(time.Hour),
	})

	res, err := e.Enrich(context.Background(), &item)
	if err != nil {
		t.Fatalf("clip enrich bypass: %v", err)
	}
	if res != scriptports.EnrichBypass {
		t.Fatalf("expected bypass for clip source, got %v", res)
	}
	if item.Source.SourceText != "" {
		t.Fatalf("clip source text must not be populated from topic cache, got %q", item.Source.SourceText)
	}
}

func TestSourceEnricher_Save(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

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

	if err := e.Save(context.Background(), item, "resolved text"); err != nil {
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
	if err := e.Save(context.Background(), item, "resolved text v2"); err != nil {
		t.Fatalf("second save: %v", err)
	}
	rec = cache.data[key]
	if rec.SourceText != "resolved text v2" {
		t.Fatalf("expected overwritten text, got %q", rec.SourceText)
	}
}

func TestSourceEnricher_Save_ClipsBypassCache(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:    "item-clip",
		Title: "Clip Topic",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceClips,
			Topic:       "Clip Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, TTLHours: 12},
		},
	}

	if err := e.Save(context.Background(), item, "resolved clip text"); err != nil {
		t.Fatalf("clip save bypass: %v", err)
	}
	if len(cache.data) != 0 {
		t.Fatalf("expected no cache writes for clip sources, got %d", len(cache.data))
	}
}

func TestSourceEnricher_Save_Disabled(t *testing.T) {
	cache := newFakeTopicSourceCache()
	e := NewSourceTextEnricher(cache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:    "item-1",
		Title: "Save Topic",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Save Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		},
	}

	if err := e.Save(context.Background(), item, "resolved text"); err != nil {
		t.Fatalf("save disabled: %v", err)
	}

	if len(cache.data) != 0 {
		t.Fatal("expected no writes when cache disabled")
	}
}

func TestSourceEnricher_Save_PropagatesError(t *testing.T) {
	failingCache := &failingTopicSourceCache{}
	e := NewSourceTextEnricher(failingCache, zap.NewNop())

	item := scriptpkg.GenerationItemV2{
		ID:    "item-1",
		Title: "Save Topic",
		Source: scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceText,
			Topic:       "Save Topic",
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache, TTLHours: 12},
		},
	}

	if err := e.Save(context.Background(), item, "resolved text"); err == nil {
		t.Fatal("expected error from failing cache")
	}
}
