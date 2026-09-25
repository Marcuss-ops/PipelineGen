package videocreate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	steps "github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	executionsteps "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/executionsteps"

	_ "github.com/mattn/go-sqlite3"
)

// TestRecovery_SQLiteStore_RestartAndReplay is the §7/§26 acceptance
// test against the PRODUCTION durable store (executionsteps.NewSQLiteStore), not
// a fake: two handler lifetimes over ONE SQLite file, with a process
// death mid-render between them.
//
// It pins exactly the two criteria the runbook lists:
//
//	Restart → the workflow resumes at the first non-completed step
//	          (07_render) instead of re-running script/media/voiceover;
//	Replay  → no duplicate children and no second publication.
//
// The "process death" is simulated by interrupting the wait on the
// second render child (the children stay alive, exactly like a worker
// that died before observing their terminal state), then closing and
// reopening the database — a real second process lifetime.
func TestRecovery_SQLiteStore_RestartAndReplay(t *testing.T) {
	children := newFakeChildren()
	deps, _, publisher := newTestDeps(t, children, fakeProbe{})

	dbPath := filepath.Join(t.TempDir(), "execution_steps.db")
	openStore := func() (*sql.DB, steps.Store) {
		db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		// Canonical migrations inline (121 + 122), the same hermetic
		// form steps' own store test uses. Production gets the table
		// from migrations/sqlite/121_execution_steps.sql.
		if _, err := db.Exec(`
			CREATE TABLE IF NOT EXISTS execution_steps (
			    id INTEGER PRIMARY KEY AUTOINCREMENT,
			    job_id TEXT NOT NULL,
			    step_key TEXT NOT NULL,
			    input_fingerprint TEXT NOT NULL,
			    status TEXT NOT NULL DEFAULT 'pending',
			    attempt INTEGER NOT NULL DEFAULT 0,
			    result_json TEXT NOT NULL DEFAULT '{}',
			    artifact_refs_json TEXT NOT NULL DEFAULT '[]',
			    started_at TEXT NOT NULL DEFAULT '',
			    completed_at TEXT NOT NULL DEFAULT '',
			    last_error TEXT NOT NULL DEFAULT ''
			);
			CREATE UNIQUE INDEX IF NOT EXISTS uniq_execution_steps_dedup
			    ON execution_steps (job_id, step_key, input_fingerprint);
			CREATE INDEX IF NOT EXISTS ix_execution_steps_resume
			    ON execution_steps (job_id, status, step_key);
			CREATE INDEX IF NOT EXISTS ix_execution_steps_audit
			    ON execution_steps (job_id, step_key);
		`); err != nil {
			t.Fatalf("open sqlite: apply execution_steps migrations: %v", err)
		}
		// Migration 122 is an ALTER: tolerant here because BOTH process
		// lifetimes apply this block to the SAME file.
		_, _ = db.Exec(`ALTER TABLE execution_steps ADD COLUMN lease_until TEXT NOT NULL DEFAULT ''`)
		if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS ix_execution_steps_leased_stale
			ON execution_steps (lease_until) WHERE lease_until != ''`); err != nil {
			t.Fatalf("open sqlite: apply execution_steps lease index: %v", err)
		}
		return db, executionsteps.NewSQLiteStore(db)
	}

	// ── Process lifetime 1: dies at render 40% ───────────────────────
	db1, store1 := openStore()
	deps.Steps = store1
	handler1, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	children.failNextWaitOnce("calendar:item-128:2026-09-24:render:scene:002")
	j := testJob("job_restart", "calendar:item-128:2026-09-24")
	if _, err := handler1(context.Background(), j, nil); err == nil {
		t.Fatal("first lifetime: want interruption at render, got success")
	} else if !errors.Is(err, ErrWorkflowFailed) {
		t.Fatalf("first lifetime error = %v, want ErrWorkflowFailed", err)
	}
	countAfterCrash := children.enqueueCount()

	rows1, err := store1.ListByJob(context.Background(), j.ID)
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	state1, err := StateFromSteps(rows1)
	if err != nil {
		t.Fatalf("StateFromSteps after crash: %v", err)
	}
	for _, key := range []string{"01_script", "02_media_search", "03_media_acquire", "04_voiceover", "05_audio_master", "06_overlay_plan"} {
		if !state1.Completed(key) {
			t.Fatalf("step %s not durably completed before the crash", key)
		}
	}
	if rec := state1.StageRecordFor("07_render"); rec == nil || rec.Status != StageFailed {
		t.Fatalf("07_render after crash = %v, want FAILED", rec)
	}
	if publisher.calls != 0 {
		t.Fatalf("publisher calls = %d before success, want 0", publisher.calls)
	}
	_ = db1.Close()

	// ── Process lifetime 2: the same job is redelivered ──────────────
	db2, store2 := openStore()
	defer db2.Close()
	deps.Steps = store2
	handler2, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler (second lifetime): %v", err)
	}
	res, err := handler2(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("second lifetime: %v", err)
	}

	// §26 Replay → nessun duplicato.
	if got := children.enqueueCount(); got != countAfterCrash {
		t.Errorf("resume created NEW children: %d -> %d (§8 idempotency broken)", countAfterCrash, got)
	}
	if publisher.calls != 1 {
		t.Errorf("publisher calls = %d, want exactly 1 across both lifetimes", publisher.calls)
	}

	// §7 Resume: the completed steps were NOT re-executed — the real
	// store holds exactly ONE row per pre-crash step (no second
	// MarkStarted with a new fingerprint).
	rows2, err := store2.ListByJob(context.Background(), j.ID)
	if err != nil {
		t.Fatalf("ListByJob (second lifetime): %v", err)
	}
	rowsPerStep := map[string]int{}
	for _, row := range rows2 {
		rowsPerStep[row.StepKey]++
	}
	for _, key := range []string{"01_script", "02_media_search", "03_media_acquire", "04_voiceover", "05_audio_master", "06_overlay_plan"} {
		if rowsPerStep[key] != 1 {
			t.Errorf("step %s has %d rows after resume, want exactly 1 (re-executed!)", key, rowsPerStep[key])
		}
	}

	// The resumed run produced the full typed result.
	finalVideo, _ := res["final_video"].(map[string]any)
	if finalVideo == nil || finalVideo["media_url"] == "" || finalVideo["sha256"] == "" {
		t.Fatalf("resumed result has no published final_video: %v", res)
	}
	completed := res["completed_stages"]
	if completed == nil {
		t.Fatalf("resumed result has no completed_stages: %v", res)
	}
}
