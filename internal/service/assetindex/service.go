package assetindex

import (
    "context"
    "time"
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

func (s *Service) UpdateStatus(ctx context.Context, assetID, status string) error {
    return s.repo.UpdateStatus(ctx, assetID, status)
}

func (s *Service) Delete(ctx context.Context, assetID string) error {
    return s.repo.Delete(ctx, assetID)
}

func (s *Service) MarkAsReady(ctx context.Context, assetID string) error {
    return s.repo.UpdateStatus(ctx, assetID, "ready")
}

func (s *Service) CreateOrUpdateFromFinalize(ctx context.Context, assetID, assetType, source, sourceID string, opts CreateOrUpdateOptions) error {
    now := time.Now().UTC()

    rec := &AssetRecord{
        AssetID:      assetID,
        AssetType:    assetType,
        Source:       source,
        SourceID:     sourceID,
        GroupName:    opts.GroupName,
        Subfolder:    opts.Subfolder,
        LocalPath:    opts.LocalPath,
        DriveLink:    opts.DriveLink,
        DownloadLink: opts.DownloadLink,
        FileHash:     opts.FileHash,
        ContentHash:  opts.ContentHash,
        Status:       opts.Status,
        Metadata:     opts.Metadata,
        CreatedAt:    now,
        UpdatedAt:    now,
    }

    return s.repo.Upsert(ctx, rec)
}

type CreateOrUpdateOptions struct {
    GroupName    string
    Subfolder    string
    LocalPath    string
    DriveLink    string
    DownloadLink string
    FileHash     string
    ContentHash  string
    Status       string
    Metadata     string
}
