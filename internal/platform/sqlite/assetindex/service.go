package assetindex

import (
	"context"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Upsert(ctx context.Context, rec *AssetRecord) error {
	return s.repo.Upsert(ctx, rec)
}

func (s *Service) FindByContentHash(ctx context.Context, hash string) (*AssetRecord, error) {
	return s.repo.FindByContentHash(ctx, hash)
}

func (s *Service) FindReadyByGroup(ctx context.Context, group, subfolder string) ([]*AssetRecord, error) {
	return s.repo.FindReadyByGroup(ctx, group, subfolder)
}

func (s *Service) FindBySource(ctx context.Context, source, sourceID string) (*AssetRecord, error) {
	return s.repo.FindBySource(ctx, source, sourceID)
}

func (s *Service) GetByID(ctx context.Context, assetID string) (*AssetRecord, error) {
	return s.repo.GetByID(ctx, assetID)
}

func (s *Service) UpdateStatus(ctx context.Context, assetID, status string) error {
	return s.repo.UpdateStatus(ctx, assetID, status)
}

func (s *Service) Delete(ctx context.Context, assetID string) error {
	return s.repo.Delete(ctx, assetID)
}

func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	return s.repo.GetStats(ctx)
}

func (s *Service) ListAll(ctx context.Context) ([]*AssetRecord, error) {
	return s.repo.ListAll(ctx)
}
