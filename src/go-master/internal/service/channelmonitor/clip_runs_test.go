package channelmonitor

import (
	"path/filepath"
	"testing"
)

func TestClipRunStoreUpsertAndCompleted(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "clip_runs.sqlite")
	store, err := OpenClipRunStore(storePath)
	if err != nil {
		t.Fatalf("OpenClipRunStore failed: %v", err)
	}

	key := clipRunKey("video-1", 10, 20)
	if err := store.Upsert(ClipRunRecord{
		RunKey:       key,
		VideoID:      "video-1",
		Title:        "Test Video",
		SegmentIdx:   1,
		StartSec:     10,
		EndSec:       20,
		Duration:     10,
		Status:       ClipRunStatusUploaded,
		FileName:     "clip.mp4",
		DriveFileID:  "drive-1",
		DriveFileURL: "https://drive.google.com/file/d/drive-1/view",
	}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	rec, ok := store.Get(key)
	if !ok || rec.DriveFileID != "drive-1" {
		t.Fatalf("expected record to be persisted")
	}
	if !store.Completed(key) {
		t.Fatalf("expected run to be completed")
	}
}
