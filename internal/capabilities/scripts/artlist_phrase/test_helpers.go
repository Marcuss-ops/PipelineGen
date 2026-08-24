package artlist_phrase

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

// stubTranslator is a hermetic PhraseTranslator for tests.
// Per-phrase translation is configured via the translations map.
// Phrases not in the map return errStub (default: nil).
type stubTranslator struct {
	translations map[string]string
	errStub      error
}

func (s *stubTranslator) Translate(_ context.Context, phrase string) (string, error) {
	if s.errStub != nil {
		return "", s.errStub
	}
	t, ok := s.translations[phrase]
	if !ok {
		return "", nil
	}
	return t, nil
}

// stubSearcher is a hermetic PhraseAssetSearcher for tests.
// Per-query hit lists are configured via the hits map. Queries not
// in the map return nil. callCount tracks total invocations so tests
// can assert the searcher was/wasn't called for a given phrase.
type stubSearcher struct {
	hits      map[string][]ports.AssetSearchHit
	errStub   error
	callCount int64
}

func (s *stubSearcher) SearchAssets(_ context.Context, q ports.AssetSearchQuery) ([]ports.AssetSearchHit, error) {
	atomic.AddInt64(&s.callCount, 1)
	if s.errStub != nil {
		return nil, s.errStub
	}
	return s.hits[q.Query], nil
}

func (s *stubSearcher) CallCount() int {
	return int(atomic.LoadInt64(&s.callCount))
}

// delayedTranslator exercises the ParallelMap path without relying on
// wall-clock ordering in the caller. Each phrase can finish in a
// different order while the service must still return matches in
// input order.
type delayedTranslator struct {
	translations map[string]string
	delays       map[string]time.Duration
}

func (d *delayedTranslator) Translate(_ context.Context, phrase string) (string, error) {
	if delay := d.delays[phrase]; delay > 0 {
		time.Sleep(delay)
	}
	return d.translations[phrase], nil
}

// orderedHitSearcher records queries under a mutex so the concurrency
// test can inspect the search flow without introducing races.
type orderedHitSearcher struct {
	mu   sync.Mutex
	hits map[string][]ports.AssetSearchHit
}

func (s *orderedHitSearcher) SearchAssets(_ context.Context, q ports.AssetSearchQuery) ([]ports.AssetSearchHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[q.Query], nil
}
