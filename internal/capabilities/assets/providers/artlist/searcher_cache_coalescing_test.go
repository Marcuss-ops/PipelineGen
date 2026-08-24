package assets

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type coalescingSearcher struct {
	calls  atomic.Int32
	wait   chan struct{}
	result []Candidate
}

func (s *coalescingSearcher) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	s.calls.Add(1)
	select {
	case <-s.wait:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return append([]Candidate(nil), s.result...), nil
}

func TestCachedSearcher_CoalescesConcurrentMisses(t *testing.T) {
	upstream := &coalescingSearcher{
		wait:   make(chan struct{}),
		result: []Candidate{{ID: "artlist-1", Title: "Highway"}},
	}
	cached := NewCachedSearcher(upstream, newLiveSearchCache(), 24, nil)

	const callers = 20
	results := make([][]Candidate, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = cached.Search(context.Background(), SearchRequest{Term: "  CAR   Driving Highway ", Limit: 8})
		}(i)
	}
	// Let all callers enter the request path before releasing the single
	// upstream operation.
	time.Sleep(10 * time.Millisecond)
	close(upstream.wait)
	wg.Wait()

	if got := upstream.calls.Load(); got != 1 {
		t.Fatalf("upstream calls=%d, want exactly 1", got)
	}
	for i := range results {
		if errs[i] != nil || len(results[i]) != 1 || results[i][0].ID != "artlist-1" {
			t.Fatalf("caller %d result=%#v err=%v", i, results[i], errs[i])
		}
	}
}

func TestCachedSearcher_CachesNegativeResultAndNormalizesTerm(t *testing.T) {
	upstream := &coalescingSearcher{wait: make(chan struct{})}
	close(upstream.wait)
	cached := NewCachedSearcher(upstream, newLiveSearchCache(), 24, nil)

	for _, term := range []string{"Elon Musk Tesla", "elon musk tesla", " ELON   MUSK   TESLA "} {
		got, err := cached.Search(context.Background(), SearchRequest{Term: term, Limit: 8})
		if err != nil {
			t.Fatalf("Search(%q): %v", term, err)
		}
		if len(got) != 0 {
			t.Fatalf("Search(%q) returned %#v, want negative-cache miss", term, got)
		}
	}
	if got := upstream.calls.Load(); got != 1 {
		t.Fatalf("negative-cache upstream calls=%d, want 1", got)
	}
}
