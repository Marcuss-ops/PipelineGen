package assetprocessing

import (
	"context"
	"testing"

	"velox/go-master/internal/storage"
)

const testSchema = `
CREATE TABLE IF NOT EXISTS asset_processing (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL,
    step            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','running','completed','failed')),
    started_at      TEXT,
    completed_at    TEXT,
    error_message   TEXT NOT NULL DEFAULT '',
    attempt_count   INTEGER NOT NULL DEFAULT 1,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, step)
);
`

func newTestRepo(t *testing.T) (*Repository, func()) {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, testSchema)
	return NewRepository(db), func() { db.Close() }
}

func TestStartAndComplete(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// Start processing.
	if err := repo.Start(ctx, "asset_1", "download"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	rec, err := repo.Get(ctx, "asset_1", "download")
	if err != nil {
		t.Fatalf("Get after start: %v", err)
	}
	if rec == nil {
		t.Fatal("expected record after start, got nil")
	}
	if rec.Status != StatusRunning {
		t.Errorf("expected running, got %s", rec.Status)
	}
	if rec.StartedAt == nil {
		t.Error("started_at should be set")
	}
	if rec.AttemptCount != 1 {
		t.Errorf("expected attempt_count=1 on first start, got %d", rec.AttemptCount)
	}

	// Complete (only succeeds if status=running).
	if err := repo.Complete(ctx, "asset_1", "download"); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	rec, _ = repo.Get(ctx, "asset_1", "download")
	if rec.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", rec.Status)
	}
	if rec.CompletedAt == nil {
		t.Error("completed_at should be set")
	}
}

func TestStartIncrementsAttemptCount(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// First start → attempt_count=1.
	if err := repo.Start(ctx, "asset_2", "embedding"); err != nil {
		t.Fatalf("First Start: %v", err)
	}
	rec, _ := repo.Get(ctx, "asset_2", "embedding")
	if rec.AttemptCount != 1 {
		t.Errorf("expected attempt_count=1 on first start, got %d", rec.AttemptCount)
	}

	// Second start → attempt_count=2 (incremented).
	if err := repo.Start(ctx, "asset_2", "embedding"); err != nil {
		t.Fatalf("Second Start: %v", err)
	}
	rec, _ = repo.Get(ctx, "asset_2", "embedding")
	if rec.AttemptCount != 2 {
		t.Errorf("expected attempt_count=2 after second start, got %d", rec.AttemptCount)
	}
}

