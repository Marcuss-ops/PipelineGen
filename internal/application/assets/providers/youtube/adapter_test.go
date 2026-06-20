package youtube

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	youtubesrc "github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
)

// ── Test double ─────────────────────────────────────────────────────

// fakeSearcher implements the private searcher interface so adapter
// tests can run without spinning up a full *youtubesrc.Service (heavy
// scraper + repository chain).
type fakeSearcher struct {
	lastCall struct {
		Query          string
		Limit          int
		SortMode       string
		PublishedAfter string
	}
	resp *youtubesrc.TopicSearchResponse
	err  error
}

func (f *fakeSearcher) SearchByTopicWithFilter(
	_ context.Context,
	query string,
	limit int,
	sortMode, publishedAfter string,
) (*youtubesrc.TopicSearchResponse, error) {
	f.lastCall.Query = query
	f.lastCall.Limit = limit
	f.lastCall.SortMode = sortMode
	f.lastCall.PublishedAfter = publishedAfter
	return f.resp, f.err
}

func newAdapterWith(s searcher) *Adapter { return &Adapter{src: s} }

// ── Tests ──────────────────────────────────────────────────────────

func TestAdapter_Name(t *testing.T) {
	a := newAdapterWith(&fakeSearcher{})
	if got := a.Name(); got != "youtube" {
		t.Fatalf("Name()=%q, want \"youtube\"", got)
	}
}

func TestAdapter_Capabilities(t *testing.T) {
	a := newAdapterWith(&fakeSearcher{})
	caps := a.Capabilities()
	if !slices.Contains(caps, providers.CapabilitySearch) {
		t.Errorf("Capabilities() missing CapabilitySearch: %v", caps)
	}
	if !slices.Contains(caps, providers.CapabilityVideo) {
		t.Errorf("Capabilities() missing CapabilityVideo: %v", caps)
	}
	if slices.Contains(caps, providers.CapabilityFetch) {
		t.Errorf("Capabilities() must NOT declare CapabilityFetch (download lives in channel-monitor): %v", caps)
	}
}

func TestSearch_NilInterfaceSource(t *testing.T) {
	a := &Adapter{} // src == nil interface value
	got, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, ErrSourceNotWired) {
		t.Fatalf("expected ErrSourceNotWired, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil candidates, got %v", got)
	}
}

func TestSearch_TypedNilSource(t *testing.T) {
	// Mirror the registry's typed-nil convention: a non-nil
	// interface holding a nil concrete pointer must also be
	// rejected.
	a := &Adapter{src: (*fakeSearcher)(nil)}
	got, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, ErrSourceNotWired) {
		t.Fatalf("expected ErrSourceNotWired on typed-nil, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil candidates, got %v", got)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	a := newAdapterWith(&fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}})
	got, err := a.Search(context.Background(), providers.SearchRequest{Query: ""})
	if err == nil {
		t.Fatalf("expected error on empty query, got nil (results=%v)", got)
	}
	if got != nil {
		t.Errorf("expected nil candidates on empty query, got %v", got)
	}
}

