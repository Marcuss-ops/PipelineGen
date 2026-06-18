package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	"velox/go-master/internal/core/domain/job"
	jobsrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/storage"
)

// jobTestSchema matches the canonical jobs table from the production migrations.
const jobTestSchema = `
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    priority INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    video_name TEXT NOT NULL DEFAULT '',
    active_key TEXT NOT NULL DEFAULT '',
    correlation_id TEXT,
    payload_json TEXT,
    result_json TEXT,
    progress INTEGER NOT NULL DEFAULT 0,
    error TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    worker_id TEXT,
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    next_attempt_at TEXT,
    revision INTEGER NOT NULL DEFAULT 0,
    workflow_id TEXT NOT NULL DEFAULT '',
    workflow_step_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    started_at TEXT,
    completed_at TEXT,
    cancelled_at TEXT
);
`

func setupJobRepo(t *testing.T) (*SQLiteJobRepository, context.Context, *sql.DB, func()) {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, jobTestSchema)
	inner := jobsrepo.NewRepository(db, zap.NewNop())
	repo := NewSQLiteJobRepository(inner)
	ctx := context.Background()
	return repo, ctx, db, func() { db.Close() }
}

// Helper: transition a job to running for tests that need a running job.
func ensureRunning(t *testing.T, repo *SQLiteJobRepository, ctx context.Context, id string) {
	t.Helper()
	if err := repo.Transition(ctx, id, job.StatusQueued, job.StatusRunning); err != nil {
		t.Fatalf("ensureRunning %s: %v", id, err)
	}
}

func TestJobRepo_CreateAndGet(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{
		ID:         "job_001",
		Type:       "script.generate_from_clips",
		Priority:   5,
		Project:    "test-project",
		Payload:    json.RawMessage(`{"topic":"test"}`),
		MaxRetries: 3,
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.Get(ctx, "job_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != "job_001" {
		t.Errorf("ID: got %s", got.ID)
	}
	if got.Type != "script.generate_from_clips" {
		t.Errorf("Type: got %s", got.Type)
	}
	if got.Status != job.StatusQueued {
		t.Errorf("Status: got %s, want queued", got.Status)
	}
	if got.Priority != 5 {
		t.Errorf("Priority: got %d", got.Priority)
	}
	if got.Project != "test-project" {
		t.Errorf("Project: got %s", got.Project)
	}
	if string(got.Payload) != `{"topic":"test"}` {
		t.Errorf("Payload: got %s", string(got.Payload))
	}
}

func TestJobRepo_Get_NotFound(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j, err := repo.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get should not error for not-found: %v", err)
	}
	if j != nil {
		t.Fatal("expected nil for not-found job")
	}
}

func TestJobRepo_List(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	// Create 3 jobs — all queued, then transition as needed.
	for _, id := range []string{"job_list_a", "job_list_b", "job_list_c"} {
		repo.Create(ctx, &job.Job{ID: id, Type: "script.generate"})
	}

	// job_list_b → running (via ClaimNext to get a proper lease)
	claimed, err := repo.ClaimNext(ctx, "worker-list", 60, []string{"script.generate"})
	if err != nil {
		t.Fatalf("ClaimNext for job_b: %v", err)
	}
	if claimed == nil || claimed.ID != "job_list_a" {
		t.Fatal("expected to claim job_list_a (highest priority = equal, oldest)")
	}

	// job_list_c → running → completed
	ensureRunning(t, repo, ctx, "job_list_c")
	repo.Transition(ctx, "job_list_c", job.StatusRunning, job.StatusCompleted)

	// job_list_b is still running, job_list_a was claimed to running, job_list_c is completed
	all, err := repo.List(ctx, job.Filter{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(all))
	}

	// job_list_b was never claimed or transitioned — still queued.
	qs := job.StatusQueued
	queued, err := repo.List(ctx, job.Filter{Status: &qs})
	if err != nil {
		t.Fatalf("List queued: %v", err)
	}
	if len(queued) != 1 {
		t.Errorf("expected 1 queued job (job_list_b), got %d", len(queued))
	}
}

