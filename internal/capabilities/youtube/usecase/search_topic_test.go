package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// CPR-CC-6 Phase 2 (June 2026): moved from internal/application/youtube/.
// Scorers live in the search package; the tests run against the canonical
// scoring constants without needing an explicit package qualifier.

// ── Limit-contract stubs ────────────────────────────────────────────────

// stubTopicSearchRunner is a deterministic floor for the topic-search limit
// contract. It honours the requested limit exactly like the infrastructure
// adapter does — TopicSearch passes limit*2 to over-fetch for the
// publishedAfter filter — so a response that exceeds the caller's limit can
// only be the use case's doing, never the port returning more than asked.
// requestedLimits records what was asked for, so the over-fetch itself stays
// an asserted behaviour rather than an assumption.
type stubTopicSearchRunner struct {
	corpus          []youtubeports.SearchLiveResult
	requestedLimits []int
}

func (s *stubTopicSearchRunner) SearchLive(_ context.Context, _ string, limit int, _ string) ([]youtubeports.SearchLiveResult, error) {
	s.requestedLimits = append(s.requestedLimits, limit)
	if limit > 0 && len(s.corpus) > limit {
		return s.corpus[:limit], nil
	}
	return s.corpus, nil
}

func (s *stubTopicSearchRunner) GetVideoInfo(_ context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	return &youtubeports.DownloaderMetadata{ID: topicVideoID(videoURL), URL: videoURL}, nil
}

// stubTopicMetaFetcher satisfies the Service-level metadata port that
// enrichTopicResult reads through (Service.GetVideoInfo → metaFetcher).
// A non-nil pointer keeps portutil.IsNilPort false, matching a wired deploy.
type stubTopicMetaFetcher struct{}

func (*stubTopicMetaFetcher) GetVideoMetadata(_ context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	return &youtubeports.DownloaderMetadata{
		ID:         topicVideoID(videoURL),
		URL:        videoURL,
		Title:      "Denzel Washington Interview",
		Uploader:   "Graham Bensinger",
		Duration:   900,
		UploadDate: "20240601",
		ViewCount:  1000,
	}, nil
}

// topicVideoID mirrors the adapter's watch-URL shape back to the video id.
func topicVideoID(videoURL string) string {
	return strings.TrimPrefix(videoURL, "https://www.youtube.com/watch?v=")
}

// TestTopicSearchCapsResultsAtLimit pins the response contract of
// GET /api/clips/search: `limit` is what the caller asked for and
// TopicSearchResponse.Limit restates it, so the returned page may never carry
// more rows than that. The publishedAfter filter needs the limit*2 over-fetch,
// which must stay an internal detail — leaking it doubles the page size for
// every caller and made Count disagree with the advertised limit.
func TestTopicSearchCapsResultsAtLimit(t *testing.T) {
	const (
		limit      = 3
		corpusSize = 12
	)

	corpus := make([]youtubeports.SearchLiveResult, 0, corpusSize)
	for i := 0; i < corpusSize; i++ {
		id := fmt.Sprintf("vid%02d", i)
		corpus = append(corpus, youtubeports.SearchLiveResult{
			ID:       id,
			Title:    "Denzel Washington Interview",
			URL:      "https://www.youtube.com/watch?v=" + id,
			Uploader: "Graham Bensinger",
			Duration: 900,
		})
	}

	runner := &stubTopicSearchRunner{corpus: corpus}
	svc := &Service{
		log:         zap.NewNop(),
		search:      NewSearchService(SearchDeps{SearchRunner: runner, Log: zap.NewNop()}),
		metaFetcher: &stubTopicMetaFetcher{},
	}

	resp, err := svc.TopicSearch(context.Background(), "Denzel Washington Interview", limit, "", "")
	if err != nil {
		t.Fatalf("TopicSearch: %v", err)
	}

	if got := len(resp.Results); got > limit {
		t.Fatalf("TopicSearch returned %d results for limit=%d: the limit*2 over-fetch leaked into the response", got, limit)
	}
	if resp.Count != len(resp.Results) {
		t.Errorf("Count %d must match len(Results) %d", resp.Count, len(resp.Results))
	}
	if resp.Limit != limit {
		t.Errorf("Limit %d must echo the requested %d", resp.Limit, limit)
	}
	if len(runner.requestedLimits) != 1 || runner.requestedLimits[0] != limit*2 {
		t.Errorf("expected a single upstream call with the limit*2 over-fetch (%d), got %v", limit*2, runner.requestedLimits)
	}

	// The corpus is larger than the limit, so an empty page would mean the
	// assertion above passed vacuously (e.g. enrichment silently dropped every
	// candidate). Require the page to actually be filled to the limit.
	if len(resp.Results) == 0 {
		t.Fatal("expected a non-empty page: enrichment dropped every candidate, so the cap was never exercised")
	}
}

