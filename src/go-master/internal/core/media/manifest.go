package media

import (
	"context"
	"time"
)

// Manifest represents an export of media assets.
type Manifest struct {
	WorkspaceID string       `json:"workspace_id"`
	ProjectID   string       `json:"project_id"`
	GeneratedAt time.Time    `json:"generated_at"`
	Assets      []AssetEntry `json:"assets"`
}

// AssetEntry represents a media asset in the manifest export.
type AssetEntry struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	SourceKind   string   `json:"source_kind"`
	MediaType    string   `json:"media_type"`
	ExternalURL  string   `json:"external_url,omitempty"`
	DurationSecs int      `json:"duration_secs"`
	FileHash     string   `json:"file_hash,omitempty"`
}

// ManifestExporter exports manifests from a MediaRepository.
type ManifestExporter struct {
	repo MediaRepository
}

// MediaRepository is the interface for the canonical media repository.
type MediaRepository interface {
	UpsertAsset(ctx context.Context, asset MediaAsset) error
	GetAsset(ctx context.Context, workspaceID, assetID string) (MediaAsset, error)
	SearchAssets(ctx context.Context, query SearchQuery) ([]MediaAsset, error)
	ListAssets(ctx context.Context, workspaceID, projectID string, limit, offset int) ([]MediaAsset, error)
}

// NewManifestExporter creates a new ManifestExporter.
func NewManifestExporter(repo MediaRepository) *ManifestExporter {
	return &ManifestExporter{repo: repo}
}

// Export generates a manifest from the database.
func (e *ManifestExporter) Export(ctx context.Context, workspaceID, projectID string) (*Manifest, error) {
	assets, err := e.repo.ListAssets(ctx, workspaceID, projectID, 10000, 0)
	if err != nil {
		return nil, err
	}

	return NewManifestFromAssets(workspaceID, projectID, assets), nil
}

// NewManifestFromAssets creates a Manifest from a slice of MediaAssets.
func NewManifestFromAssets(workspaceID, projectID string, assets []MediaAsset) *Manifest {
	entries := make([]AssetEntry, 0, len(assets))
	for _, a := range assets {
		entry := AssetEntry{
			ID:           a.ID,
			Title:        a.Title,
			Category:     a.Category,
			Tags:         a.Tags,
			SourceKind:   string(a.SourceKind),
			MediaType:    string(a.MediaType),
			ExternalURL:  a.ExternalURL,
			DurationSecs: a.DurationSecs,
		}
		if a.PrimaryFile != nil {
			entry.FileHash = a.PrimaryFile.FileHash
		}
		entries = append(entries, entry)
	}

	return &Manifest{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		GeneratedAt: time.Now(),
		Assets:      entries,
	}
}