func TestJobRepo_List_WithLimit(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		j := &job.Job{ID: "job_lim_" + string(rune('a'+i)), Type: "test", Status: job.StatusQueued}
		repo.Create(ctx, j)
	}

	limited, err := repo.List(ctx, job.Filter{Limit: 3})
	if err != nil {
		t.Fatalf("List limit: %v", err)
	}
	if len(limited) != 3 {
		t.Errorf("expected 3 jobs with limit, got %d", len(limited))
	}
}

func TestJobRepo_Transition_StatusFlow(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_status", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	if err := repo.Transition(ctx, "job_status", job.StatusQueued, job.StatusRunning); err != nil {
		t.Fatalf("Transition queued→running: %v", err)
	}

	got, _ := repo.Get(ctx, "job_status")
	if got.Status != job.StatusRunning {
		t.Errorf("status after transition: got %s", got.Status)
	}
	if got.StartedAt == nil || got.StartedAt.IsZero() {
		t.Error("started_at should be set when transitioning to running")
	}

	if err := repo.Transition(ctx, "job_status", job.StatusRunning, job.StatusCompleted); err != nil {
		t.Fatalf("Transition running→completed: %v", err)
	}
	got, _ = repo.Get(ctx, "job_status")
	if got.Status != job.StatusCompleted {
		t.Errorf("status after complete: got %s", got.Status)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("completed_at should be set")
	}
}

func TestJobRepo_Transition_InvalidTransitionFromTerminal(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_inv", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	// queued → running → completed (valid path)
	repo.Transition(ctx, "job_inv", job.StatusQueued, job.StatusRunning)
	if err := repo.Transition(ctx, "job_inv", job.StatusRunning, job.StatusCompleted); err != nil {
		t.Fatalf("running→completed should succeed: %v", err)
	}

	// completed → running (invalid: terminal → non-terminal)
	err := repo.Transition(ctx, "job_inv", job.StatusCompleted, job.StatusRunning)
	if err == nil {
		t.Fatal("expected error for invalid transition completed→running")
	}
}

