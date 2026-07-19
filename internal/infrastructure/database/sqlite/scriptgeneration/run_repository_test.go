package scriptgenrepo

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/scriptgeneration"
)

// TestSQLiteRunRepository_RoundTrip verifies the full lifecycle of a
// GenerationRun in the SQLite repository.
func TestSQLiteRunRepository_RoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	if err := createTestSchema(db); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	repo, err := NewSQLiteRunRepository(db, zap.NewNop())
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()
	run := &scriptgen.GenerationRun{
		ID:           "run_123",
		JobID:        "job_456",
		Request:      scriptgen.GenerateRequest{IdempotencyKey: "idem-1"},
		Status:       scriptgen.RunStatusPending,
		CurrentStage: scriptgen.StageNormalizing,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil {
		t.Fatal("expected run, got nil")
	}
	if got.ID != run.ID {
		t.Errorf("id mismatch: got %q, want %q", got.ID, run.ID)
	}
	if got.JobID != run.JobID {
		t.Errorf("job_id mismatch: got %q, want %q", got.JobID, run.JobID)
	}
	if got.Request.IdempotencyKey != run.Request.IdempotencyKey {
		t.Errorf("idempotency key mismatch: got %q, want %q", got.Request.IdempotencyKey, run.Request.IdempotencyKey)
	}

	gotByJob, err := repo.GetByJobID(ctx, run.JobID)
	if err != nil {
		t.Fatalf("get by job id: %v", err)
	}
	if gotByJob == nil || gotByJob.ID != run.ID {
		t.Errorf("GetByJobID did not return the expected run")
	}

	if err := repo.UpdateStage(ctx, run.ID, scriptgen.RunStatusRunning, scriptgen.StageGeneratingSceneText); err != nil {
		t.Fatalf("update stage: %v", err)
	}
	got, err = repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Status != scriptgen.RunStatusRunning || got.CurrentStage != scriptgen.StageGeneratingSceneText {
		t.Errorf("UpdateStage failed: got status=%q stage=%q", got.Status, got.CurrentStage)
	}

	result := &scriptgen.GenerateResult{Title: "test script"}
	if err := repo.SavePartialResult(ctx, run.ID, result); err != nil {
		t.Fatalf("save partial result: %v", err)
	}
	got, err = repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get after save result: %v", err)
	}
	if got.Result == nil || got.Result.Title != result.Title {
		t.Errorf("SavePartialResult failed: result=%v", got.Result)
	}

	if err := repo.FailRun(ctx, scriptgen.FailRunInput{
		RunID:        run.ID,
		FailedStage:  scriptgen.StageTranslatingScenes,
		ErrorCode:    "TRANSLATION_FAILED",
		ErrorMessage: "ollama timeout",
		AttemptCount: 1,
	}); err != nil {
		t.Fatalf("fail run: %v", err)
	}
	got, err = repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get after fail: %v", err)
	}
	if got.Status != scriptgen.RunStatusFailed {
		t.Errorf("FailRun status mismatch: got %q, want %q", got.Status, scriptgen.RunStatusFailed)
	}
	if got.ErrorCode != "TRANSLATION_FAILED" {
		t.Errorf("FailRun error_code mismatch: got %q, want %q", got.ErrorCode, "TRANSLATION_FAILED")
	}
	if got.FailedStage != scriptgen.StageTranslatingScenes {
		t.Errorf("FailRun failed_stage mismatch: got %q, want %q", got.FailedStage, scriptgen.StageTranslatingScenes)
	}
}

func createTestSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE pipeline_runs (
			id TEXT PRIMARY KEY,
			job_id TEXT,
			idempotency_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'PENDING',
			current_stage TEXT NOT NULL DEFAULT 'NORMALIZING',
			requested_payload_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT,
			error_code TEXT,
			error_message TEXT,
			failed_stage TEXT,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`)
	return err
}
