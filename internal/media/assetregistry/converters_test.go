package assetregistry

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

func TestToCanonicalNil(t *testing.T) {
	if got := ToCanonical(nil); got != nil {
		t.Errorf("ToCanonical(nil) = %v, want nil", got)
	}
}

func TestToLegacyNil(t *testing.T) {
	if got := ToLegacy(nil); got != nil {
		t.Errorf("ToLegacy(nil) = %v, want nil", got)
	}
}

func TestRoundTripBasic(t *testing.T) {
	now := time.Now()
	legacy := &models.MediaAsset{
		ID:             "asset-1",
		Source:         "youtube",
		Name:           "Test Clip",
		Filename:       "clip.mp4",
		MediaType:      "video",
		Category:       "nature",
		Group:          "group-a",
		ExternalURL:    "https://youtube.com/watch?v=abc",
		ClipPageURL:    "https://artlist.io/clip/123",
		Duration:       120,
		Tags:           []string{"tag1", "tag2"},
		SearchTerms:    []string{"query1"},
		SearchText:     "search text",
		LifecycleState: "ready",
		CreatedAt:      now,
		UpdatedAt:      now,
		ThumbURL:       "https://thumb.url/img.jpg",
		Width:          1920,
		Height:         1080,
		RelativePath:   "some/relative/path",
		TagsNorm:       "tag1 tag2",
		FolderID:       "folder-1",
		ParentFolderID: "parent-1",
		FolderPath:     "/root/folder",
		Depth:          2,
		IsFolder:       false,
		SceneType:      "outdoor",
		QualityScore:   0.85,
		ReuseCount:     5,
		LastUsedAt:     "2024-01-01",
		UsableFor:      []string{"intro", "broll"},
		AvoidFor:       []string{"credits"},
		ChildCount:     3,
		PHash:          "phash123",
		DriveFileID:    "drive-123",
		DriveLink:      "https://drive.google.com/file/d/123",
		DownloadLink:   "https://drive.google.com/download/123",
		LocalPath:      "/data/clip.mp4",
		FileHash:       "abc123hash",
		EmbeddingJSON:  `[0.1, 0.2]`,
		VisualEmbedding: "vis-emb",
		TranscriptEmbedding: "trans-emb",
		VisualEmbeddingJSON: "vis-emb-json",
		Metadata: map[string]any{
			"custom_field": "value",
		},
	}

	// Round-trip
	canon := ToCanonical(legacy)
	back := ToLegacy(canon)

	// Verify round-trip for key fields
	tests := []struct {
		field string
		got   any
		want  any
	}{
		{"ID", back.ID, "asset-1"},
		{"Source", back.Source, "youtube"},
		{"Name", back.Name, "Test Clip"},
		{"Filename", back.Filename, "clip.mp4"},
		{"MediaType", back.MediaType, "video"},
		{"Category", back.Category, "nature"},
		{"Group", back.Group, "group-a"},
		{"Duration", back.Duration, 120},
		{"Tags", len(back.Tags), 2},
		{"SearchTerms", len(back.SearchTerms), 1},
		{"SearchText", back.SearchText, "search text"},
		{"LifecycleState", back.LifecycleState, "ready"},
		{"ThumbURL", back.ThumbURL, "https://thumb.url/img.jpg"},
		{"Width", back.Width, 1920},
		{"Height", back.Height, 1080},
		{"RelativePath", back.RelativePath, "some/relative/path"},
		{"TagsNorm", back.TagsNorm, "tag1 tag2"},
		{"FolderID", back.FolderID, "folder-1"},
		{"ParentFolderID", back.ParentFolderID, "parent-1"},
		{"FolderPath", back.FolderPath, "/root/folder"},
		{"Depth", back.Depth, 2},
		{"SceneType", back.SceneType, "outdoor"},
		{"QualityScore", back.QualityScore, 0.85},
		{"ReuseCount", back.ReuseCount, 5},
		{"LastUsedAt", back.LastUsedAt, "2024-01-01"},
		{"ChildCount", back.ChildCount, 3},
		{"PHash", back.PHash, "phash123"},
		{"DriveFileID", back.DriveFileID, "drive-123"},
		{"DriveLink", back.DriveLink, "https://drive.google.com/file/d/123"},
		{"DownloadLink", back.DownloadLink, "https://drive.google.com/download/123"},
		{"LocalPath", back.LocalPath, "/data/clip.mp4"},
		{"FileHash", back.FileHash, "abc123hash"},
		{"EmbeddingJSON", back.EmbeddingJSON, `[0.1, 0.2]`},
		{"VisualEmbedding", back.VisualEmbedding, "vis-emb"},
		{"TranscriptEmbedding", back.TranscriptEmbedding, "trans-emb"},
		{"VisualEmbeddingJSON", back.VisualEmbeddingJSON, "vis-emb-json"},
		{"ClipPageURL", back.ClipPageURL, "https://artlist.io/clip/123"},
		// Custom metadata survives round-trip
		{"Metadata custom_field", back.GetMetadataString("custom_field"), "value"},
	}

	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.field, tc.got, tc.want)
		}
	}

	// Verify canonical fields
	if canon.SourceURL != "https://youtube.com/watch?v=abc" {
		t.Errorf("SourceURL = %q, want youtube URL", canon.SourceURL)
	}
	if canon.ThumbnailURL != "https://thumb.url/img.jpg" {
		t.Errorf("ThumbnailURL = %q", canon.ThumbnailURL)
	}
	if canon.DurationMs != 120 {
		t.Errorf("DurationMs = %d, want 120", canon.DurationMs)
	}
	if canon.LifecycleState != asset.StateReady {
		t.Errorf("LifecycleState = %q, want ready", canon.LifecycleState)
	}

	// Verify Status/Error are zeroed (migrated to asset_processing)
	if back.Status != "" {
		t.Errorf("Status should be empty after round-trip, got %q", back.Status)
	}
	if back.Error != "" {
		t.Errorf("Error should be empty after round-trip, got %q", back.Error)
	}

	// Verify _width, _height, _relative_path are NOT in metadata after round-trip
	for _, key := range []string{"_width", "_height", "_relative_path", "_tags_norm"} {
		if _, ok := back.Metadata[key]; ok {
			t.Errorf("metadata key %q should be removed after round-trip", key)
		}
	}
}

