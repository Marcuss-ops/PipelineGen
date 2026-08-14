// Package search — aggregator_cursor_test.go pins the T4 cursor
// pagination fix contract: when a cursor is present, backends
// receive an effective limit of (pageLimit + skipSetSize) so that
// after skip-set dedup removes previously-served items, there are
// still enough candidates to fill the page.
//
// The test uses a limitCapturingBackend that records the Query.Limit
// it received, plus returns enough distinct items to fill pages.
package search

import (
	"context"
	"sync"
	"testing"
)

// limitCapturingBackend records the effective limit the Aggregator
// passed via q.Limit. It returns `totalItems` distinct candidates
// so the backend has enough items for both page 1 and page 2.
type limitCapturingBackend struct {
	name       string
	totalItems []Candidate

	mu         sync.Mutex
	seenLimits []int
}

func (b *limitCapturingBackend) Name() string { return b.name }

func (b *limitCapturingBackend) Capabilities() []Capability {
	return []Capability{CapVideo}
}

func (b *limitCapturingBackend) Universe() SearchUniverse { return SearchCatalog }

func (b *limitCapturingBackend) Search(_ context.Context, q Query) ([]Candidate, error) {
	b.mu.Lock()
	b.seenLimits = append(b.seenLimits, q.Limit)
	b.mu.Unlock()

	limit := q.Limit
	if limit > len(b.totalItems) {
		limit = len(b.totalItems)
	}
	out := make([]Candidate, limit)
	copy(out, b.totalItems[:limit])
	return out, nil
}

// TestAggregatorCursorPagination_BackendLimitIncreased verifies the
// T4 fix: when a cursor is present, the Aggregator bumps q.Limit
// by len(skipSet) so backends fetch enough rows to fill the page
// after skip-set dedup.
//
// Without the fix: page 2 backend limit = 2 (same as page 1),
// backend returns the same 2 items, skip-set removes them, page 2
// is empty.
//
// With the fix: page 2 backend limit = 2 + 2 (skip-set size) = 4,
// backend returns 4 items, skip-set removes 2, page 2 gets 2 items.
func TestAggregatorCursorPagination_BackendLimitIncreased(t *testing.T) {
	registry := NewBackendRegistry()

	// 6 distinct items with descending scores so ordering is stable.
	items := []Candidate{
		{AssetID: "a1", Source: "s", Score: 0.95},
		{AssetID: "a2", Source: "s", Score: 0.90},
		{AssetID: "a3", Source: "s", Score: 0.85},
		{AssetID: "a4", Source: "s", Score: 0.80},
		{AssetID: "a5", Source: "s", Score: 0.75},
		{AssetID: "a6", Source: "s", Score: 0.70},
	}

	backend := &limitCapturingBackend{
		name:       "cap-backend",
		totalItems: items,
	}
	if err := registry.Register(backend); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)

	// Page 1: limit=2 → backend gets limit=2, returns [a1, a2]
	page1, err := agg.Search(context.Background(), Query{Limit: 2})
	if err != nil {
		t.Fatalf("page1 err: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1: want 2 items, got %d", len(page1.Items))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1: NextCursor must be non-empty")
	}

	// Record the limit the backend saw for page 1.
	backend.mu.Lock()
	page1BackendLimit := backend.seenLimits[len(backend.seenLimits)-1]
	backend.mu.Unlock()

	if page1BackendLimit != 2 {
		t.Fatalf("page1 backend limit: want 2, got %d", page1BackendLimit)
	}

	// Page 2: limit=2 + cursor from page 1.
	// Skip-set has 2 items (a1, a2).
	// Without T4 fix: backend sees limit=2, returns [a1, a2],
	//   skip-set removes them → page 2 empty.
	// With T4 fix: backend sees limit=2+2=4, returns [a1,a2,a3,a4],
	//   skip-set removes [a1,a2] → page 2 = [a3, a4].
	page2, err := agg.Search(context.Background(), Query{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2 err: %v", err)
	}

	// Verify the backend received an increased limit.
	backend.mu.Lock()
	page2BackendLimit := backend.seenLimits[len(backend.seenLimits)-1]
	backend.mu.Unlock()

	// With T4 fix: page1 limit=2 + skip-set size=2 = effective limit 4.
	// Pin the exact value to catch over-bumping bugs.
	expectedBackendLimit := 4
	if page2BackendLimit != expectedBackendLimit {
		t.Fatalf("T4 regression: page2 backend limit want %d (= page1 limit 2 + skip-set size 2), got %d",
			expectedBackendLimit, page2BackendLimit)
	}

	// Verify page 2 is NOT empty (the critical T4 invariant).
	if len(page2.Items) == 0 {
		t.Fatal("T4 regression: page 2 must NOT be empty — " +
			"cursor skip-set removed all backend results because " +
			"backend limit was not increased by skip-set size")
	}

	// Verify page 2 items are distinct from page 1.
	page1IDs := make(map[string]struct{}, len(page1.Items))
	for _, c := range page1.Items {
		page1IDs[c.AssetID] = struct{}{}
	}
	for _, c := range page2.Items {
		if _, seen := page1IDs[c.AssetID]; seen {
			t.Fatalf("T4 regression: page 2 item %q was already on page 1 — "+
				"skip-set should have filtered it", c.AssetID)
		}
	}

	// Verify the final page size respects the user-requested limit.
	if len(page2.Items) > 2 {
		t.Fatalf("page 2 must respect limit=2, got %d items", len(page2.Items))
	}
}

// TestAggregatorCursorPagination_NoCursor_NoBackendLimitBump verifies
// that when no cursor is present (first page), the backend limit is
// NOT bumped. This is a regression guard against over-fetching on
// the initial page request.
func TestAggregatorCursorPagination_NoCursor_NoBackendLimitBump(t *testing.T) {
	registry := NewBackendRegistry()

	backend := &limitCapturingBackend{
		name: "cap-backend",
		totalItems: []Candidate{
			{AssetID: "a1", Source: "s", Score: 0.9},
			{AssetID: "a2", Source: "s", Score: 0.8},
		},
	}
	if err := registry.Register(backend); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)

	_, err := agg.Search(context.Background(), Query{Limit: 5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	backend.mu.Lock()
	seenLimit := backend.seenLimits[len(backend.seenLimits)-1]
	backend.mu.Unlock()

	if seenLimit != 5 {
		t.Fatalf("first page (no cursor) should NOT bump backend limit: want 5, got %d", seenLimit)
	}
}
