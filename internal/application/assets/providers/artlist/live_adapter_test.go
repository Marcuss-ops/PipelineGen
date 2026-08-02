package artlist

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
)

type fakeLiveSearcher struct {
	query        string
	limit        int
	preferRemote bool
	result       []Candidate
}

func (f *fakeLiveSearcher) SearchLive(_ context.Context, query string, limit int, preferRemote bool) ([]Candidate, error) {
	f.query, f.limit, f.preferRemote = query, limit, preferRemote
	return f.result, nil
}

func TestLiveAdapterUsesLiveSearchPath(t *testing.T) {
	fake := &fakeLiveSearcher{result: []Candidate{{ID: "clip-live"}}}
	adapter := &LiveAdapter{src: fake}

	result, err := adapter.Search(context.Background(), providers.SearchRequest{Query: "mountain sunrise", Limit: 3})
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if fake.query != "mountain sunrise" || fake.limit != 3 || !fake.preferRemote {
		t.Fatalf("live request = query=%q limit=%d prefer_remote=%v", fake.query, fake.limit, fake.preferRemote)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].ID != "clip-live" {
		t.Fatalf("unexpected candidates: %+v", result.Candidates)
	}
}

func TestLiveAdapterDefaultsLimit(t *testing.T) {
	fake := &fakeLiveSearcher{}
	if _, err := (&LiveAdapter{src: fake}).Search(context.Background(), providers.SearchRequest{Query: "x"}); err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if fake.limit != 8 {
		t.Fatalf("limit=%d, want 8", fake.limit)
	}
}

func TestLiveAdapterFailsClosedWhenUnavailable(t *testing.T) {
	_, err := (&LiveAdapter{}).Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, ErrSourceNotWired) {
		t.Fatalf("error=%v, want ErrSourceNotWired", err)
	}
}
