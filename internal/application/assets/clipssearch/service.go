package clipssearch

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// AdvancedSearchRepo searches clips with structured filters.
type AdvancedSearchRepo interface {
	SearchClipsAdvanced(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error)
}

// Service fans out advanced clip search across the configured source repos.
type Service struct {
	repos map[string]AdvancedSearchRepo
	log   *zap.Logger
}

// NewService creates a new advanced clip search use case.
func NewService(log *zap.Logger, repos map[string]AdvancedSearchRepo) *Service {
	return &Service{repos: repos, log: log}
}

// Search performs a multi-source advanced clip search and merges results.
func (s *Service) Search(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
	req.Q = strings.TrimSpace(req.Q)

	sources := []string{"youtube", "artlist", "stock"}
	if req.Source != "" && req.Source != "all" {
		sources = []string{req.Source}
	}

	var allClips []*asset.Asset
	totalCount := 0

	for _, src := range sources {
		repo, ok := s.repos[src]
		if !ok || repo == nil {
			continue
		}

		srcReq := req
		srcReq.Source = src
		srcReq.Limit = 0

		result, err := repo.SearchClipsAdvanced(ctx, srcReq)
		if err != nil {
			if s.log != nil {
				s.log.Warn("advanced clip search failed", zap.String("source", src), zap.Error(err))
			}
			continue
		}
		if result == nil {
			continue
		}

		allClips = append(allClips, result.Clips...)
		totalCount += result.Total
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := req.Offset
	if offset > 0 && offset < len(allClips) {
		allClips = allClips[offset:]
	} else if offset >= len(allClips) {
		allClips = nil
	}
	if len(allClips) > limit {
		allClips = allClips[:limit]
	}

	return &asset.AdvancedSearchResult{
		Clips:  allClips,
		Total:  totalCount,
		Limit:  limit,
		Offset: req.Offset,
	}, nil
}
