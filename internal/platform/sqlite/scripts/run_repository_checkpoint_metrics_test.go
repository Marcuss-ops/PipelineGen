// Package scripts — run_repository_checkpoint_metrics_test.go.
//
// TDD coverage for the checkpoint write-amplification byte counter. Uses
// in-memory SQLite (the existing scripts_test.go pattern via
// sql.Open("sqlite3", ":memory:") + the `_ "github.com/mattn/go-sqlite3"`
// driver import — AGENTS.md locks the driver).
//
// Why it is pinned: the counter is what turns "the voiceover phase checkpoints
// the whole result once per (scene, language)" from a code reading into a
// measured bytes-per-run number. It must count the payload SQLite actually
// received, so the assertion compares the metric delta against the row read
// back — not against an estimate.
package scripts

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

const runRepositoryCheckpointTestSchema = `
	CREATE TABLE job_attempts (
		attempt_id     TEXT PRIMARY KEY,
		job_id         TEXT NOT NULL DEFAULT '',
		run_id         TEXT NOT NULL DEFAULT '',
		attempt_number INTEGER NOT NULL DEFAULT 1,
		status         TEXT NOT NULL DEFAULT '',
		created_at     TEXT NOT NULL DEFAULT '',
		updated_at     TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE run_observability (
		run_id                TEXT PRIMARY KEY,
		job_id                TEXT NOT NULL DEFAULT '',
		job_type              TEXT NOT NULL DEFAULT '',
		attempt_id            TEXT NOT NULL DEFAULT '',
		status                TEXT NOT NULL DEFAULT '',
		created_at            TEXT NOT NULL DEFAULT '',
		updated_at            TEXT NOT NULL DEFAULT '',
		started_at            TEXT,
		finished_at           TEXT,
		report_json           TEXT NOT NULL DEFAULT '{}',
		workflow_payload_json TEXT NOT NULL DEFAULT '',
		error_code            TEXT NOT NULL DEFAULT '',
		error                 TEXT NOT NULL DEFAULT ''
	);
`

func newRunRepositoryTestRepo(t *testing.T) (*SQLiteRunRepository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(runRepositoryCheckpointTestSchema)
	require.NoError(t, err)
	repo, err := NewSQLiteRunRepository(db, zap.NewNop())
	require.NoError(t, err)
	return repo, db
}

// TestSavePartialResultCountsTheSerializedCheckpointBytes pins the byte counter
// at the exact place the payload is produced: the metric delta must equal the
// serialized length of the checkpoint row SQLite stored.
func TestSavePartialResultCountsTheSerializedCheckpointBytes(t *testing.T) {
	ctx := context.Background()
	repo, db := newRunRepositoryTestRepo(t)
	runID := "run-checkpoint-bytes"
	require.NoError(t, repo.Create(ctx, &scriptgen.GenerationRun{
		ID:           runID,
		Request:      scriptgen.GenerateRequest{IdempotencyKey: "bytes-key", Title: "byte amplification"},
		Status:       scriptgen.RunStatusPending,
		CurrentStage: scriptgen.StageNormalizing,
	}))

	before := testutil.ToFloat64(observability.ScriptCheckpointBytesTotal)
	require.NoError(t, repo.SavePartialResult(ctx, runID, &scriptgen.GenerateResult{
		Title:  "byte amplification",
		Scenes: []scriptgen.Scene{{ID: "scene-0", Index: 0}},
	}))
	delta := testutil.ToFloat64(observability.ScriptCheckpointBytesTotal) - before
	require.Greater(t, delta, float64(0), "a persisted checkpoint must contribute its serialized bytes")

	var payload string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT workflow_payload_json FROM run_observability WHERE run_id=?`, runID).Scan(&payload))
	require.Equal(t, float64(len([]byte(payload))), delta,
		"the counter must measure the payload SQLite received, not an estimate")
}
