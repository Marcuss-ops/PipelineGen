package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type discoveryContractBackend struct {
	query Query
	items []Candidate
}

func (b *discoveryContractBackend) Name() string { return "youtube" }
func (b *discoveryContractBackend) Capabilities() []Capability {
	return []Capability{CapVideo}
}
func (b *discoveryContractBackend) Universe() SearchUniverse { return SearchDiscovery }
func (b *discoveryContractBackend) Search(_ context.Context, q Query) ([]Candidate, error) {
	b.query = q
	return b.items, nil
}

func runMediaSearchHandler(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/media"))
	req := httptest.NewRequest(http.MethodPost, "/api/media/search", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestMediaSearchDiscoveryControlsReachBackend(t *testing.T) {
	publishedAt := time.Date(2025, 1, 2, 1, 4, 5, 0, time.UTC)
	backend := &discoveryContractBackend{items: []Candidate{{
		AssetID: "yt-1", Source: "youtube", SourceRef: "video-1", Score: 0.8, PublishedAt: &publishedAt,
	}}}
	registry := NewBackendRegistry()
	if err := registry.Register(backend); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	handler := NewHandler(NewAggregator(registry, nil), nil, zap.NewNop())

	rec := runMediaSearchHandler(t, handler, `{
		"query":"boxing",
		"sources":["youtube"],
		"universe":"discovery",
		"filters":{"sort":"newest","published_after":"2025-01-02T03:04:05+02:00"},
		"min_score":0.7,
		"limit":5
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []Candidate `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].AssetID != "yt-1" {
		t.Fatalf("items=%+v, want matching discovery item", payload.Items)
	}
	q := backend.query
	if q.EffectiveUniverse() != SearchDiscovery || len(q.Sources) != 1 || q.Sources[0] != "youtube" {
		t.Fatalf("discovery selection not forwarded: %+v", q)
	}
	if q.Filters.Sort != "newest" || q.MinScore != 0.7 {
		t.Fatalf("sort/min_score not forwarded: %+v", q)
	}
	wantDate := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("+02", 2*60*60))
	if q.Filters.PublishedAfter == nil || !q.Filters.PublishedAfter.Equal(wantDate) {
		t.Fatalf("published_after=%v, want %v", q.Filters.PublishedAfter, wantDate)
	}
}

func TestMediaSearchMinScoreFiltersMergedResults(t *testing.T) {
	registry := NewBackendRegistry()
	backend := &discoveryContractBackend{items: []Candidate{
		{AssetID: "high", Source: "youtube", Score: 0.75},
		{AssetID: "low", Source: "youtube", Score: 0.25},
	}}
	if err := registry.Register(backend); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	handler := NewHandler(NewAggregator(registry, nil), nil, zap.NewNop())
	rec := runMediaSearchHandler(t, handler, `{"query":"boxing","universe":"discovery","min_score":0.5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []Candidate `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].AssetID != "high" {
		t.Fatalf("score floor returned %+v, want only high-scoring candidate", payload.Items)
	}
}

func TestMediaSearchPublishedAfterFiltersAllSourcesAndSortsCandidates(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	backend := &discoveryContractBackend{items: []Candidate{
		{AssetID: "old", Source: "youtube", Score: 1, PublishedAt: timePtr(base.Add(-24 * time.Hour)), ViewCount: 10, DurationMs: 10_000},
		{AssetID: "newer-low-score", Source: "youtube", Score: 0.2, PublishedAt: timePtr(base.Add(24 * time.Hour)), ViewCount: 200, DurationMs: 20_000},
		{AssetID: "newest", Source: "youtube", Score: 0.1, PublishedAt: timePtr(base.Add(48 * time.Hour)), ViewCount: 500, DurationMs: 30_000},
		{AssetID: "unknown-date", Source: "youtube", Score: 0.9, ViewCount: 1000},
	}}
	registry := NewBackendRegistry()
	if err := registry.Register(backend); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	handler := NewHandler(NewAggregator(registry, nil), nil, zap.NewNop())
	body := `{"query":"boxing","sources":["youtube"],"universe":"discovery","filters":{"sort":"newest","published_after":"2025-01-01T00:00:00Z"}}`
	rec := runMediaSearchHandler(t, handler, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []Candidate `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 || payload.Items[0].AssetID != "newest" || payload.Items[1].AssetID != "newer-low-score" {
		t.Fatalf("items=%+v, want newest-first survivors (old and unknown dates excluded)", payload.Items)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestMediaSearchRejectsInvalidDiscoveryControls(t *testing.T) {
	registry := NewBackendRegistry()
	if err := registry.Register(&discoveryContractBackend{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	handler := NewHandler(NewAggregator(registry, nil), nil, zap.NewNop())
	cases := []struct{ name, body string }{
		{"unknown sort", `{"query":"x","filters":{"sort":"popularity"}}`},
		{"unknown universe", `{"query":"x","universe":"everything"}`},
		{"malformed json", `{"query":`},
		{"invalid date type", `{"query":"x","filters":{"published_after":42}}`},

		{"negative min score", `{"query":"x","min_score":-0.1}`},
		{"min score over one", `{"query":"x","min_score":1.1}`},
		{"malformed published after", `{"query":"x","filters":{"published_after":"yesterday"}}`},
		{"invalid universe", `{"query":"x","universe":"unknown"}`},
		{"sort without discovery", `{"query":"x","filters":{"sort":"newest"}}`},
		{"date without YouTube discovery", `{"query":"x","universe":"catalog","sources":["youtube"],"filters":{"published_after":"2025-01-01T00:00:00Z"}}`},
		{"invalid discovery date shape", `{"query":"x","universe":"discovery","sources":["youtube"],"filters":{"published_after":42}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := runMediaSearchHandler(t, handler, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}