func TestJobRepo_ClaimNext(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j1 := &job.Job{ID: "job_claim_1", Type: "script.generate", Status: job.StatusQueued, Priority: 1}
	j2 := &job.Job{ID: "job_claim_2", Type: "script.generate", Status: job.StatusQueued, Priority: 10}
	repo.Create(ctx, j1)
	repo.Create(ctx, j2)

	claimed, err := repo.ClaimNext(ctx, "worker-1", 30, []string{"script.generate"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected a claimed job")
	}
	if claimed.ID != "job_claim_2" {
		t.Errorf("expected priority-10 job, got %s", claimed.ID)
	}
	if claimed.Status != job.StatusRunning {
		t.Errorf("status after claim: got %s", claimed.Status)
	}
	if claimed.WorkerID != "worker-1" {
		t.Errorf("worker: got %s", claimed.WorkerID)
	}
	if claimed.StartedAt == nil || claimed.StartedAt.IsZero() {
		t.Error("started_at should be set")
	}
}

func TestJobRepo_ClaimNext_NoMatch(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_nomatch", Type: "voiceover.batch", Status: job.StatusQueued}
	repo.Create(ctx, j)

	claimed, err := repo.ClaimNext(ctx, "worker-1", 30, []string{"script.generate"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed != nil {
		t.Fatal("expected nil when no queued job matches type filter")
	}
}

func TestJobRepo_Complete(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_complete", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	// ClaimNext transitions queued→running AND provides fencing tokens.
	claimed, err := repo.ClaimNext(ctx, "worker-1", 60, []string{"test"})
	if err != nil {
		t.Fatalf("ClaimNext for Complete: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected to claim job for Complete test")
	}

	result := json.RawMessage(`{"output":"done","images":5}`)
	if err := repo.Complete(ctx, "job_complete", result); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, _ := repo.Get(ctx, "job_complete")
	if got.Status != job.StatusCompleted {
		t.Errorf("status: got %s", got.Status)
	}
	if got.Progress != 100 {
		t.Errorf("progress: got %d, want 100", got.Progress)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("completed_at should be set")
	}
}

func TestJobRepo_Fail(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_fail", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	// ClaimNext transitions queued→running AND provides fencing tokens.
	claimed, err := repo.ClaimNext(ctx, "worker-1", 60, []string{"test"})
	if err != nil {
		t.Fatalf("ClaimNext for Fail: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected to claim job for Fail test")
	}

	if err := repo.Fail(ctx, "job_fail", "something went wrong"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	got, _ := repo.Get(ctx, "job_fail")
	if got.Status != job.StatusFailed {
		t.Errorf("status: got %s", got.Status)
	}
	if got.Error != "something went wrong" {
		t.Errorf("error: got %s", got.Error)
	}
}

func TestJobRepo_Cancel(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_cancel", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	if err := repo.Cancel(ctx, "job_cancel"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, _ := repo.Get(ctx, "job_cancel")
	if got.Status != job.StatusCancelled {
		t.Errorf("status: got %s", got.Status)
	}
}

func TestJobRepo_Retry(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{
		ID: "job_retry", Type: "test",
		Status: job.StatusQueued, MaxRetries: 5,
	}
	repo.Create(ctx, j)

	// ClaimNext gives us a proper lease + running state.
	claimed, err := repo.ClaimNext(ctx, "worker-retry", 60, []string{"test"})
	if err != nil {
		t.Fatalf("ClaimNext for Retry: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected to claim job for Retry test")
	}

	// Fail it first (uses claim from the Get inside Fail).
	if err := repo.Fail(ctx, "job_retry", "will retry"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Now retry via Retry() method.
	if err := repo.Retry(ctx, "job_retry"); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	got, _ := repo.Get(ctx, "job_retry")
	if got.Status != job.StatusQueued {
		t.Errorf("status: got %s, want queued", got.Status)
	}
	if got.RetryCount < 1 {
		t.Errorf("retry_count: got %d, want >= 1", got.RetryCount)
	}
	if got.WorkerID != "" {
		t.Errorf("worker_id should be cleared on retry, got %q", got.WorkerID)
	}
}

func TestJobRepo_Retry_Exhausted(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{
		ID: "job_exh", Type: "test",
		Status: job.StatusQueued, MaxRetries: 2,
	}
	repo.Create(ctx, j)

	// Cycle 1: ClaimNext → Fail → Retry (retry_count = 1)
	claimed, err := repo.ClaimNext(ctx, "worker-exh", 60, []string{"test"})
	if err != nil {
		t.Fatalf("ClaimNext 1: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected to claim job")
	}
	if err := repo.Fail(ctx, "job_exh", "try 1"); err != nil {
		t.Fatalf("Fail 1: %v", err)
	}
	if err := repo.Retry(ctx, "job_exh"); err != nil {
		t.Fatalf("Retry 1: %v", err)
	}

	// Cycle 2: ClaimNext → Fail → Retry (retry_count = 2)
	claimed2, err := repo.ClaimNext(ctx, "worker-exh2", 60, []string{"test"})
	if err != nil {
		t.Fatalf("ClaimNext 2: %v", err)
	}
	if claimed2 == nil {
		t.Fatal("expected to claim job on retry")
	}
	if err := repo.Fail(ctx, "job_exh", "try 2"); err != nil {
		t.Fatalf("Fail 2: %v", err)
	}
	if err := repo.Retry(ctx, "job_exh"); err != nil {
		t.Fatalf("Retry 2: %v", err)
	}

	// Cycle 3: ClaimNext → Fail → Retry (retry_count = 2, max_retries = 2 → exhausted)
	claimed3, err := repo.ClaimNext(ctx, "worker-exh3", 60, []string{"test"})
	if err != nil {
		t.Fatalf("ClaimNext 3: %v", err)
	}
	if claimed3 == nil {
		t.Fatal("expected to claim job")
	}
	if err := repo.Fail(ctx, "job_exh", "try 3"); err != nil {
		t.Fatalf("Fail 3: %v", err)
	}

	err = repo.Retry(ctx, "job_exh")
	if err == nil {
		t.Fatal("expected error when retries exhausted (retry_count=2, max_retries=2)")
	}
}

func TestJobRepo_Retry_NotFailed(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_nf", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	err := repo.Retry(ctx, "job_nf")
	if err == nil {
		t.Fatal("expected error when retrying non-failed job")
	}
}

func TestJobRepo_Transition_SetsTimestamps(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_ts", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	if err := repo.Transition(ctx, "job_ts", job.StatusQueued, job.StatusRunning); err != nil {
		t.Fatalf("Transition queued→running: %v", err)
	}
	got, _ := repo.Get(ctx, "job_ts")
	if got.StartedAt == nil || got.StartedAt.IsZero() {
		t.Errorf("started_at should be set on queued→running, got StartedAt=%#v", got.StartedAt)
	}

	if err := repo.Transition(ctx, "job_ts", job.StatusRunning, job.StatusCompleted); err != nil {
		t.Fatalf("Transition running→completed: %v", err)
	}
	got, _ = repo.Get(ctx, "job_ts")
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Errorf("completed_at should be set on running→completed, got CompletedAt=%#v", got.CompletedAt)
	}
}

func TestCompileTimeJobInterface(t *testing.T) {
	var repo job.Repository = NewSQLiteJobRepository(nil)
	_ = repo
}

func TestJobRepo_WorkflowColumns(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{
		ID:             "job_wf",
		Type:           "test",
		WorkflowID:     "wf_abc123",
		WorkflowStepID: "wf_abc123_step_0_xyz",
	}
	repo.Create(ctx, j)

	got, _ := repo.Get(ctx, "job_wf")
	if got.WorkflowID != "wf_abc123" {
		t.Errorf("WorkflowID: got %s, want wf_abc123", got.WorkflowID)
	}
	if got.WorkflowStepID != "wf_abc123_step_0_xyz" {
		t.Errorf("WorkflowStepID: got %s", got.WorkflowStepID)
	}
}

func TestJobRepo_Transition(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_tr", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	if err := repo.Transition(ctx, "job_tr", job.StatusQueued, job.StatusRunning); err != nil {
		t.Fatalf("Transition queued→running: %v", err)
	}
	got, _ := repo.Get(ctx, "job_tr")
	if got.Status != job.StatusRunning {
		t.Errorf("status: got %s, want running", got.Status)
	}
	if got.StartedAt == nil || got.StartedAt.IsZero() {
		t.Error("started_at should be set on queued→running")
	}

	if err := repo.Transition(ctx, "job_tr", job.StatusRunning, job.StatusCompleted); err != nil {
		t.Fatalf("Transition running→completed: %v", err)
	}
	got, _ = repo.Get(ctx, "job_tr")
	if got.Status != job.StatusCompleted {
		t.Errorf("status: got %s, want completed", got.Status)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("completed_at should be set on running→completed")
	}
}

func TestJobRepo_Transition_Conflict(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_conflict", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	repo.Transition(ctx, "job_conflict", job.StatusQueued, job.StatusRunning)

	// Try queued→cancelled (wrong from state — it's now running).
	err := repo.Transition(ctx, "job_conflict", job.StatusQueued, job.StatusCancelled)
	if err == nil {
		t.Fatal("expected error for transition conflict")
	}
}

func TestJobRepo_Transition_Invalid(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_inv_tr", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	// queued → completed is an invalid state machine transition
	// (must go through running first).
	err := repo.Transition(ctx, "job_inv_tr", job.StatusQueued, job.StatusCompleted)
	if err == nil {
		t.Fatal("expected error for invalid transition queued→completed")
	}

	// Also test terminal → non-terminal via the running→completed path.
	ensureRunning(t, repo, ctx, "job_inv_tr")
	repo.Transition(ctx, "job_inv_tr", job.StatusRunning, job.StatusCompleted)
	err = repo.Transition(ctx, "job_inv_tr", job.StatusCompleted, job.StatusRunning)
	if err == nil {
		t.Fatal("expected error for invalid transition completed→running")
	}
}

func TestJobRepo_Transition_Fail(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_tr_fail", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	repo.Transition(ctx, "job_tr_fail", job.StatusQueued, job.StatusRunning)
	if err := repo.Transition(ctx, "job_tr_fail", job.StatusRunning, job.StatusFailed); err != nil {
		t.Fatalf("Transition running→failed: %v", err)
	}

	got, _ := repo.Get(ctx, "job_tr_fail")
	if got.Status != job.StatusFailed {
		t.Errorf("status: got %s, want failed", got.Status)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("completed_at should be set on fail")
	}
}

func TestJobRepo_Transition_Cancel(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_tr_cancel", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	if err := repo.Transition(ctx, "job_tr_cancel", job.StatusQueued, job.StatusCancelled); err != nil {
		t.Fatalf("Transition queued→cancelled: %v", err)
	}

	got, _ := repo.Get(ctx, "job_tr_cancel")
	if got.Status != job.StatusCancelled {
		t.Errorf("status: got %s, want cancelled", got.Status)
	}
}

func TestJobRepo_Transition_Retry(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_tr_retry", Type: "test", Status: job.StatusQueued, MaxRetries: 3}
	repo.Create(ctx, j)

	repo.Transition(ctx, "job_tr_retry", job.StatusQueued, job.StatusRunning)
	repo.Transition(ctx, "job_tr_retry", job.StatusRunning, job.StatusFailed)

	if err := repo.Transition(ctx, "job_tr_retry", job.StatusFailed, job.StatusQueued); err != nil {
		t.Fatalf("Transition failed→queued: %v", err)
	}

	got, _ := repo.Get(ctx, "job_tr_retry")
	if got.Status != job.StatusQueued {
		t.Errorf("status: got %s, want queued", got.Status)
	}
	if got.RetryCount != 1 {
		t.Errorf("retry_count: got %d, want 1", got.RetryCount)
	}
	if got.WorkerID != "" {
		t.Errorf("worker_id should be cleared on retry, got %q", got.WorkerID)
	}
	if got.StartedAt != nil {
		t.Error("started_at should be cleared on retry")
	}
	if got.CompletedAt != nil {
		t.Error("completed_at should be cleared on retry")
	}
}

func TestJobRepo_PayloadRoundtrip(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	payload := json.RawMessage(`{"topic":"AI","nested":{"depth":2},"array":[1,2,3]}`)
	j := &job.Job{ID: "job_pl", Type: "test", Payload: payload}
	repo.Create(ctx, j)

	got, _ := repo.Get(ctx, "job_pl")

	var expected, actual map[string]any
	json.Unmarshal(payload, &expected)
	json.Unmarshal(got.Payload, &actual)

	if expected["topic"] != actual["topic"] {
		t.Errorf("topic: got %v", actual["topic"])
	}
	if arr, ok := actual["array"].([]any); !ok || len(arr) != 3 {
		t.Errorf("array roundtrip failed: %v", actual["array"])
	}
}

func TestJobRepo_ResultRoundtrip(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	j := &job.Job{ID: "job_res", Type: "test", Status: job.StatusQueued}
	repo.Create(ctx, j)

	// ClaimNext gives running + fencing tokens.
	claimed, err := repo.ClaimNext(ctx, "worker-result", 60, []string{"test"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected to claim job")
	}

	result := json.RawMessage(`{"images":3,"url":"https://example.com/video.mp4"}`)
	if err := repo.Complete(ctx, "job_res", result); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, _ := repo.Get(ctx, "job_res")
	if got.Result == nil {
		t.Fatal("Result should not be nil")
	}

	var gotResult map[string]any
	if err := json.Unmarshal(got.Result, &gotResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if gotResult["images"] != float64(3) {
		t.Errorf("result.images: got %v", gotResult["images"])
	}
	if gotResult["url"] != "https://example.com/video.mp4" {
		t.Errorf("result.url: got %v", gotResult["url"])
	}
}

func TestJobRepo_Transition_JobNotFound(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	err := repo.Transition(ctx, "nonexistent", job.StatusQueued, job.StatusRunning)
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}

func TestJobRepo_Complete_JobNotFound(t *testing.T) {
	repo, ctx, _, cleanup := setupJobRepo(t)
	defer cleanup()

	err := repo.Complete(ctx, "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}
