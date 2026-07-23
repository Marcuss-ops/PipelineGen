package images

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

func TestAssetToResultWithCache_ExposesTraceFields(t *testing.T) {
	hit := true
	res := assetToResultWithCache(&asset.ImageAsset{
		Hash:     "hash-1",
		Origin:   asset.ImageOriginRetrieved,
		Provider: asset.ProviderWikipedia,
		PathRel:  "images/hash-1.jpg",
	}, &hit, "database", "wikipedia")

	if res.CacheHit == nil || !*res.CacheHit {
		t.Fatalf("CacheHit = %#v, want true pointer", res.CacheHit)
	}
	if res.CacheSource != "database" {
		t.Fatalf("CacheSource = %q, want %q", res.CacheSource, "database")
	}
	if res.RetrievalProvider != "wikipedia" {
		t.Fatalf("RetrievalProvider = %q, want %q", res.RetrievalProvider, "wikipedia")
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if payload["cache_hit"] != true {
		t.Fatalf("cache_hit JSON = %#v, want true", payload["cache_hit"])
	}
	if payload["cache_source"] != "database" {
		t.Fatalf("cache_source JSON = %#v, want database", payload["cache_source"])
	}
	if payload["retrieval_provider"] != "wikipedia" {
		t.Fatalf("retrieval_provider JSON = %#v, want wikipedia", payload["retrieval_provider"])
	}
}
