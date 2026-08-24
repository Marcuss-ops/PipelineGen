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

// TestSearchLiveAndSave_HydratesCandidatesInParallel verifies that the
// hydration phase fans out multiple detail fetches concurrently while still
// preserving the original candidate order in the returned clips.
func TestSearchLiveAndSave_HydratesCandidatesInParallel(t *testing.T) {
	rec := &recordingDispatcher{}
	release := make(chan struct{})
	started := make(chan string, 2)

	url1 := "https://artlist.io/stock-footage/clip/parallel-001"
	url2 := "https://artlist.io/stock-footage/clip/parallel-002"
	detailFetcher := &blockingDetailFetcher{
		candidateByURL: map[string]*Candidate{
			url1: {
				ID:         "parallel-001",
				Title:      "Parallel One",
				PageURL:    url1,
				SourceRef:  "https://cdn.artlist.io/parallel-001.mp4",
				Keywords:   []string{"one"},
				Categories: []string{"first"},
			},
			url2: {
				ID:         "parallel-002",
				Title:      "Parallel Two",
				PageURL:    url2,
				SourceRef:  "https://cdn.artlist.io/parallel-002.mp4",
				Keywords:   []string{"two"},
				Categories: []string{"second"},
			},
		},
		started: started,
		release: release,
	}

	svc := &Service{
		log:           zap.NewNop(),
		assetStore:    nil,
		detailFetcher: detailFetcher,
		scraperSearcher: &staticSearcher{cands: []Candidate{
			{
				ID:         "parallel-001",
				Title:      "Search One",
				SourceRef:  "https://cdn.artlist.io/parallel-001.mp4",
				PageURL:    url1,
				SourceName: "artlist",
			},
			{
				ID:         "parallel-002",
				Title:      "Search Two",
				SourceRef:  "https://cdn.artlist.io/parallel-002.mp4",
				PageURL:    url2,
				SourceName: "artlist",
			},
		}},
	}
	ss, err := NewSearchService(svc, rec)
	require.NoError(t, err)

	resultCh := make(chan struct {
		resp *SearchResponse
		err  error
	}, 1)
	go func() {
		resp, err := ss.SearchLiveAndSave(context.Background(), "maya temple", 2)
		resultCh <- struct {
			resp *SearchResponse
			err  error
		}{resp: resp, err: err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first detail fetch did not start")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("second detail fetch did not start concurrently")
	}

	close(release)
	result := <-resultCh
	require.NoError(t, result.err)
	require.NotNil(t, result.resp)
	require.Len(t, result.resp.Clips, 2)
	assert.Equal(t, "parallel-001", result.resp.Clips[0].ID)
	assert.Equal(t, "parallel-002", result.resp.Clips[1].ID)

	detailFetcher.mu.Lock()
	maxActive := detailFetcher.maxActive
	detailFetcher.mu.Unlock()
	assert.GreaterOrEqual(t, maxActive, 2, "detail fetches should overlap")
}
