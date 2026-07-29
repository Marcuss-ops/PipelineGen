// Package searchqueries — unit tests for the UseCase (fake Repository).
//
// Wave 14 problem #3 close-out (June 2026): the use-case lives in the
// application layer and must be testable without the SQLite repo, the
// HTTP transport, or the live config. We assert it via a tiny in-memory
// fake that records calls and lets each test pin a canned error.
//
// Pattern: table-driven tests in this file map one-to-one onto the
// use-case methods. Each row focuses on a single orchestration path:
// success / not-found / repo-error. No method-level branching beyond
// that — the use-case has no policy of its own beyond pass-through.
package searchqueries

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeRepo is a tiny in-memory Repository stub. Each method is a thin
// capture-and-return, no goroutines, no DB. Use cases drive the
// entire test surface area; the concrete implementation never runs.
type fakeRepo struct {
	listAllFn         func(ctx context.Context) ([]*asset.SearchQuery, error)
	listActiveFn      func(ctx context.Context) ([]*asset.SearchQuery, error)
	getByIDFn         func(ctx context.Context, id string) (*asset.SearchQuery, error)
	upsertFn          func(ctx context.Context, q *asset.SearchQuery) error
	deleteFn          func(ctx context.Context, id string) error
	listResultsByFn   func(ctx context.Context, qid string) ([]*asset.SearchQueryResult, error)
	upsertCalls       []*asset.SearchQuery
	deleteCalls       []string
	listResultsCallID string
}

func (f *fakeRepo) ListAll(ctx context.Context) ([]*asset.SearchQuery, error) {
	return f.listAllFn(ctx)
}
func (f *fakeRepo) ListActive(ctx context.Context) ([]*asset.SearchQuery, error) {
	return f.listActiveFn(ctx)
}
func (f *fakeRepo) GetByID(ctx context.Context, id string) (*asset.SearchQuery, error) {
	return f.getByIDFn(ctx, id)
}
func (f *fakeRepo) Upsert(ctx context.Context, q *asset.SearchQuery) error {
	f.upsertCalls = append(f.upsertCalls, q)
	return f.upsertFn(ctx, q)
}
func (f *fakeRepo) Delete(ctx context.Context, id string) error {
	f.deleteCalls = append(f.deleteCalls, id)
	return f.deleteFn(ctx, id)
}
func (f *fakeRepo) ListResultsByQuery(ctx context.Context, qid string) ([]*asset.SearchQueryResult, error) {
	f.listResultsCallID = qid
	return f.listResultsByFn(ctx, qid)
}

func sampleQuery(id string) *asset.SearchQuery {
	return &asset.SearchQuery{
		ID:    id,
		Query: "Floyd Mayweather interview",
		// Defaults intentionally left at zero so the test does not
		// accidentally pin repo-level defaulting behaviour; that
		// layering concern belongs to the SQLite repository.
	}
}

func TestUseCase_ListAll(t *testing.T) {
	t.Parallel()
	want := []*asset.SearchQuery{sampleQuery("a"), sampleQuery("b")}
	repo := &fakeRepo{
		listAllFn: func(ctx context.Context) ([]*asset.SearchQuery, error) {
			require.Equal(t, context.Background(), ctx, "use-case must pass through ctx verbatim")
			return want, nil
		},
	}
	uc := NewUseCase(repo)
	got, err := uc.ListAll(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestUseCase_ListActive_RepoError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	repo := &fakeRepo{
		listActiveFn: func(ctx context.Context) ([]*asset.SearchQuery, error) { return nil, wantErr },
	}
	uc := NewUseCase(repo)
	got, err := uc.ListActive(context.Background())
	require.Nil(t, got)
	require.ErrorIs(t, err, wantErr)
}

func TestUseCase_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{
		getByIDFn: func(ctx context.Context, id string) (*asset.SearchQuery, error) {
			return nil, errors.New("row not found")
		},
	}
	uc := NewUseCase(repo)
	got, err := uc.GetByID(context.Background(), "missing")
	require.Nil(t, got)
	require.Error(t, err)
}

func TestUseCase_Upsert_PassesDomainStruct(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{
		upsertFn: func(ctx context.Context, q *asset.SearchQuery) error { return nil },
	}
	uc := NewUseCase(repo)
	q := sampleQuery("xyz")
	q.MaxResults = 10
	err := uc.Upsert(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, repo.upsertCalls, 1)
	require.Same(t, q, repo.upsertCalls[0], "use-case must not deep-copy domain structs")
	require.Equal(t, 10, repo.upsertCalls[0].MaxResults)
}

func TestUseCase_Delete_PropagatesID(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{
		deleteFn: func(ctx context.Context, id string) error { return nil },
	}
	uc := NewUseCase(repo)
	err := uc.Delete(context.Background(), "abc")
	require.NoError(t, err)
	require.Equal(t, []string{"abc"}, repo.deleteCalls)
}

func TestUseCase_ListResults_ForwardsQueryIDAndResultSlice(t *testing.T) {
	t.Parallel()
	want := []*asset.SearchQueryResult{
		{QueryID: "qx", VideoID: "v1", VideoTitle: "T"},
	}
	repo := &fakeRepo{
		listResultsByFn: func(ctx context.Context, qid string) ([]*asset.SearchQueryResult, error) {
			require.Equal(t, "qx", qid)
			return want, nil
		},
	}
	uc := NewUseCase(repo)
	got, err := uc.ListResults(context.Background(), "qx")
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, "qx", repo.listResultsCallID)
}
