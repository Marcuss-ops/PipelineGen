package media

import (
	"context"
	"testing"
	"time"
)

// FakeMediaRepository is a fake implementation of MediaRepository for testing.
type FakeMediaRepository struct {
	assets []MediaAsset
}

func (f *FakeMediaRepository) UpsertAsset(ctx context.Context, asset MediaAsset) error {
	f.assets = append(f.assets, asset)
	return nil
}

func (f *FakeMediaRepository) GetAsset(ctx context.Context, workspaceID, assetID string) (MediaAsset, error) {
	for _, a := range f.assets {
		if a.ID == assetID && a.WorkspaceID == workspaceID {
			return a, nil
		}
	}
	return MediaAsset{}, nil
}

func (f *FakeMediaRepository) SearchAssets(ctx context.Context, query SearchQuery) ([]MediaAsset, error) {
	return f.assets, nil
}

func (f *FakeMediaRepository) ListAssets(ctx context.Context, workspaceID, projectID string, limit, offset int) ([]MediaAsset, error) {
	var result []MediaAsset
	for _, a := range f.assets {
		if a.WorkspaceID == workspaceID && (projectID == "" || a.ProjectID == projectID) {
			result = append(result, a)
		}
	}
	return result, nil
}

func TestManifestExporterUsesRepository(t *testing.T) {
	repo := &FakeMediaRepository{
		assets: []MediaAsset{
			{
				ID:          "clip-1",
				WorkspaceID: "w1",
				ProjectID:   "p1",
				Title:       "Clip 1",
				SourceKind:  SourceKindArtlist,
				MediaType:   MediaTypeVideo,
				PrimaryFile: &MediaFile{FileHash: "abc123"},
			},
		},
	}

	exporter := NewManifestExporter(repo)

	manifest, err := exporter.Export(context.Background(), "w1", "p1")
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	if len(manifest.Assets) != 1 {
		t.Errorf("Expected 1 asset, got %d", len(manifest.Assets))
	}
	if manifest.Assets[0].ID != "clip-1" {
		t.Errorf("Expected asset ID 'clip-1', got %q", manifest.Assets[0].ID)
	}
	if manifest.Assets[0].FileHash != "abc123" {
		t.Errorf("Expected file hash 'abc123', got %q", manifest.Assets[0].FileHash)
	}
}

func TestNewManifestFromAssets(t *testing.T) {
	assets := []MediaAsset{
		{
			ID:          "asset-1",
			Title:       "Test Asset",
			Category:    "video",
			Tags:        []string{"tag1", "tag2"},
			SourceKind:  SourceKindYouTube,
			MediaType:   MediaTypeVideo,
			DurationSecs: 120,
			PrimaryFile: &MediaFile{FileHash: "def456"},
		},
	}

	manifest := NewManifestFromAssets("w1", "p1", assets)

	if manifest.WorkspaceID != "w1" {
		t.Errorf("Expected WorkspaceID 'w1', got %q", manifest.WorkspaceID)
	}
	if manifest.ProjectID != "p1" {
		t.Errorf("Expected ProjectID 'p1', got %q", manifest.ProjectID)
	}
	if len(manifest.Assets) != 1 {
		t.Errorf("Expected 1 asset, got %d", len(manifest.Assets))
	}
	if manifest.Assets[0].Title != "Test Asset" {
		t.Errorf("Expected title 'Test Asset', got %q", manifest.Assets[0].Title)
	}
	if len(manifest.Assets[0].Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(manifest.Assets[0].Tags))
	}
	if manifest.Assets[0].FileHash != "def456" {
		t.Errorf("Expected file hash 'def456', got %q", manifest.Assets[0].FileHash)
	}
}

func TestManifestGeneratedAt(t *testing.T) {
	assets := []MediaAsset{
		{ID: "asset-1", Title: "Test", SourceKind: SourceKindManual, MediaType: MediaTypeVideo},
	}

	before := time.Now().Add(-time.Second)
	manifest := NewManifestFromAssets("w1", "p1", assets)
	after := time.Now().Add(time.Second)

	if manifest.GeneratedAt.Before(before) || manifest.GeneratedAt.After(after) {
		t.Errorf("GeneratedAt should be around now, got %v", manifest.GeneratedAt)
	}
}
