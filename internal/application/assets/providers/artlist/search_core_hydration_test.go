package artlist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSearchLiveAndSave_HydratesFromDetailFetcher verifies that when a
// DetailFetcher is wired, SearchLiveAndSave hydrates the synthetic search
// result with the rich clip detail metadata before persisting the asset.
func TestSearchLiveAndSave_HydratesFromDetailFetcher(t *testing.T) {
	rec := &recordingDispatcher{}

	detailFetcher := &fakeDetailFetcher{
		candidate: &Candidate{
			ID:           "hydrate-001",
			Title:        "Hydrated Title",
			Description:  "Rich description from detail page",
			Creator:      "Hydrated Creator",
			PageURL:      "https://artlist.io/stock-footage/clip/hydrate-001",
			SourceRef:    "https://cdn.artlist.io/hydrate-001.mp4",
			Keywords:     []string{"hydrated", "keyword"},
			Categories:   []string{"Hydrated Category"},
			ThumbnailURL: "https://cdn.artlist.io/hydrate-001/thumb.jpg",
			PreviewURL:   "https://cdn.artlist.io/hydrate-001/preview.mp4",
			DurationMs:   12345,
			Width:        1920,
			Height:       1080,
			LicenseClass: "unlimited",
			CollectionID: "col-123",
			RawMetadata: map[string]any{
				"country":  "Guatemala",
				"location": "Tikal",
			},
		},
	}

	svc := &Service{
		log:           zap.NewNop(),
		assetStore:    nil,
		detailFetcher: detailFetcher,
		scraperSearcher: &staticSearcher{cands: []Candidate{{
			ID:         "hydrate-001",
			Title:      "Search Title",
			SourceRef:  "https://cdn.artlist.io/hydrate-001.mp4",
			PageURL:    "https://artlist.io/stock-footage/clip/hydrate-001",
			SourceName: "artlist",
			Keywords:   []string{"search", "keyword"},
			Categories: []string{"Search Category"},
		}}},
	}
	ss, err := NewSearchService(svc, rec)
	require.NoError(t, err)

	ctx := context.Background()
	resp, err := ss.SearchLiveAndSave(ctx, "maya temple", 1)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Clips, 1)

	// The detail fetcher was invoked for the candidate's page URL.
	assert.Equal(t, "https://artlist.io/stock-footage/clip/hydrate-001", detailFetcher.calledURL)

	// The persisted asset carries the hydrated metadata, not the sparse
	// search result.
	clip := resp.Clips[0]
	assert.Equal(t, "hydrate-001", clip.ID)
	assert.Equal(t, "Hydrated Title", clip.Name)
	assert.Equal(t, []string{"hydrated", "keyword"}, clip.ProviderTags)
	assert.Equal(t, "Hydrated Creator", clip.Metadata["creator"])
	assert.Equal(t, "Rich description from detail page", clip.Metadata["description"])
	assert.Equal(t, []string{"Hydrated Category"}, clip.Metadata["provider_categories"])
	assert.Equal(t, "https://cdn.artlist.io/hydrate-001/preview.mp4", clip.Metadata["preview_url"])
	assert.Equal(t, "unlimited", clip.Metadata["license_class"])
	assert.Equal(t, "col-123", clip.Metadata["collection_id"])
	assert.Equal(t, "Guatemala", clip.Metadata["country"])
	assert.Equal(t, "Tikal", clip.Metadata["location"])
	assert.Equal(t, 12345*time.Millisecond, clip.Duration)
	assert.Equal(t, 1920, clip.Metadata["width"])
	assert.Equal(t, 1080, clip.Metadata["height"])

	// The search term that discovered the clip is preserved separately.
	assert.Equal(t, []string{"maya temple"}, clip.Metadata["discovered_by_queries"])
	assert.Contains(t, clip.SearchTerms, "maya temple")
}

// TestSearchLiveAndSave_FallsBackWhenDetailFetcherFails verifies that a
// DetailFetcher error does not block discovery: the sparse search result is
// still saved and the run continues.
func TestSearchLiveAndSave_FallsBackWhenDetailFetcherFails(t *testing.T) {
	rec := &recordingDispatcher{}

	detailFetcher := &fakeDetailFetcher{
		err: errors.New("detail fetcher unavailable"),
	}

	svc := &Service{
		log:           zap.NewNop(),
		assetStore:    nil,
		detailFetcher: detailFetcher,
		scraperSearcher: &staticSearcher{cands: []Candidate{{
			ID:         "fallback-001",
			Title:      "Fallback Title",
			SourceRef:  "https://cdn.artlist.io/fallback-001.mp4",
			PageURL:    "https://artlist.io/stock-footage/clip/fallback-001",
			SourceName: "artlist",
			Keywords:   []string{"search", "keyword"},
		}}},
	}
	ss, err := NewSearchService(svc, rec)
	require.NoError(t, err)

	ctx := context.Background()
	resp, err := ss.SearchLiveAndSave(ctx, "maya temple", 1)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Clips, 1)

	clip := resp.Clips[0]
	assert.Equal(t, "fallback-001", clip.ID)
	assert.Equal(t, "Fallback Title", clip.Name)
	assert.Equal(t, []string{"search", "keyword"}, clip.ProviderTags)
	assert.Equal(t, []string{"maya temple"}, clip.Metadata["discovered_by_queries"])
}