func TestTopicSearchHonorsRequestedSortAndDateFilter(t *testing.T) {
	corpus := []youtubeports.SearchLiveResult{
		{ID: "old", Title: "Denzel Washington Interview", URL: "https://www.youtube.com/watch?v=old"},
		{ID: "new-low", Title: "Denzel Washington Interview", URL: "https://www.youtube.com/watch?v=new-low"},
		{ID: "new-high-views", Title: "Denzel Washington Interview", URL: "https://www.youtube.com/watch?v=new-high-views"},
	}
	meta := &sortMetadataFetcher{byID: map[string]*youtubeports.DownloaderMetadata{
		"old":            {ID: "old", Title: "Denzel Washington Interview", UploadDate: "20240101", ViewCount: 9000, Duration: 90},
		"new-low":        {ID: "new-low", Title: "Denzel Washington Interview", UploadDate: "20250101", ViewCount: 10, Duration: 50},
		"new-high-views": {ID: "new-high-views", Title: "Denzel Washington Interview", UploadDate: "20250601", ViewCount: 500, Duration: 70},
	}}
	runner := &stubTopicSearchRunner{corpus: corpus}
	svc := &Service{log: zap.NewNop(), search: NewSearchService(SearchDeps{SearchRunner: runner, Log: zap.NewNop()}), metaFetcher: meta}
	resp, err := svc.TopicSearch(context.Background(), "Denzel Washington Interview", 5, "views", "2025-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 || resp.Results[0].VideoID != "new-high-views" || resp.Results[1].VideoID != "new-low" {
		t.Fatalf("results=%+v, want only post-date videos ordered by view count", resp.Results)
	}
}

type sortMetadataFetcher struct {
	byID map[string]*youtubeports.DownloaderMetadata
}

func (f *sortMetadataFetcher) GetVideoMetadata(_ context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	return f.byID[topicVideoID(videoURL)], nil
}

func TestScoreTopicSimilarityPrefersExactTopicMatch(t *testing.T) {
	meta := &youtubeports.DownloaderMetadata{
		Title:      "Denzel Washington Interview with Graham Bensinger",
		Uploader:   "Graham Bensinger",
		Duration:   14.5,
		UploadDate: "20240601",
		Tags:       []string{"denzel", "washington", "interview"},
	}

	if got := scoreTopicSimilarity("Denzel Washington Interview", meta); got < 90 {
		t.Fatalf("expected strong similarity score, got %d", got)
	}
	if got := scoreFormatMatch("Denzel Washington Interview", meta); got < 90 {
		t.Fatalf("expected strong format score, got %d", got)
	}
}

func TestScoreTopicSimilarityRewardsPartialOverlap(t *testing.T) {
	meta := &youtubeports.DownloaderMetadata{
		Title:    "Denzel Washington on Acting",
		Uploader: "Movie Times",
	}

	if got := scoreTopicSimilarity("Denzel Washington Interview", meta); got >= 90 {
		t.Fatalf("expected partial overlap, got %d", got)
	}
}
