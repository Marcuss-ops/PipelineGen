package wiring

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/artlist/fallback"
)

// cannedPexelsVideosResponse is the minimum Pexels `/v1/videos/search` payload
// that yields one usable candidate (a progressive MP4 in video_files).
const cannedPexelsVideosResponse = `{
  "videos": [
    {
      "id": 3366238,
      "url": "https://www.pexels.com/video/3366238/",
      "image": "https://images.pexels.com/3366238.jpg",
      "duration": 12,
      "user": {"name": "Fixture Author", "url": "https://www.pexels.com/@fixture"},
      "video_files": [
        {"id": 1, "quality": "hd", "file_type": "video/mp4", "width": 1920, "height": 1080, "fps": 30, "link": "https://videos.pexels.com/3366238-hd.mp4"}
      ]
    }
  ]
}`

// policyMediaTypeForAsset maps the asset family an adapter returns onto the
// provider-policy vocabulary. It is the bridge that makes the policy's
// declaration checkable against real adapter output.
func policyMediaTypeForAsset(mediaType asset.MediaType) string {
	switch mediaType {
	case asset.MediaTypeClip, asset.MediaTypeStock, asset.MediaTypeImageVideo:
		return "video"
	case asset.MediaTypeImage:
		return "image"
	default:
		return string(mediaType)
	}
}

// TestProviderCatalogPoliciesMatchTheShippedAdapterSurface cross-checks every
// declared policy media type against what the wired adapter ACTUALLY returns,
// by driving the shipped Pexels searcher against a canned Pexels response.
//
// This is the assertion that would have failed while the policy declared
// "image" over the video adapter (MUDA D2 follow-up): the adapter hits
// /videos/search and types its candidates as clips, so the declaration must
// say video.
func TestProviderCatalogPoliciesMatchTheShippedAdapterSurface(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write([]byte(cannedPexelsVideosResponse))
	}))
	defer srv.Close()

	searcher := fallback.NewPexels(fallback.Config{APIKey: "test-key", BaseURL: srv.URL, SourceName: "pexels"})
	candidates, err := searcher.Search(context.Background(), artapp.SearchRequest{Term: "boxing", Limit: 1})
	if err != nil {
		t.Fatalf("shipped pexels adapter search: %v", err)
	}
	if !strings.HasSuffix(requestedPath, "/videos/search") {
		t.Fatalf("shipped pexels adapter requested %q, want the Pexels VIDEO search endpoint", requestedPath)
	}
	if len(candidates) == 0 {
		t.Fatal("shipped pexels adapter returned no candidates for a valid response")
	}

	declared := make(map[string]string)
	for _, policy := range providerCatalogPolicies() {
		declared[policy.Name] = policy.MediaType
	}

	served := policyMediaTypeForAsset(candidates[0].MediaType)
	if got := declared["pexels"]; got != served {
		t.Fatalf("policy declares media type %q for pexels, but the wired adapter returns %q assets (policy vocabulary %q)", got, candidates[0].MediaType, served)
	}
}

// TestProviderCatalogPoliciesDeclareTheServedSurface pins MEDIA-TYPE ACCURACY
// for the whole table: every policy's declared MediaType must describe the
// endpoint its adapter calls. All three shipped adapters serve video surfaces
// and return clip-typed ProviderAssets, so a reintroduced "image" declaration
// over a video adapter fails here.
//
// Why this pin exists: ProviderPolicy.MediaType is not consulted by the
// registry today, so nothing else would catch the drift.
func TestProviderCatalogPoliciesDeclareTheServedSurface(t *testing.T) {
	policies := providerCatalogPolicies()
	if len(policies) == 0 {
		t.Fatal("providerCatalogPolicies() is empty")
	}

	byName := make(map[string]string, len(policies))
	for _, policy := range policies {
		if policy.Name == "" {
			t.Fatal("provider policy with empty name")
		}
		if _, dup := byName[policy.Name]; dup {
			t.Fatalf("duplicate provider policy %q", policy.Name)
		}
		if !policy.Enabled {
			t.Errorf("provider policy %q is disabled; the catalog builder fails closed on a disabled adapter", policy.Name)
		}
		byName[policy.Name] = policy.MediaType
	}

	for _, name := range []string{"artlist", "pexels", "pixabay"} {
		mediaType, ok := byName[name]
		if !ok {
			t.Fatalf("missing provider policy %q", name)
		}
		if mediaType != "video" {
			t.Errorf("provider policy %q declares media type %q, want %q: the wired adapter serves the video endpoint (/v1/videos/search) and returns clip-typed assets", name, mediaType, "video")
		}
	}
}
