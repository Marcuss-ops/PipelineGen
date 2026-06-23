package clipssearch

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

type mockRepo struct {
	searchFn func(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error)
}

func (m *mockRepo) SearchClipsAdvanced(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
	if m.searchFn != nil {
		return m.searchFn(ctx, req)
	}
	return &asset.AdvancedSearchResult{}, nil
}

func TestSearch_MergesSources(t *testing.T) {
	svc := NewService(zap.NewNop(), map[string]AdvancedSearchRepo{
		"youtube": &mockRepo{
			searchFn: func(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
				if req.Source != "youtube" {
					t.Fatalf("expected source youtube, got %q", req.Source)
				}
				return &asset.AdvancedSearchResult{
					Clips: []*asset.Asset{{ID: "yt-1"}},
					Total: 1,
				}, nil
			},
		},
		"artlist": &mockRepo{
			searchFn: func(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
				return &asset.AdvancedSearchResult{
					Clips: []*asset.Asset{{ID: "al-1"}},
					Total: 1,
				}, nil
			},
		},
	})

	result, err := svc.Search(context.Background(), asset.AdvancedSearchRequest{Q: "  test  ", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Clips) != 2 {
		t.Fatalf("expected 2 clips, got %d", len(result.Clips))
	}
	if result.Total != 2 {
		t.Fatalf("expected total 2, got %d", result.Total)
	}
	if result.Limit != 10 {
		t.Fatalf("expected limit 10, got %d", result.Limit)
	}
}

func TestSearch_SkipsBrokenRepos(t *testing.T) {
	svc := NewService(zap.NewNop(), map[string]AdvancedSearchRepo{
		"youtube": &mockRepo{
			searchFn: func(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
				return nil, errors.New("boom")
			},
		},
		"stock": &mockRepo{
			searchFn: func(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
				return &asset.AdvancedSearchResult{
					Clips: []*asset.Asset{{ID: "st-1"}},
					Total: 1,
				}, nil
			},
		},
	})

	result, err := svc.Search(context.Background(), asset.AdvancedSearchRequest{Source: "all"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Clips) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(result.Clips))
	}
	if result.Total != 1 {
		t.Fatalf("expected total 1, got %d", result.Total)
	}
}
