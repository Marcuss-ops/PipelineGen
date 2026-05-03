package media

import (
	"context"
	"strings"

	"velox/go-master/internal/repository/clips"
)

// ClipsRepositoryAdapter adapts a clips.Repository to the MediaRepository interface.
type ClipsRepositoryAdapter struct {
	repo *clips.Repository
}

// NewClipsRepositoryAdapter creates a new adapter.
func NewClipsRepositoryAdapter(repo *clips.Repository) *ClipsRepositoryAdapter {
	return &ClipsRepositoryAdapter{repo: repo}
}

// UpsertAsset inserts or updates a MediaAsset.
func (a *ClipsRepositoryAdapter) UpsertAsset(ctx context.Context, asset MediaAsset) error {
	clip := MediaAssetToClip(asset)
	return a.repo.UpsertClip(ctx, &clip)
}

// GetAsset retrieves a MediaAsset by workspace and asset ID.
// Uses direct DB query - no in-memory filtering.
func (a *ClipsRepositoryAdapter) GetAsset(ctx context.Context, workspaceID, assetID string) (MediaAsset, error) {
	clip, err := a.repo.GetClip(ctx, assetID)
	if err != nil {
		return MediaAsset{}, err
	}
	if clip == nil {
		return MediaAsset{}, nil
	}
	return ClipToMediaAsset(*clip, workspaceID, ""), nil
}

// SearchAssets searches for MediaAssets matching the query.
// TODO: This is a temporary adapter - it lists all clips and filters in memory.
// A proper implementation should use DB-level filtering (FTS5 or WHERE clauses).
// Also, workspace/project filtering is faked by passing values to ClipToMediaAsset.
func (a *ClipsRepositoryAdapter) SearchAssets(ctx context.Context, query SearchQuery) ([]MediaAsset, error) {
	source := ""
	if len(query.SourceKinds) > 0 {
		source = string(query.SourceKinds[0])
	}

	clipsList, err := a.repo.ListClips(ctx, source)
	if err != nil {
		return nil, err
	}

	assets := make([]MediaAsset, 0, len(clipsList))
	for _, c := range clipsList {
		asset := ClipToMediaAsset(*c, query.WorkspaceID, query.ProjectID)
		if query.Query != "" {
			if !strings.Contains(strings.ToLower(asset.Title), strings.ToLower(query.Query)) {
				continue
			}
		}
		assets = append(assets, asset)
	}

	return assets, nil
}

// ListAssets lists MediaAssets for a workspace/project.
// TODO: This is a temporary adapter - it lists ALL clips and ignores workspace/project
// at the DB level. Workspace/project are faked by passing values to ClipToMediaAsset.
// A proper implementation needs a real media_items table with workspace/project columns.
func (a *ClipsRepositoryAdapter) ListAssets(ctx context.Context, workspaceID, projectID string, limit, offset int) ([]MediaAsset, error) {
	clipsList, err := a.repo.ListClips(ctx, "")
	if err != nil {
		return nil, err
	}

	assets := make([]MediaAsset, 0, len(clipsList))
	for _, c := range clipsList {
		asset := ClipToMediaAsset(*c, workspaceID, projectID)
		assets = append(assets, asset)
	}

	return assets, nil
}

// Ensure the adapter implements MediaRepository.
var _ MediaRepository = (*ClipsRepositoryAdapter)(nil)
