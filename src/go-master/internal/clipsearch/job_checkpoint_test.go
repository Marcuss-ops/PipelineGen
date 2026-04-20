package clipsearch

import (
	"path/filepath"
	"testing"
	"time"
)

func TestClipJobCheckpointStore_SaveTransitionReload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "clipsearch_checkpoints.json")
	store, err := OpenClipJobCheckpointStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	job := ClipJobCheckpoint{
		JobID:     "job_1",
		Keyword:   "floyd mayweather",
		Status:    ClipJobStatusQueued,
		Attempts:  1,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SaveOrUpdate(job); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	res := &SearchResult{
		Keyword:  "floyd mayweather",
		ClipID:   "clip_123",
		Filename: "floyd.mp4",
		DriveURL: "https://drive.google.com/file/d/clip_123/view",
		DriveID:  "clip_123",
		Folder:   "Stock/Artlist/Boxe/Floyd Mayweather",
	}
	if err := store.Transition("job_1", ClipJobStatusUploaded, "", res); err != nil {
		t.Fatalf("transition checkpoint: %v", err)
	}

	reloaded, err := OpenClipJobCheckpointStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := reloaded.Get("job_1")
	if !ok {
		t.Fatalf("expected job_1 in reloaded store")
	}
	if got.Status != ClipJobStatusUploaded {
		t.Fatalf("status mismatch: got %s", got.Status)
	}
	if got.DriveID != "clip_123" {
		t.Fatalf("drive id mismatch: got %q", got.DriveID)
	}
	if got.Filename != "floyd.mp4" {
		t.Fatalf("filename mismatch: got %q", got.Filename)
	}
}

func TestClipJobCheckpointStore_GetLatestByKeyword(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "clipsearch_checkpoints.json")
	store, err := OpenClipJobCheckpointStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	now := time.Now().UTC()
	if err := store.SaveOrUpdate(ClipJobCheckpoint{
		JobID:     "job_old",
		Keyword:   "Floyd Mayweather",
		Status:    ClipJobStatusQueued,
		Attempts:  1,
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("save old job: %v", err)
	}
	if err := store.SaveOrUpdate(ClipJobCheckpoint{
		JobID:     "job_new",
		Keyword:   "floyd mayweather",
		Status:    ClipJobStatusSearched,
		Attempts:  2,
		CreatedAt: now.Add(-1 * time.Hour),
		UpdatedAt: now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("save new job: %v", err)
	}

	got, ok := store.GetLatestByKeyword("FLOYD MAYWEATHER")
	if !ok {
		t.Fatalf("expected latest job by keyword")
	}
	if got.JobID != "job_new" {
		t.Fatalf("latest mismatch: got %q", got.JobID)
	}
}