func TestRoundTripEmptyFields(t *testing.T) {
	// Verify zero-value fields round-trip without crashes.
	legacy := &models.MediaAsset{
		ID:   "empty",
		Name: "Empty Asset",
	}

	canon := ToCanonical(legacy)
	back := ToLegacy(canon)

	if back.ID != "empty" {
		t.Errorf("ID = %q, want empty", back.ID)
	}
	if back.Name != "Empty Asset" {
		t.Errorf("Name = %q", back.Name)
	}
	// Nil slices should become empty, not nil
	if back.Tags == nil {
		t.Error("Tags should be non-nil (empty slice)")
	}
	if back.UsableFor == nil {
		t.Error("UsableFor should be non-nil (empty slice)")
	}
}

func TestExternalURLFallback(t *testing.T) {
	// When SourceURL is set but ExternalURL is not, ToLegacy uses SourceURL.
	canon := &asset.MediaAsset{
		ID:        "fallback-test",
		SourceURL: "https://example.com",
	}
	back := ToLegacy(canon)
	if back.ExternalURL != "https://example.com" {
		t.Errorf("ExternalURL = %q, want %q", back.ExternalURL, "https://example.com")
	}

	// When both are set, SourceURL wins.
	canon2 := &asset.MediaAsset{
		ID:          "fallback-test-2",
		SourceURL:   "https://primary.com",
		ExternalURL: "https://alias.com",
	}
	back2 := ToLegacy(canon2)
	if back2.ExternalURL != "https://primary.com" {
		t.Errorf("ExternalURL = %q, want %q", back2.ExternalURL, "https://primary.com")
	}
}

func TestDurationConversion(t *testing.T) {
	legacy := &models.MediaAsset{
		ID:       "dur-test",
		Duration: 240,
	}
	canon := ToCanonical(legacy)
	if canon.DurationMs != 240 {
		t.Errorf("DurationMs = %d, want 240", canon.DurationMs)
	}

	canon.DurationMs = 500
	back := ToLegacy(canon)
	if back.Duration != 500 {
		t.Errorf("Duration = %d, want 500", back.Duration)
	}
}

func TestLifecycleStateConversion(t *testing.T) {
	// ready → StateReady
	legacy := &models.MediaAsset{ID: "ls1", LifecycleState: "ready"}
	canon := ToCanonical(legacy)
	if canon.LifecycleState != asset.StateReady {
		t.Errorf("LifecycleState = %q", canon.LifecycleState)
	}
	back := ToLegacy(canon)
	if back.LifecycleState != "ready" {
		t.Errorf("LifecycleState = %q", back.LifecycleState)
	}

	// deleted → StateDeleted
	legacy2 := &models.MediaAsset{ID: "ls2", LifecycleState: "deleted"}
	canon2 := ToCanonical(legacy2)
	if canon2.LifecycleState != asset.StateDeleted {
		t.Errorf("LifecycleState = %q", canon2.LifecycleState)
	}
	back2 := ToLegacy(canon2)
	if back2.LifecycleState != "deleted" {
		t.Errorf("LifecycleState = %q", back2.LifecycleState)
	}
}

func TestMetadataClone(t *testing.T) {
	// Verify ToCanonical clones Metadata so mutations don't leak.
	legacy := &models.MediaAsset{
		ID: "clone-test",
		Metadata: map[string]any{
			"key": "original",
		},
	}
	canon := ToCanonical(legacy)
	legacy.Metadata["key"] = "mutated"

	if canon.GetMetadataString("key") != "original" {
		t.Errorf("Metadata mutated through legacy reference: got %q",
			canon.GetMetadataString("key"))
	}
}
