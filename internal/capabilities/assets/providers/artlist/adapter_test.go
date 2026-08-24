package assets

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Test double ─────────────────────────────────────────────────────

// fakeSearcher implements the private searcher interface so adapter
// tests can run without a full *Service (heavy scraper +
// repository + config chain).
type fakeSearcher struct {
	lastCall struct {
		Term  string
		Limit int
	}
	resp *SearchResponse
	err  error
}

func (f *fakeSearcher) Search(_ context.Context, req *SearchRequest) (*SearchResponse, error) {
	f.lastCall.Term = req.Term
	f.lastCall.Limit = req.Limit
	return f.resp, f.err
}

func newAdapterWith(s searcher) *Adapter { return &Adapter{src: s} }

// ── Compile-time + capability checks ──────────────────────────────

func TestAdapter_Name(t *testing.T) {
	a := &Adapter{}
	if got := a.Name(); got != "artlist" {
		t.Fatalf("Name()=%q, want \"artlist\"", got)
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
	if !slices.Contains(caps, providers.CapabilityMusic) {
		t.Errorf("Capabilities() missing CapabilityMusic: %v", caps)
	}
	if slices.Contains(caps, providers.CapabilityFetch) {
		t.Errorf("Capabilities() must NOT declare CapabilityFetch (no public fetch binary path): %v", caps)
	}
}

func TestAdapter_DoesNotImplementFetchProvider(t *testing.T) {
	var sp providers.SearchProvider = (*Adapter)(nil)
	if _, ok := sp.(providers.FetchProvider); ok {
		t.Fatal("artlist Adapter must NOT satisfy FetchProvider")
	}
}

// ── Nil-source handling ──────────────────────────────────────────

func TestSearch_NilSource(t *testing.T) {
	a := &Adapter{} // src == nil interface
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, ErrSourceNotWired) {
		t.Fatalf("expected ErrSourceNotWired, got %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates on nil source, got %v", res.Candidates)
	}
	if res.NextPageToken != "" {
		t.Errorf("expected empty NextPageToken, got %q", res.NextPageToken)
	}
}

func TestNewAdapter_NilService_SearchReturnsErrSourceNotWired(t *testing.T) {
	a := NewAdapter(nil)
	if a.Name() != "artlist" {
		t.Errorf("Name mismatch: got %q", a.Name())
	}
	if _, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"}); !errors.Is(err, ErrSourceNotWired) {
		t.Errorf("expected ErrSourceNotWired, got %v", err)
	}
}

// ── Round-trip: SearchRequest → SearchResult → Candidate ─────────

func TestSearch_EmptyQuery_ForwardedAsIs(t *testing.T) {
	// Artlist does not enforce non-empty query at the adapter boundary;
	// the underlying service owns validation.
	stub := &fakeSearcher{resp: &SearchResponse{}}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Candidates == nil {
		t.Fatalf("expected non-nil candidates, got nil")
	}
	if stub.lastCall.Term != "" {
		t.Errorf("forwarded Term=%q, want empty string", stub.lastCall.Term)
	}
}

func TestSearch_LimitForwardedCorrectly(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		want  int
	}{
		{"zero is forwarded", 0, 0},
		{"positive is forwarded", 25, 25},
		{"negative is forwarded", -5, -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fakeSearcher{resp: &SearchResponse{}}
			a := newAdapterWith(stub)
			_, err := a.Search(context.Background(), providers.SearchRequest{
				Query: "test",
				Limit: tc.limit,
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

func TestSearch_CandidateTranslation_RoundTrip(t *testing.T) {
	stub := &fakeSearcher{
		resp: &SearchResponse{
			Clips: []asset.Asset{
				{
					ID:           "artlist_abc123",
					Name:         "Cinematic Drone Shot",
					ClipPageURL:  "https://artlist.io/clip/abc123",
					ThumbnailURL: "https://cdn.artlist.io/thumb_abc123.jpg",
					MediaType:    asset.MediaType("video"),
					Duration:     time.Duration(15 * float64(time.Second)),
				},
				{
					ID:           "artlist_def456",
					Name:         "Ambient Music Loop",
					ClipPageURL:  "https://artlist.io/clip/def456",
					ThumbnailURL: "https://cdn.artlist.io/thumb_def456.jpg",
					MediaType:    asset.MediaType("music"),
					Duration:     time.Duration(120 * float64(time.Second)),
				},
			},
		},
	}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "cinematic", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(res.Candidates))
	}
	if res.NextPageToken != "" {
		t.Errorf("NextPageToken should be empty for artlist, got %q", res.NextPageToken)
	}

	// First candidate
	c0 := res.Candidates[0]
	if c0.SourceName != "artlist" {
		t.Errorf("candidate[0].SourceName=%q, want \"artlist\"", c0.SourceName)
	}
	if c0.SourceRef != "https://artlist.io/clip/abc123" {
		t.Errorf("candidate[0].SourceRef=%q", c0.SourceRef)
	}
	if c0.Title != "Cinematic Drone Shot" {
		t.Errorf("candidate[0].Title=%q", c0.Title)
	}
	if c0.PreviewURL != "https://artlist.io/clip/abc123" {
		t.Errorf("candidate[0].PreviewURL=%q", c0.PreviewURL)
	}
	if c0.ThumbnailURL != "https://cdn.artlist.io/thumb_abc123.jpg" {
		t.Errorf("candidate[0].ThumbnailURL=%q", c0.ThumbnailURL)
	}
	if c0.MediaType != asset.MediaType("video") {
		t.Errorf("candidate[0].MediaType=%q", c0.MediaType)
	}
	if c0.Duration != 15*time.Second {
		t.Errorf("candidate[0].Duration=%v", c0.Duration)
	}
	if c0.PublishedAt != nil {
		t.Errorf("candidate[0].PublishedAt should be nil (artlist has no publish time), got %v", c0.PublishedAt)
	}
	if c0.Score != 0 {
		t.Errorf("candidate[0].Score=%v want 0 (artlist adapter returns raw score 0)", c0.Score)
	}

	// Second candidate — music
	c1 := res.Candidates[1]
	if c1.SourceName != "artlist" {
		t.Errorf("candidate[1].SourceName=%q", c1.SourceName)
	}
	if c1.MediaType != asset.MediaType("music") {
		t.Errorf("candidate[1].MediaType=%q", c1.MediaType)
	}
	if c1.Duration != 120*time.Second {
		t.Errorf("candidate[1].Duration=%v", c1.Duration)
	}
}

func TestSearch_NilResponseReturnsNilCandidates(t *testing.T) {
	stub := &fakeSearcher{resp: nil}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates on nil response, got %v", res.Candidates)
	}
	if res.NextPageToken != "" {
		t.Errorf("expected empty NextPageToken, got %q", res.NextPageToken)
	}
}

func TestSearch_PropagatesUpstreamError(t *testing.T) {
	sentinel := errors.New("artlist upstream failure")
	stub := &fakeSearcher{err: sentinel}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err == nil {
		t.Fatalf("expected error propagating upstream failure")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) true; got %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates on error, got %v", res.Candidates)
	}
}