func TestSearch_LimitClamping(t *testing.T) {
	cases := []struct {
		name    string
		inLimit int
		want    int
	}{
		{"zero clamped to default 10", 0, 10},
		{"negative clamped to default 10", -3, 10},
		{"over 50 clamped to 50", 100, 50},
		{"exactly 50 preserved", 50, 50},
		{"positive in-range preserved", 7, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
			a := newAdapterWith(stub)
			_, err := a.Search(context.Background(), providers.SearchRequest{
				Query: "x",
				Limit: tc.inLimit,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stub.lastCall.Limit != tc.want {
				t.Errorf("forwarded Limit=%d, want %d", stub.lastCall.Limit, tc.want)
			}
		})
	}
}

func TestSearch_SortModeMapping(t *testing.T) {
	cases := []struct {
		name     string
		inSort   providers.SortMode
		wantSort string
	}{
		{"empty maps to native default", "", ""},
		{"SortByRelevance maps to native default", providers.SortByRelevance, ""},
		{"SortByNewest passes through", providers.SortByNewest, "newest"},
		{"SortByOldest passes through", providers.SortByOldest, "oldest"},
		{"SortByLongest passes through", providers.SortByLongest, "longest"},
		{"SortByShortest passes through", providers.SortByShortest, "shortest"},
		{"unknown value passes through verbatim", providers.SortMode("custom"), "custom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
			a := newAdapterWith(stub)
			_, err := a.Search(context.Background(), providers.SearchRequest{
				Query:   "x",
				Limit:   5,
				Filters: providers.SearchFilters{Sort: tc.inSort},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stub.lastCall.SortMode != tc.wantSort {
				t.Errorf("forwarded SortMode=%q, want %q", stub.lastCall.SortMode, tc.wantSort)
			}
		})
	}
}

func TestSearch_PublishedAfterForwarding(t *testing.T) {
	t.Run("nil filters publishedAfter forwarded as empty string", func(t *testing.T) {
		stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
		a := newAdapterWith(stub)
		_, err := a.Search(context.Background(), providers.SearchRequest{
			Query: "x",
			Limit: 5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stub.lastCall.PublishedAfter != "" {
			t.Errorf("expected empty PublishedAfter, got %q", stub.lastCall.PublishedAfter)
		}
	})

	t.Run("non-nil filters publishedAfter forwarded as RFC3339 UTC", func(t *testing.T) {
		want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
		stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
		a := newAdapterWith(stub)
		_, err := a.Search(context.Background(), providers.SearchRequest{
			Query: "x",
			Limit: 5,
			Filters: providers.SearchFilters{
				PublishedAfter: &want,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stub.lastCall.PublishedAfter == "" {
			t.Fatalf("expected non-empty PublishedAfter, got empty")
		}
		parsed, err := time.Parse(time.RFC3339, stub.lastCall.PublishedAfter)
		if err != nil {
			t.Fatalf("forwarded PublishedAfter %q is not RFC3339: %v", stub.lastCall.PublishedAfter, err)
		}
		if !parsed.Equal(want) {
			t.Errorf("forwarded PublishedAfter=%v want %v", parsed, want)
		}
	})

	t.Run("non-UTC time is normalised to UTC before encoding", func(t *testing.T) {
		// 03:04:05 in a +02:00 zone == 01:04:05 UTC.
		loc := time.FixedZone("plus02", 2*60*60)
		in := time.Date(2025, 6, 7, 3, 4, 5, 0, loc)
		want := in.UTC()
		stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
		a := newAdapterWith(stub)
		_, _ = a.Search(context.Background(), providers.SearchRequest{
			Query: "x", Limit: 5,
			Filters: providers.SearchFilters{PublishedAfter: &in},
		})
		parsed, err := time.Parse(time.RFC3339, stub.lastCall.PublishedAfter)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if !parsed.Equal(want) {
			t.Errorf("forwarded=%v want %v", parsed, want)
		}
	})
}

func TestSearch_CandidateTranslation(t *testing.T) {
	stub := &fakeSearcher{
		resp: &youtubesrc.TopicSearchResponse{
			Results: []youtubesrc.TopicSearchResult{
				{
					VideoID:            "v123abc",
					Title:              "An interesting video",
					ChannelName:        "Channel X",
					ThumbnailURL:       "https://example.com/thumb.jpg",
					ViewCount:          9000,
					UploadDate:         "20250101",
					Duration:           123.5, // seconds
					SimilarityScore:    80,
					FormatMatchPercent: 60,
					DirectLink:         "https://www.youtube.com/watch?v=v123abc",
				},
			},
		},
	}
	a := newAdapterWith(stub)
	got, err := a.Search(context.Background(), providers.SearchRequest{Query: "x", Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	c := got[0]

	if c.SourceName != "youtube" {
		t.Errorf("SourceName=%q want \"youtube\"", c.SourceName)
	}
	if c.SourceRef != "v123abc" {
		t.Errorf("SourceRef=%q want \"v123abc\"", c.SourceRef)
	}
	if c.Title != "An interesting video" {
		t.Errorf("Title=%q", c.Title)
	}
	if c.PreviewURL != "https://www.youtube.com/watch?v=v123abc" {
		t.Errorf("PreviewURL=%q", c.PreviewURL)
	}
	if c.ThumbnailURL != "https://example.com/thumb.jpg" {
		t.Errorf("ThumbnailURL=%q", c.ThumbnailURL)
	}
	if c.MediaType != youtubeMediaType {
		t.Errorf("MediaType=%q want %q", c.MediaType, youtubeMediaType)
	}
	wantDur := time.Duration(123.5 * float64(time.Second))
	if c.Duration != wantDur {
		t.Errorf("Duration=%v want %v", c.Duration, wantDur)
	}
	if c.PublishedAt == nil {
		t.Errorf("expected non-nil PublishedAt (parsed from YYYYMMDD)")
	}
	// combinedScore: 80*70 + 60*30 = 5600 + 1800 = 7400 → 0.74.
	if c.Score != 0.74 {
		t.Errorf("Score=%v want 0.74", c.Score)
	}
}

func TestSearch_CandidateTranslation_UnparseableUploadDateIsNil(t *testing.T) {
	stub := &fakeSearcher{
		resp: &youtubesrc.TopicSearchResponse{
			Results: []youtubesrc.TopicSearchResult{
				{
					VideoID:    "v1",
					Duration:   10,
					UploadDate: "not-a-date",
				},
			},
		},
	}
	a := newAdapterWith(stub)
	got, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].PublishedAt != nil {
		t.Errorf("expected nil PublishedAt on unparseable upload_date, got %v", got[0].PublishedAt)
	}
}

func TestSearch_NilResponseReturnsNilCandidates(t *testing.T) {
	stub := &fakeSearcher{resp: nil}
	a := newAdapterWith(stub)
	got, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil candidates on nil response, got %v", got)
	}
}

func TestSearch_PropagatesUpstreamError(t *testing.T) {
	sentinel := errors.New("upstream failure")
	stub := &fakeSearcher{err: sentinel}
	a := newAdapterWith(stub)
	got, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err == nil {
		t.Fatalf("expected error propagating upstream failure")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) true; got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil candidates on error, got %v", got)
	}
}

func TestFetch_ReturnsPlainError_NoSentinel(t *testing.T) {
	a := newAdapterWith(&fakeSearcher{})
	got, err := a.Fetch(context.Background(), providers.FetchRequest{SourceRef: "v123"})
	if err == nil {
		t.Fatal("expected error from Fetch on a source that declares no CapabilityFetch")
	}
	if got != nil {
		t.Errorf("expected nil FetchedAsset, got %v", got)
	}
	if err.Error() == "" {
		t.Errorf("expected descriptive error message")
	}
	// Per PR 3E: there is NO sentinel for "fetch unsupported".
	// Switching on err.Error() must not be the supported approach.
}

func TestNewAdapter_NilService_SearchReturnsErrSourceNotWired(t *testing.T) {
	a := NewAdapter(nil)
	if a.Name() != "youtube" {
		t.Errorf("Name mismatch")
	}
	if _, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"}); !errors.Is(err, ErrSourceNotWired) {
		t.Errorf("expected ErrSourceNotWired, got %v", err)
	}
}
