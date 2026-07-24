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

// TestAssetToResultWithCache_ProviderFallbackToRetrievalProvider pins
// PR-IMG-PROVIDER-FALLBACK (July 2026): when Asset.Provider is empty
// or equals "unknown" but the service layer stamped a known Retrieval
// Provider (e.g. duckduckgo) for this row, the response MUST surface
// that Retrieval Provider as the canonical `provider`. This closes
// the silent-fallback signature godlike/07 forbids at the API DTO.
func TestAssetToResultWithCache_ProviderFallbackToRetrievalProvider(t *testing.T) {
	cases := []struct {
		name             string
		assetProvider    asset.ImageProvider
		retrievalSrc     string
		wantResponseProv string
	}{
		{
			name:             "ProviderUnknown_FallsBackToDuckDuckGo",
			assetProvider:    asset.ProviderUnknown,
			retrievalSrc:     "duckduckgo",
			wantResponseProv: "duckduckgo",
		},
		{
			name:             "EmptyProvider_FallsBackToSearXNG",
			assetProvider:    "",
			retrievalSrc:     "searxng",
			wantResponseProv: "searxng",
		},
		{
			name:             "ProviderUnknown_FallsBackToWikipedia",
			assetProvider:    asset.ProviderUnknown,
			retrievalSrc:     "wikipedia",
			wantResponseProv: "wikipedia",
		},
		{
			name:             "ProviderUnknown_FallsBackToDrive",
			assetProvider:    asset.ProviderUnknown,
			retrievalSrc:     "drive",
			wantResponseProv: "drive",
		},
		{
			name:             "UnknownAndUnknown_StaysUnknown_FailClosed",
			assetProvider:    asset.ProviderUnknown,
			retrievalSrc:     string(asset.ProviderUnknown),
			wantResponseProv: string(asset.ProviderUnknown),
		},
		{
			name:             "UnknownAndEmpty_StaysEmpty_FailClosed",
			assetProvider:    asset.ProviderUnknown,
			retrievalSrc:     "",
			wantResponseProv: string(asset.ProviderUnknown),
		},
		{
			name:             "CanonicalProviderWins_NoFallback",
			assetProvider:    asset.ProviderWikipedia,
			retrievalSrc:     "duckduckgo",
			wantResponseProv: "wikipedia",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := assetToResultWithCache(&asset.ImageAsset{
				Hash:     "hash-fb-" + tc.name,
				Origin:   asset.ImageOriginRetrieved,
				Provider: tc.assetProvider,
			}, nil, "database", tc.retrievalSrc)
			if res.Provider != tc.wantResponseProv {
				t.Fatalf("Provider = %q, want %q (asset=%q retrieval=%q)",
					res.Provider, tc.wantResponseProv, tc.assetProvider, tc.retrievalSrc)
			}
			// RetrievalProvider is still stamped verbatim; the mapper
			// does not collapse the two fields.
			if res.RetrievalProvider != tc.retrievalSrc {
				t.Fatalf("RetrievalProvider = %q, want verbatim %q", res.RetrievalProvider, tc.retrievalSrc)
			}
		})
	}
}
