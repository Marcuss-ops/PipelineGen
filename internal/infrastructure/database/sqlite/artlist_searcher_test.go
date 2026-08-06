package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/require"
)

type fakeArtlistSearchStore struct {
	term  string
	calls int
	clips []*asset.Asset
	err   error
}

func (s *fakeArtlistSearchStore) SearchClips(_ context.Context, source, term string) ([]*asset.Asset, error) {
	s.calls++
	s.term = source + ":" + term
	return s.clips, s.err
}

func TestArtlistSQLiteSearcher_SearchMapsRepositoryAssets(t *testing.T) {
	store := &fakeArtlistSearchStore{clips: []*asset.Asset{{
		ID:          "clip-1",
		Name:        "Test clip",
		SourceURL:   "https://cdn.example/clip.mp4",
		ClipPageURL: "https://artlist.io/clip/1",
		MediaType:   asset.MediaType("video"),
		Duration:    15 * time.Second,
		Tags:        []string{"test"},
		Metadata:    asset.Metadata{"description": "desc", "provider_categories": []any{"nature"}},
	}}}
	searcher := NewArtlistSQLiteSearcher(store)

	got, err := searcher.Search(context.Background(), artlist.SearchRequest{Term: "  test  ", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, store.calls)
	require.Equal(t, "artlist:test", store.term)
	require.Len(t, got, 1)
	require.Equal(t, "clip-1", got[0].ID)
	require.Equal(t, "database", got[0].SourceName)
	require.Equal(t, "desc", got[0].Description)
	require.Equal(t, "https://cdn.example/clip.mp4", got[0].SourceRef)
	require.Equal(t, 15*time.Second, got[0].Duration)
	require.Equal(t, []string{"nature"}, got[0].Categories)
}

func TestArtlistSQLiteSearcher_EmptyTermSkipsRepository(t *testing.T) {
	store := &fakeArtlistSearchStore{}
	searcher := NewArtlistSQLiteSearcher(store)

	got, err := searcher.Search(context.Background(), artlist.SearchRequest{Term: "   "})
	require.NoError(t, err)
	require.Empty(t, got)
	require.Zero(t, store.calls)
}

func TestArtlistSQLiteSearcher_PropagatesRepositoryError(t *testing.T) {
	sentinel := errors.New("sqlite unavailable")
	searcher := NewArtlistSQLiteSearcher(&fakeArtlistSearchStore{err: sentinel})

	_, err := searcher.Search(context.Background(), artlist.SearchRequest{Term: "test"})
	require.ErrorIs(t, err, sentinel)
}
