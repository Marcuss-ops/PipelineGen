package search

import (
	"context"
	"testing"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

type recordingLocalStore struct {
	lastRequest pgmedia.LocalMediaSearchRequest
	calls       int
	rows        []pgmedia.MediaAssetRecord
}

func (s *recordingLocalStore) SearchLocal(_ context.Context, req pgmedia.LocalMediaSearchRequest) ([]pgmedia.MediaAssetRecord, error) {
	s.lastRequest = req
	s.calls++
	return s.rows, nil
}

func (s *recordingLocalStore) FindAllByHash(_ context.Context, _ string, _ int) ([]pgmedia.MediaAssetRecord, error) {
	return nil, nil
}

type fixedLocalScorer struct{}

func (fixedLocalScorer) ScoreLocal(_ context.Context, _ search.LocalScoreQuery, candidates []search.LocalScoreCandidate) ([]search.LocalScoreResult, error) {
	out := make([]search.LocalScoreResult, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, search.LocalScoreResult{ID: candidate.ID, Score: 0.5})
	}
	return out, nil
}

// TestFilterLanguageEnforcedContract replaces the former honesty test: the
// local backend must COMPILE q.Filters.Language into the SQL request, not just
// forward the field. It fails if the predicate is silently dropped again.
func TestFilterLanguageEnforcedContract(t *testing.T) {
	store := &recordingLocalStore{}
	backend := &pgLocalSearchBackend{store: store, scorer: fixedLocalScorer{}}

	_, err := backend.Search(context.Background(), search.Query{
		Text:    "greek salad",
		Limit:   5,
		Filters: search.Filters{Language: "it"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("SearchLocal calls = %d, want 1", store.calls)
	}
	if got := store.lastRequest.Language; got != "it" {
		t.Fatalf("local request Language = %q, want %q (filter was dropped)", got, "it")
	}
}

// TestFilterLanguageEmptyStaysEmpty pins the no-filter contract: an empty
// filter must not become an accidental "language = ”" constraint.
func TestFilterLanguageEmptyStaysEmpty(t *testing.T) {
	store := &recordingLocalStore{}
	backend := &pgLocalSearchBackend{store: store, scorer: fixedLocalScorer{}}

	if _, err := backend.Search(context.Background(), search.Query{Text: "x", Limit: 5}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := store.lastRequest.Language; got != "" {
		t.Fatalf("local request Language = %q, want empty", got)
	}
}
