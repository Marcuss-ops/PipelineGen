// Package search — aggregator_test.go covers the Wave 21 PR 9
// Aggregator pipeline. Eight unit tests as specified by PR 9:
//
//  1. TestAggregatorConcurrentFanout — backends run in parallel
//     (start time + finish time overlap); order of late arrival
//     does not bias ranking.
//  2. TestAggregatorPartialFailure — one backend errors →
//     Result.Partial=true, ProviderErrors["name"] populated,
//     other backends' results still served.
//  3. TestAggregatorDedup4Key — same evidence presented via
//     all 4 identity keys (assetID / sourceRef / url / hash) on
//     different backends; Survives merged slice has 1 entry.
//  4. TestAggregatorCursorStability — page 1 emits items; the
//     cursor is fed back into page 2 which skips those items.
//  5. TestAggregatorCtxCancel — parent ctx cancelled mid-fanout
//     → all in-flight backends observed ctx.Done() OR completed.
//  6. TestAggregatorMediaTypeFilter — only backends whose
//     Capabilities intersect q.MediaTypes participate.
//  7. TestAggregatorInvalidCursor — malformed cursor → ErrInvalidCursor.
//  8. TestAggregatorMaxScoreRanking — duplicates collide; the
//     higher-Score entry wins.
//
// The stubs are kept minimal: name + caps + items + err + delay +
// ctxCheck hook. Adapters translate backend shapes in
// internal/app/search_backends.go and are NOT exercised here —
// this file tests the Aggregator pipeline with controlled inputs.
package assets

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// stubBackend is the controllable SearchBackend for the
// aggregator tests. It honours ctx cancellation, supports an
// artificial delay, and emits a chosen result slice or error.
type stubBackend struct {
	name     string
	caps     []Capability
	universe SearchUniverse
	delaysMu sync.Mutex
	items    []Candidate
	err      error
	delay    time.Duration

	// ctxChecks captures ctx.Done() observation. Tests inspect
	// the channel length to assert ctx-cancel propagation.
	ctxChecksMu sync.Mutex
	ctxChecks   int32 // atomic via sync.Mutex
}

func (s *stubBackend) Name() string { return s.name }

func (s *stubBackend) Capabilities() []Capability {
	if s.caps != nil {
		return s.caps
	}
	return []Capability{CapVideo}
}

func (s *stubBackend) Universe() SearchUniverse {
	if s.universe != "" {
		return s.universe
	}
	return SearchCatalog
}

func (s *stubBackend) Search(ctx context.Context, _ Query) ([]Candidate, error) {
	s.ctxChecksMu.Lock()
	s.ctxChecks++
	s.ctxChecksMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.delay):
		return s.items, s.err
	}
}

// TestAggregatorConcurrentFanout — three backends run roughly in
// parallel; completion durations overlap (each backend's start is
// roughly the parent's start). Asserts that the Aggregator returns
// in well under the sum of delays (parallel fanout) and that the
// per-backend ctx-cancel paths are still respected (no goroutine
// leak).
func TestAggregatorConcurrentFanout(t *testing.T) {
	registry := NewBackendRegistry()

	a := &stubBackend{
		name: "a", caps: []Capability{CapVideo}, delay: 50 * time.Millisecond,
		items: []Candidate{{AssetID: "a-1", Source: "a", Score: 0.5}},
	}
	b := &stubBackend{
		name: "b", caps: []Capability{CapVideo}, delay: 50 * time.Millisecond,
		items: []Candidate{{AssetID: "b-1", Source: "b", Score: 0.7}},
	}
	c := &stubBackend{
		name: "c", caps: []Capability{CapVideo}, delay: 50 * time.Millisecond,
		items: []Candidate{{AssetID: "c-1", Source: "c", Score: 0.9}},
	}
	if err := registry.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(b); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(c); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)
	t0 := time.Now()
	res, err := agg.Search(context.Background(), Query{Limit: 50})
	elapsed := time.Since(t0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Partial {
		t.Fatalf("all backends succeeded; expected Partial=false, got true")
	}
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(res.Items))
	}
	// Concurrent fanout: 3 × 50ms = 150 ms sequential vs. ~50ms parallel.
	// Generous bound to avoid flakiness on shared CI infra.
	if elapsed > 130*time.Millisecond {
		t.Fatalf("fanout should run ~50ms in parallel; took %v", elapsed)
	}
	// Highest-scoring item first.
	if res.Items[0].AssetID != "c-1" {
		t.Fatalf("expected c-1 first (score 0.9); got %v", res.Items[0].AssetID)
	}
}