func TestFail(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	if err := repo.Start(ctx, "asset_3", "transcription"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := repo.Fail(ctx, "asset_3", "transcription", "timeout after 30s"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	rec, _ := repo.Get(ctx, "asset_3", "transcription")
	if rec.Status != StatusFailed {
		t.Errorf("expected failed, got %s", rec.Status)
	}
	if rec.ErrorMessage != "timeout after 30s" {
		t.Errorf("error: got %q", rec.ErrorMessage)
	}
	if rec.CompletedAt == nil {
		t.Error("completed_at should be set on fail")
	}
}

func TestCompleteFailsIfNotRunning(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// Complete without Start → should fail because no record exists.
	if err := repo.Complete(ctx, "asset_missing", "download"); err == nil {
		t.Fatal("expected error when completing non-existent record")
	}

	// Create a completed record, then try to complete again.
	repo.Start(ctx, "asset_ok", "download")
	repo.Complete(ctx, "asset_ok", "download")

	// Second Complete should fail (status=completed, not running).
	if err := repo.Complete(ctx, "asset_ok", "download"); err == nil {
		t.Fatal("expected error when completing already-completed step")
	}
}

func TestFailFailsIfNotRunning(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// Fail without Start → should fail.
	if err := repo.Fail(ctx, "asset_none", "step", "error"); err == nil {
		t.Fatal("expected error when failing non-existent record")
	}

	// Create a failed record, then try to fail again.
	repo.Start(ctx, "asset_fail_once", "step")
	repo.Fail(ctx, "asset_fail_once", "step", "first error")

	// Second Fail should fail (status=failed, not running).
	if err := repo.Fail(ctx, "asset_fail_once", "step", "second error"); err == nil {
		t.Fatal("expected error when failing already-failed step")
	}
}

func TestTransition(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// First create the record with Start(), then Transition.
	if err := repo.Start(ctx, "asset_tr", "step1"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// running → completed
	if err := repo.Transition(ctx, "asset_tr", "step1", StatusRunning, StatusCompleted); err != nil {
		t.Fatalf("Transition running→completed: %v", err)
	}
	rec, _ := repo.Get(ctx, "asset_tr", "step1")
	if rec.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", rec.Status)
	}
	if rec.CompletedAt == nil {
		t.Error("completed_at should be set on complete transition")
	}

	// completed → running (reprocessing)
	if err := repo.Transition(ctx, "asset_tr", "step1", StatusCompleted, StatusRunning); err != nil {
		t.Fatalf("Transition completed→running: %v", err)
	}
	rec, _ = repo.Get(ctx, "asset_tr", "step1")
	if rec.Status != StatusRunning {
		t.Errorf("expected running, got %s", rec.Status)
	}

	// running → failed
	if err := repo.Transition(ctx, "asset_tr", "step1", StatusRunning, StatusFailed); err != nil {
		t.Fatalf("Transition running→failed: %v", err)
	}
	rec, _ = repo.Get(ctx, "asset_tr", "step1")
	if rec.Status != StatusFailed {
		t.Errorf("expected failed, got %s", rec.Status)
	}

	// failed → running (retry)
	if err := repo.Transition(ctx, "asset_tr", "step1", StatusFailed, StatusRunning); err != nil {
		t.Fatalf("Transition failed→running: %v", err)
	}
	rec, _ = repo.Get(ctx, "asset_tr", "step1")
	if rec.Status != StatusRunning {
		t.Errorf("expected running after retry, got %s", rec.Status)
	}
}

func TestTransition_Invalid(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// Start to create the record.
	if err := repo.Start(ctx, "asset_inv", "step"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// pending → completed (illegal, must go through running)
	// Use a non-existent step to test invalid from status.
	err := repo.Transition(ctx, "asset_inv", "step", StatusPending, StatusCompleted)
	if err == nil {
		t.Fatal("expected error for invalid transition pending→completed")
	}

	// completed → failed (illegal from terminal state via direct transition)
	repo.Complete(ctx, "asset_inv", "step")
	err = repo.Transition(ctx, "asset_inv", "step", StatusCompleted, StatusFailed)
	if err == nil {
		t.Fatal("expected error for invalid transition completed→failed")
	}
}

func TestTransition_WrongFromStatus(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Start(ctx, "asset_ws", "step")
	repo.Complete(ctx, "asset_ws", "step")

	// Try running→failed, but current is completed.
	err := repo.Transition(ctx, "asset_ws", "step", StatusRunning, StatusFailed)
	if err == nil {
		t.Fatal("expected error when from status doesn't match")
	}
}

func TestTransition_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Transition(ctx, "nonexistent", "step", StatusPending, StatusRunning)
	if err == nil {
		t.Fatal("expected error when transitioning non-existent record")
	}
}

func TestGetByAssetID(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Start(ctx, "asset_4", "download")
	repo.Complete(ctx, "asset_4", "download")
	repo.Start(ctx, "asset_4", "transcription")

	records, err := repo.GetByAssetID(ctx, "asset_4")
	if err != nil {
		t.Fatalf("GetByAssetID: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
}

func TestGetFailed(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Start(ctx, "a1", "download")
	repo.Complete(ctx, "a1", "download")
	repo.Start(ctx, "a1", "embedding")
	repo.Fail(ctx, "a1", "embedding", "OOM")

	repo.Start(ctx, "a2", "download")
	repo.Fail(ctx, "a2", "download", "network error")

	failed, err := repo.GetFailed(ctx)
	if err != nil {
		t.Fatalf("GetFailed: %v", err)
	}
	if len(failed) != 2 {
		t.Fatalf("expected 2 failed records, got %d", len(failed))
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Start(ctx, "a3", "download")
	repo.Start(ctx, "a3", "transcription")

	if err := repo.Delete(ctx, "a3", "download"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	records, _ := repo.GetByAssetID(ctx, "a3")
	if len(records) != 1 {
		t.Fatalf("expected 1 record after delete, got %d", len(records))
	}
}

func TestDeleteAll(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Start(ctx, "a4", "download")
	repo.Start(ctx, "a4", "transcription")
	repo.Start(ctx, "a4", "embedding")

	if err := repo.DeleteAll(ctx, "a4"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	records, _ := repo.GetByAssetID(ctx, "a4")
	if len(records) != 0 {
		t.Fatalf("expected 0 records after DeleteAll, got %d", len(records))
	}
}

func TestUpsert_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Upsert(ctx, ProcessingRecord{
		AssetID:      "asset_json",
		Step:         "download",
		Status:       StatusPending,
		MetadataJSON: "not-valid-json",
	})
	if err == nil {
		t.Fatal("expected error for invalid metadata_json in Upsert")
	}
}

func TestUpsert_ValidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Upsert(ctx, ProcessingRecord{
		AssetID:      "asset_valid_json",
		Step:         "download",
		Status:       StatusPending,
		MetadataJSON: `{"encoder":"h264"}`,
	})
	if err != nil {
		t.Fatalf("expected success for valid metadata_json, got: %v", err)
	}

	rec, err := repo.Get(ctx, "asset_valid_json", "download")
	if err != nil {
		t.Fatalf("Get after valid Upsert: %v", err)
	}
	if rec == nil {
		t.Fatal("expected record after valid Upsert")
	}
	if rec.MetadataJSON != `{"encoder":"h264"}` {
		t.Errorf("metadata_json mismatch: got %s", rec.MetadataJSON)
	}
}

func TestStart_ValidJSONPreserved(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// Upsert with valid JSON metadata, then Start to mark it running.
	err := repo.Upsert(ctx, ProcessingRecord{
		AssetID:      "asset_meta",
		Step:         "embedding",
		Status:       StatusPending,
		MetadataJSON: `{"model":"e5-base","dims":768}`,
	})
	if err != nil {
		t.Fatalf("Upsert with valid metadata: %v", err)
	}

	err = repo.Start(ctx, "asset_meta", "embedding")
	if err != nil {
		t.Fatalf("Start after Upsert: %v", err)
	}

	rec, _ := repo.Get(ctx, "asset_meta", "embedding")
	if rec.MetadataJSON != `{"model":"e5-base","dims":768}` {
		t.Errorf("metadata_json should be preserved after Start, got: %s", rec.MetadataJSON)
	}
}

func TestUpsert_EmptyJSONAllowed(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// Empty string should be allowed (default JSON).
	err := repo.Upsert(ctx, ProcessingRecord{
		AssetID:      "asset_empty_json",
		Step:         "download",
		Status:       StatusPending,
		MetadataJSON: "",
	})
	if err != nil {
		t.Fatalf("empty metadata_json should be allowed: %v", err)
	}

	rec, _ := repo.Get(ctx, "asset_empty_json", "download")
	if rec.MetadataJSON != "" {
		t.Errorf("empty metadata_json should be preserved, got: %s", rec.MetadataJSON)
	}
}

func TestUpsert_EmptyObjectJSONAllowed(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// "{}" should be allowed (empty JSON object).
	err := repo.Upsert(ctx, ProcessingRecord{
		AssetID:      "asset_empty_obj",
		Step:         "download",
		Status:       StatusPending,
		MetadataJSON: "{}",
	})
	if err != nil {
		t.Fatalf(`"{}" metadata_json should be allowed: %v`, err)
	}

	rec, _ := repo.Get(ctx, "asset_empty_obj", "download")
	if rec.MetadataJSON != "{}" {
		t.Errorf(`expected "{}", got: %s`, rec.MetadataJSON)
	}
}
