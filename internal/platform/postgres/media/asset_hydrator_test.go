package media

import (
	"testing"
	"time"
)

// TestMediaAssetRecord_HydrateAsset pins the single translation site from the
// PostgreSQL read model onto the kernel asset so admin/operator readers cannot
// observe a partial projection.
func TestMediaAssetRecord_HydrateAsset(t *testing.T) {
	rec := &MediaAssetRecord{
		ID:             "a1",
		Name:           "Clip",
		Filename:       "clip.mp4",
		Source:         "youtube",
		MediaType:      "video",
		Category:       "clip",
		LifecycleState: "ACTIVE",
		IndexState:     "INDEXED",
		DurationMS:     1500,
		LocalPath:      "/tmp/clip.mp4",
		DriveFileID:    "d1",
		DriveLink:      "https://drive/d1",
		DownloadLink:   "https://dl/d1",
		SHA256:         "deadbeef",
		ThumbnailURL:   "https://thumb/x.jpg",
		Tags:           []string{"a", "b"},
		SourceURL:      "https://youtu.be/x",
		CreatedAt:      "2026-09-13T00:00:00Z",
		MetadataJSON:   `{"custom":"v"}`,
	}

	a := rec.HydrateAsset()
	if a == nil {
		t.Fatal("HydrateAsset returned nil for a non-nil record")
	}
	if a.ID != "a1" || a.Name != "Clip" || a.Filename != "clip.mp4" {
		t.Fatalf("identity fields lost: %+v", a)
	}
	if a.Source != "youtube" || a.MediaType != "video" || a.Category != "clip" {
		t.Fatalf("classification fields lost: %+v", a)
	}
	if a.Duration != 1500*time.Millisecond {
		t.Fatalf("duration = %v, want 1.5s", a.Duration)
	}
	if a.DriveFileID() != "d1" || a.DriveLink() != "https://drive/d1" {
		t.Fatalf("drive projection lost: %q %q", a.DriveFileID(), a.DriveLink())
	}
	if a.DownloadLink() != "https://dl/d1" || a.LocalPath() != "/tmp/clip.mp4" {
		t.Fatalf("location projection lost: %q %q", a.DownloadLink(), a.LocalPath())
	}
	if a.ContentHash() != "deadbeef" || a.LegacyFileMD5() != "deadbeef" {
		t.Fatalf("hash projection lost: %q %q", a.ContentHash(), a.LegacyFileMD5())
	}
	if a.GetMetadataString("index_state") != "INDEXED" {
		t.Fatalf("index_state not projected: %q", a.GetMetadataString("index_state"))
	}
	if a.GetMetadataString("custom") != "v" {
		t.Fatalf("metadata_json not merged: %v", a.Metadata)
	}
	if len(a.Tags) != 2 || a.Tags[0] != "a" {
		t.Fatalf("tags lost: %v", a.Tags)
	}
	if a.CreatedAt.IsZero() {
		t.Fatal("created_at not parsed")
	}
}

// TestHydrateAsset_NilRecordIsNil pins the nil contract so callers keep their
// existing `clip == nil` checks.
func TestHydrateAsset_NilRecordIsNil(t *testing.T) {
	var rec *MediaAssetRecord
	if got := rec.HydrateAsset(); got != nil {
		t.Fatalf("nil record must hydrate to nil, got %+v", got)
	}
}