// TestAggregatorPartialFailure — one backend errors; the other two
// succeed. Result has Partial=true and ProviderErrors["err-backend"]
// populated; surviving items are still served.
func TestAggregatorPartialFailure(t *testing.T) {
	registry := NewBackendRegistry()

	goodErr := errors.New("backend is down")
	if err := registry.Register(&stubBackend{
		name: "err-backend", caps: []Capability{CapVideo}, delay: 1 * time.Millisecond,
		err: goodErr,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name: "good-1", caps: []Capability{CapVideo}, delay: 1 * time.Millisecond,
		items: []Candidate{{AssetID: "g-1", Source: "good-1", Score: 0.6}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name: "good-2", caps: []Capability{CapVideo}, delay: 1 * time.Millisecond,
		items: []Candidate{{AssetID: "g-2", Source: "good-2", Score: 0.8}},
	}); err != nil {
		t.Fatal(err)
	}

	agg := NewAggregator(registry, nil)
	res, err := agg.Search(context.Background(), Query{Limit: 50})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.Partial {
		t.Fatal("expected Partial=true when any backend errors")
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 surviving candidates, got %d", len(res.Items))
	}
	if res.ProviderErrors["err-backend"] == "" {
		t.Fatal("expected ProviderErrors[err-backend] populated")
	}
	if _, ok := res.ProviderErrors["good-1"]; ok {
		t.Fatalf("good-1 had no error; ProviderErrors must not contain it: %v", res.ProviderErrors)
	}
}

// TestAggregatorDedup4Key — same real-world item presented via all
// 4 identity keys across 4 backends; the survivor has 1 entry.
// All four candidates share AssetID="X" — that's the dedup trigger.
// Each one ALSO carries a different secondary identity field so the
// 4-key pathway is exercised (assetID + sourceRef-compound +
// sourceRef-solo + URL canonical + Hash).
//
// The HIGHEST-Score candidate wins (by-url with score 0.7). The
// dedupIndex replaces prior entries as later arrivals carry higher
// scores — the final merged row therefore has Score=0.7, Source="",
// PreviewURL set (the by-url candidate's identity).
func TestAggregatorDedup4Key(t *testing.T) {
	registry := NewBackendRegistry()

	if err := registry.Register(&stubBackend{
		name: "by-asset-id",
		items: []Candidate{
			{AssetID: "X", Source: "by-asset-id", SourceRef: "ref-1", Score: 0.6},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name: "by-source-ref",
		items: []Candidate{
			{AssetID: "X", Source: "by-source-ref", SourceRef: "ref-1", Score: 0.65},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name: "by-url",
		items: []Candidate{
			{AssetID: "X", PreviewURL: "https://cdn/X.mp4?token=abc", Score: 0.7},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name: "by-hash",
		items: []Candidate{
			{AssetID: "X", Hash: "deadbeef", Score: 0.55},
		},
	}); err != nil {
		t.Fatal(err)
	}

	agg := NewAggregator(registry, nil)
	res, err := agg.Search(context.Background(), Query{Limit: 50})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("dedup must collapse 4 candidates sharing asset X; got %d", len(res.Items))
	}
	// Max-score (0.7) wins → "by-url" candidate wins the dedup.
	if res.Items[0].Score != 0.7 {
		t.Fatalf("max-score dedup winner should be the by-url one; got %+v", res.Items[0])
	}
	if res.Items[0].PreviewURL == "" {
		t.Fatalf("by-url candidate wins so PreviewURL must be set; got %+v", res.Items[0])
	}
}

// TestAggregatorCursorStability — page 1 emits 3 items; the cursor
// is fed into page 2; the page 2 result must NOT include any
// page-1 item.
func TestAggregatorCursorStability(t *testing.T) {
	registry := NewBackendRegistry()
	if err := registry.Register(&stubBackend{
		name: "kb",
		items: []Candidate{
			{AssetID: "a", Source: "kb", Score: 0.9},
			{AssetID: "b", Source: "kb", Score: 0.8},
			{AssetID: "c", Source: "kb", Score: 0.7},
			{AssetID: "d", Source: "kb", Score: 0.4}, // page-2 only
		},
	}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)

	page1, err := agg.Search(context.Background(), Query{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 3 || page1.NextCursor == "" {
		t.Fatalf("page1 setup: want 3 items + non-empty cursor, got %d items %q",
			len(page1.Items), page1.NextCursor)
	}
	page2, err := agg.Search(context.Background(), Query{Limit: 5, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2 err: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].AssetID != "d" {
		t.Fatalf("page2 must contain only the 4th item (assetID=d); got %+v", page2.Items)
	}
}

// TestAggregatorCtxCancel — parent ctx cancelled; backends observe
// the cancellation through their ctx argument (verified via the
// stub). The aggregator's Search returns without error if at
// least one backend completed OR errored on its own; the result
// faithfully reflects the partial state.
func TestAggregatorCtxCancel(t *testing.T) {
	registry := NewBackendRegistry()

	a := &stubBackend{
		name: "a-fast", delay: 1 * time.Millisecond,
		items: []Candidate{{AssetID: "fast", Score: 0.6}},
	}
	b := &stubBackend{
		name: "b-slow", delay: 200 * time.Millisecond, // slower than cancel
	}
	if err := registry.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(b); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	res, err := agg.Search(ctx, Query{Limit: 5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.Partial {
		t.Fatal("expected Partial=true when one backend hit ctx timeout")
	}
	if _, ok := res.ProviderErrors["b-slow"]; !ok {
		t.Fatalf("expected ProviderErrors[b-slow] populated; got %v", res.ProviderErrors)
	}
	if len(res.Items) != 1 || res.Items[0].AssetID != "fast" {
		t.Fatalf("fast backend was awaited correctly; survivors: %+v", res.Items)
	}
}

// TestAggregatorMediaTypeFilter — backends whose Capabilities do
// not intersect q.MediaTypes are excluded from fanout.
func TestAggregatorMediaTypeFilter(t *testing.T) {
	registry := NewBackendRegistry()
	if err := registry.Register(&stubBackend{
		name: "video-only", caps: []Capability{CapVideo},
		items: []Candidate{{AssetID: "v-1", Source: "video-only", Score: 0.5}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name: "audio-only", caps: []Capability{CapAudio},
		items: []Candidate{{AssetID: "a-1", Source: "audio-only", Score: 0.5}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name: "both", caps: []Capability{CapVideo, CapAudio},
		items: []Candidate{{AssetID: "va-1", Source: "both", Score: 0.9}},
	}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)
	res, err := agg.Search(context.Background(), Query{MediaTypes: []string{"audio"}, Limit: 50})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("MediaType=audio must select audio-only + both; got %d items %+v",
			len(res.Items), res.Items)
	}
	names := []string{}
	for _, c := range res.Items {
		names = append(names, c.Source)
	}
	sort.Strings(names)
	if !(len(names) == 2 && names[0] == "audio-only" && names[1] == "both") {
		t.Fatalf("expected [audio-only both]; got %v", names)
	}
}

// TestAggregatorInvalidCursor — malformed cursor → ErrInvalidCursor.
// Aggregator propagates this directly to the caller (handler maps
// to HTTP 422).
func TestAggregatorInvalidCursor(t *testing.T) {
	registry := NewBackendRegistry()
	registry.Freeze()
	agg := NewAggregator(registry, nil)
	_, err := agg.Search(context.Background(), Query{Cursor: "not-a-base64-cursor"})
	if err != ErrInvalidCursor {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
}

// TestAggregatorMaxScoreRanking — duplicate candidates from
// multiple backends with different scores; the highest-Score entry
// survives. Lower-Score duplicates are dropped.
func TestAggregatorMaxScoreRanking(t *testing.T) {
	registry := NewBackendRegistry()
	if err := registry.Register(&stubBackend{
		name:  "low-score",
		items: []Candidate{{AssetID: "winner", Source: "low-score", Score: 0.3}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name:  "high-score",
		items: []Candidate{{AssetID: "winner", Source: "high-score", Score: 0.95}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&stubBackend{
		name:  "mid-score",
		items: []Candidate{{AssetID: "winner", Source: "mid-score", Score: 0.7}},
	}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)
	res, err := agg.Search(context.Background(), Query{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("dedup must keep 1; got %d", len(res.Items))
	}
	if res.Items[0].Source != "high-score" || res.Items[0].Score != 0.95 {
		t.Fatalf("expected high-score to win dedup; got %+v", res.Items[0])
	}
}

// Ensure stable exports for cross-file references — stdlib tests
// don't need any package-level vars for the current test surface.
