package media

import (
	"testing"
	"time"

	"velox/go-master/pkg/models"
)

func TestClipToMediaAsset(t *testing.T) {
	clip := models.Clip{
		ID:           "clip-1",
		Name:         "Test Clip",
		Category:     "stock",
		Tags:         []string{"tag1", "tag2"},
		ExternalURL:  "http://example.com/clip1",
		Duration:     120,
		Metadata:     `{"key":"value"}`,
		DriveLink:    "https://drive.google.com/file/d/123",
		DownloadLink: "http://example.com/download/123",
		LocalPath:    "/tmp/clip1.mp4",
		FileHash:     "abc123",
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}

	asset := ClipToMediaAsset(clip, "ws-1", "proj-1")

	if asset.ID != "clip-1" {
		t.Errorf("ID = %q, want %q", asset.ID, "clip-1")
	}
	if asset.WorkspaceID != "ws-1" {
		t.Errorf("WorkspaceID = %q, want %q", asset.WorkspaceID, "ws-1")
	}
	if asset.ProjectID != "proj-1" {
		t.Errorf("ProjectID = %q, want %q", asset.ProjectID, "proj-1")
	}
	if asset.Title != "Test Clip" {
		t.Errorf("Title = %q, want %q", asset.Title, "Test Clip")
	}
	if asset.Category != "stock" {
		t.Errorf("Category = %q, want %q", asset.Category, "stock")
	}
	if len(asset.Tags) != 2 || asset.Tags[0] != "tag1" {
		t.Errorf("Tags = %v, want [tag1 tag2]", asset.Tags)
	}
	if asset.ExternalURL != clip.ExternalURL {
		t.Errorf("ExternalURL = %q, want %q", asset.ExternalURL, clip.ExternalURL)
	}
	if asset.DurationSecs != 120 {
		t.Errorf("DurationSecs = %d, want %d", asset.DurationSecs, 120)
	}
	if asset.MetadataJSON != clip.Metadata {
		t.Errorf("MetadataJSON = %q, want %q", asset.MetadataJSON, clip.Metadata)
	}
	if asset.PrimaryFile == nil {
		t.Fatal("PrimaryFile is nil")
	}
	if asset.PrimaryFile.DriveLink != clip.DriveLink {
		t.Errorf("PrimaryFile.DriveLink = %q, want %q", asset.PrimaryFile.DriveLink, clip.DriveLink)
	}
	if asset.PrimaryFile.LocalPath != clip.LocalPath {
		t.Errorf("PrimaryFile.LocalPath = %q, want %q", asset.PrimaryFile.LocalPath, clip.LocalPath)
	}
	if asset.PrimaryFile.FileHash != clip.FileHash {
		t.Errorf("PrimaryFile.FileHash = %q, want %q", asset.PrimaryFile.FileHash, clip.FileHash)
	}
}

func TestMediaAssetToClip(t *testing.T) {
	asset := MediaAsset{
		ID:          "asset-1",
		Title:       "Test Asset",
		Category:    "video",
		Tags:        []string{"tag1", "tag2"},
		ExternalURL: "http://example.com/asset1",
		DurationSecs: 180,
		MetadataJSON: `{"key":"value"}`,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		PrimaryFile: &MediaFile{
			LocalPath:    "/tmp/asset1.mp4",
			DriveLink:    "https://drive.google.com/file/d/456",
			DownloadLink: "http://example.com/download/456",
			FileHash:     "def456",
		},
	}

	clip := MediaAssetToClip(asset)

	if clip.ID != "asset-1" {
		t.Errorf("ID = %q, want %q", clip.ID, "asset-1")
	}
	if clip.Name != "Test Asset" {
		t.Errorf("Name = %q, want %q", clip.Name, "Test Asset")
	}
	if clip.Category != "video" {
		t.Errorf("Category = %q, want %q", clip.Category, "video")
	}
	if len(clip.Tags) != 2 || clip.Tags[0] != "tag1" {
		t.Errorf("Tags = %v, want [tag1 tag2]", clip.Tags)
	}
	if clip.ExternalURL != asset.ExternalURL {
		t.Errorf("ExternalURL = %q, want %q", clip.ExternalURL, asset.ExternalURL)
	}
	if clip.Duration != 180 {
		t.Errorf("Duration = %d, want %d", clip.Duration, 180)
	}
	if clip.Metadata != asset.MetadataJSON {
		t.Errorf("Metadata = %q, want %q", clip.Metadata, asset.MetadataJSON)
	}
	if clip.LocalPath != asset.PrimaryFile.LocalPath {
		t.Errorf("LocalPath = %q, want %q", clip.LocalPath, asset.PrimaryFile.LocalPath)
	}
	if clip.DriveLink != asset.PrimaryFile.DriveLink {
		t.Errorf("DriveLink = %q, want %q", clip.DriveLink, asset.PrimaryFile.DriveLink)
	}
	if clip.FileHash != asset.PrimaryFile.FileHash {
		t.Errorf("FileHash = %q, want %q", clip.FileHash, asset.PrimaryFile.FileHash)
	}
}

func TestClipToMediaAssetEmptyPrimaryFile(t *testing.T) {
	clip := models.Clip{
		ID:   "clip-2",
		Name: "Empty File Clip",
	}

	asset := ClipToMediaAsset(clip, "ws-1", "proj-1")

	if asset.PrimaryFile != nil {
		t.Error("PrimaryFile should be nil when no file fields are set")
	}
}
