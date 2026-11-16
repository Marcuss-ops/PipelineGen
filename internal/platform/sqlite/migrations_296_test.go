// Package sqlite — migrations_296_test.go pins the observable schema contract
// of the per-artifact checkpoint table: the composite resume identity, the
// payload NOT NULL columns, and the run_id cascade. The generic schema
// contract test already checks every declared column; this test proves the two
// behaviours the repository API depends on.
package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestMigration296RunArtifactCheckpointsSchemaAndRunCascade(t *testing.T) {
	migrationsDir, err := filepath.Abs("../../../migrations/sqlite")
	require.NoError(t, err)
	dbPath := filepath.Join(t.TempDir(), "artifact-checkpoints.sqlite")
	require.NoError(t, RunMigrationsOnDB(dbPath, zaptest.NewLogger(t), migrationsDir, "observability"))

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const now = "2026-11-16T12:00:00Z"
	_, err = db.Exec(`INSERT INTO job_attempts (attempt_id, job_id, run_id, status, created_at, updated_at)
		VALUES (?, ?, ?, 'RUNNING', ?, ?)`, "attempt-296", "job-296", "run-296", now, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run_observability (run_id, job_id, attempt_id, status, created_at, started_at, updated_at)
		VALUES (?, ?, ?, 'RUNNING', ?, ?, ?)`, "run-296", "job-296", "attempt-296", now, now, now)
	require.NoError(t, err)

	insert := func(kind, key, payloadSHA string) error {
		_, err := db.Exec(`INSERT INTO run_artifact_checkpoints
			(run_id, artifact_kind, artifact_key, schema_version, input_fingerprint, processor_version,
			 artifact_id, artifact_sha256, artifact_uri, artifact_size_bytes, artifact_duration_us,
			 payload_json, payload_sha256, attempt, completed_at, created_at, updated_at)
			VALUES (?, ?, ?, 1, ?, 'chronon/v1', 'asset-1', ?, 'cas://asset-1', 10, 5000000, '{}', ?, 1, ?, ?, ?)`,
			"run-296", kind, key, strings.Repeat("1", 64), strings.Repeat("a", 64), payloadSHA, now, now, now)
		return err
	}
	const payloadSHA = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	require.NoError(t, insert("localized_render", "scene-0/clip-1/en", payloadSHA))

	// The composite identity is the resume key: re-inserting the same triple
	// must be rejected at the schema boundary. Upsert semantics belong to the
	// repository adapter, which targets this constraint.
	require.ErrorContains(t, insert("localized_render", "scene-0/clip-1/en", payloadSHA), "UNIQUE constraint failed")
	require.NoError(t, insert("localized_render", "scene-0/clip-1/it", payloadSHA))

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM run_artifact_checkpoints WHERE run_id = ?`, "run-296").Scan(&count))
	require.Equal(t, 2, count)

	_, err = db.Exec(`DELETE FROM run_observability WHERE run_id = ?`, "run-296")
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM run_artifact_checkpoints WHERE run_id = ?`, "run-296").Scan(&count))
	require.Zero(t, count, "deleting a run must cascade to its artifact checkpoints")
}
