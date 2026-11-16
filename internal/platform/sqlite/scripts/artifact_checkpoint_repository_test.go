// Package scripts_test — adapter coverage for run_artifact_checkpoints.
//
// The tests use the real observability migrations (not a hand-written schema)
// so they also catch drift between the adapter SQL and 296: the composite
// identity, the run_id cascade and the payload NOT NULL constraints are all
// exercised through the production DDL.
package scripts_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	platformsqlite "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
)

func newArtifactCheckpointTestRepo(t *testing.T) (*sqlitescripts.SQLiteArtifactCheckpointRepository, *sql.DB) {
	t.Helper()
	migrationsDir, err := filepath.Abs("../../../../migrations/sqlite")
	require.NoError(t, err)
	dbPath := filepath.Join(t.TempDir(), "artifact-checkpoints.sqlite")
	require.NoError(t, platformsqlite.RunMigrationsOnDB(dbPath, zap.NewNop(), migrationsDir, "observability"))

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo, err := sqlitescripts.NewSQLiteArtifactCheckpointRepository(db)
	require.NoError(t, err)
	return repo, db
}

func insertArtifactCheckpointRun(t *testing.T, db *sql.DB, runID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	attemptID := "attempt-" + runID
	_, err := db.Exec(`INSERT INTO job_attempts
		(attempt_id, job_id, run_id, status, created_at, updated_at)
		VALUES (?, ?, ?, 'RUNNING', ?, ?)`, attemptID, runID, runID, now, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run_observability
		(run_id, job_id, attempt_id, status, created_at, started_at, updated_at)
		VALUES (?, ?, ?, 'RUNNING', ?, ?, ?)`, runID, runID, attemptID, now, now, now)
	require.NoError(t, err)
}

func artifactCheckpointFixture(runID string) scriptgen.ArtifactCheckpoint {
	artifactSHA := strings.Repeat("a", 64)
	return scriptgen.ArtifactCheckpoint{
		RunID:              runID,
		Kind:               scriptgen.ArtifactCheckpointLocalizedRender,
		Key:                "scene-0/clip-1/en",
		SchemaVersion:      scriptgen.ArtifactCheckpointSchemaVersion,
		InputFingerprint:   strings.Repeat("1", 64),
		ProcessorVersion:   "chronon/v1",
		ArtifactID:         "asset-1",
		ArtifactSHA256:     artifactSHA,
		ArtifactURI:        "cas://" + artifactSHA,
		ArtifactSizeBytes:  4096,
		ArtifactDurationUS: 5_000_000,
		Payload:            json.RawMessage(`{"asset_id":"asset-1","language":"en"}`),
		Attempt:            2,
		CompletedAt:        time.Date(2026, 11, 16, 12, 0, 0, 123456789, time.UTC),
	}
}

func TestArtifactCheckpointRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)
	const runID = "run-artifact-roundtrip"
	insertArtifactCheckpointRun(t, db, runID)

	input := artifactCheckpointFixture(runID)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, input))

	got, err := repo.GetArtifactCheckpoint(ctx, runID, input.Kind, input.Key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, input.RunID, got.RunID)
	require.Equal(t, input.Kind, got.Kind)
	require.Equal(t, input.Key, got.Key)
	require.Equal(t, input.SchemaVersion, got.SchemaVersion)
	require.Equal(t, input.InputFingerprint, got.InputFingerprint)
	require.Equal(t, input.ProcessorVersion, got.ProcessorVersion)
	require.Equal(t, input.ArtifactID, got.ArtifactID)
	require.Equal(t, input.ArtifactSHA256, got.ArtifactSHA256)
	require.Equal(t, input.ArtifactURI, got.ArtifactURI)
	require.Equal(t, input.ArtifactSizeBytes, got.ArtifactSizeBytes)
	require.Equal(t, input.ArtifactDurationUS, got.ArtifactDurationUS)
	require.Equal(t, []byte(input.Payload), []byte(got.Payload))
	require.Equal(t, input.PayloadDigest(), got.PayloadSHA256,
		"the adapter must compute the digest of the exact stored payload bytes")
	require.Equal(t, input.Attempt, got.Attempt)
	require.True(t, got.CompletedAt.Equal(input.CompletedAt))
	require.False(t, got.CreatedAt.IsZero())
	require.Equal(t, got.CreatedAt, got.UpdatedAt, "a first write stamps both timestamps from the same now")

	missing, err := repo.GetArtifactCheckpoint(ctx, runID, input.Kind, "scene-0/clip-1/it")
	require.NoError(t, err)
	require.Nil(t, missing, "a missing checkpoint is a miss, not an error")

	require.NoError(t, repo.InvalidateArtifactCheckpoint(ctx, runID, input.Kind, input.Key))
	require.NoError(t, repo.InvalidateArtifactCheckpoint(ctx, runID, input.Kind, input.Key),
		"invalidation must be idempotent")
	missing, err = repo.GetArtifactCheckpoint(ctx, runID, input.Kind, input.Key)
	require.NoError(t, err)
	require.Nil(t, missing)
}

// TestArtifactCheckpointRepositoryForeignKeysOnRunLedger pins the live defect
// that the first rollout exposed: a broker job id is not a run_id. The table's
// FK references run_observability.run_id, so writing the checkpoint under the
// job id must fail instead of silently creating an orphan with a plausible
// identity.
func TestArtifactCheckpointRepositoryForeignKeysOnRunLedger(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)
	const runID = "run-fk-durable"
	const jobID = "job-fk-broker"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	attemptID := "attempt-" + runID
	_, err := db.Exec(`INSERT INTO job_attempts
		(attempt_id, job_id, run_id, status, created_at, updated_at)
		VALUES (?, ?, ?, 'RUNNING', ?, ?)`, attemptID, jobID, runID, now, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run_observability
		(run_id, job_id, attempt_id, status, created_at, started_at, updated_at)
		VALUES (?, ?, ?, 'RUNNING', ?, ?, ?)`, runID, jobID, attemptID, now, now, now)
	require.NoError(t, err)

	checkpoint := artifactCheckpointFixture(runID)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, checkpoint))

	wrong := artifactCheckpointFixture(jobID)
	wrong.Key = "scene-0/clip-1/fr"
	err = repo.SaveArtifactCheckpoint(ctx, wrong)
	require.Error(t, err, "the broker job id must not be accepted as a checkpoint run_id")
	require.Contains(t, err.Error(), "FOREIGN KEY constraint failed")

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run_artifact_checkpoints WHERE run_id = ?`, jobID).Scan(&count))
	require.Zero(t, count, "a rejected write must leave no orphan checkpoint")
}

func TestArtifactCheckpointRepositoryUpsertKeepsOneRowAndCreatedAt(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)
	const runID = "run-artifact-upsert"
	insertArtifactCheckpointRun(t, db, runID)

	first := artifactCheckpointFixture(runID)
	first.CreatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, first))
	before, err := repo.GetArtifactCheckpoint(ctx, runID, first.Kind, first.Key)
	require.NoError(t, err)
	require.NotNil(t, before)

	second := first
	second.Payload = json.RawMessage(`{"asset_id":"asset-2","language":"en"}`)
	second.PayloadSHA256 = ""
	second.ArtifactID = "asset-2"
	second.ArtifactSHA256 = strings.Repeat("b", 64)
	second.ArtifactURI = "cas://" + second.ArtifactSHA256
	second.Attempt = 3
	second.CompletedAt = first.CompletedAt.Add(time.Minute)
	second.CreatedAt = time.Time{}
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, second))

	after, err := repo.GetArtifactCheckpoint(ctx, runID, second.Kind, second.Key)
	require.NoError(t, err)
	require.NotNil(t, after)
	require.Equal(t, second.PayloadDigest(), after.PayloadSHA256)
	require.Equal(t, []byte(second.Payload), []byte(after.Payload))
	require.Equal(t, second.ArtifactID, after.ArtifactID)
	require.Equal(t, second.Attempt, after.Attempt)
	require.True(t, after.CompletedAt.Equal(second.CompletedAt))
	require.True(t, after.CreatedAt.Equal(first.CreatedAt),
		"upsert must preserve the original created_at")
	require.False(t, after.UpdatedAt.Before(before.UpdatedAt),
		"upsert must refresh updated_at")

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_artifact_checkpoints
		WHERE run_id = ? AND artifact_kind = ? AND artifact_key = ?`,
		runID, second.Kind, second.Key).Scan(&count))
	require.Equal(t, 1, count, "re-saving the same identity must converge on one row")
}

func TestArtifactCheckpointRepositoryListIsOrderedAndRunScoped(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)
	const runA = "run-artifact-list-a"
	const runB = "run-artifact-list-b"
	insertArtifactCheckpointRun(t, db, runA)
	insertArtifactCheckpointRun(t, db, runB)

	localizedIT := artifactCheckpointFixture(runA)
	localizedIT.Key = "scene-0/clip-1/it"
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, localizedIT))

	finalAudio := artifactCheckpointFixture(runA)
	finalAudio.Kind = scriptgen.ArtifactCheckpointFinalAudio
	finalAudio.Key = "global"
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, finalAudio))

	localizedEN := artifactCheckpointFixture(runA)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, localizedEN))

	otherRun := artifactCheckpointFixture(runB)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, otherRun))

	got, err := repo.ListArtifactCheckpoints(ctx, runA)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []scriptgen.ArtifactCheckpointKind{
		scriptgen.ArtifactCheckpointFinalAudio,
		scriptgen.ArtifactCheckpointLocalizedRender,
		scriptgen.ArtifactCheckpointLocalizedRender,
	}, []scriptgen.ArtifactCheckpointKind{got[0].Kind, got[1].Kind, got[2].Kind})
	require.Equal(t, "global", got[0].Key)
	require.Equal(t, "scene-0/clip-1/en", got[1].Key)
	require.Equal(t, "scene-0/clip-1/it", got[2].Key)

	other, err := repo.ListArtifactCheckpoints(ctx, runB)
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, runB, other[0].RunID)
}

func TestArtifactCheckpointRepositoryRejectsCorruptPayload(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)
	const runID = "run-artifact-corrupt"
	insertArtifactCheckpointRun(t, db, runID)
	input := artifactCheckpointFixture(runID)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, input))

	_, err := db.ExecContext(ctx, `UPDATE run_artifact_checkpoints
		SET payload_json = ? WHERE run_id = ? AND artifact_kind = ? AND artifact_key = ?`,
		`{"asset_id":"tampered"}`, runID, input.Kind, input.Key)
	require.NoError(t, err)

	got, err := repo.GetArtifactCheckpoint(ctx, runID, input.Kind, input.Key)
	require.Nil(t, got)
	require.ErrorIs(t, err, scriptgen.ErrArtifactCheckpointCorrupt)

	list, err := repo.ListArtifactCheckpoints(ctx, runID)
	require.Nil(t, list)
	require.ErrorIs(t, err, scriptgen.ErrArtifactCheckpointCorrupt)
}

func TestArtifactCheckpointRepositoryRejectsInvalidJSONEvenWithMatchingDigest(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)
	const runID = "run-artifact-bad-json"
	insertArtifactCheckpointRun(t, db, runID)
	input := artifactCheckpointFixture(runID)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, input))

	bad := []byte(`{"asset_id":`)
	_, err := db.ExecContext(ctx, `UPDATE run_artifact_checkpoints
		SET payload_json = ?, payload_sha256 = ?
		WHERE run_id = ? AND artifact_kind = ? AND artifact_key = ?`,
		string(bad), digest.SHA256Bytes(bad), runID, input.Kind, input.Key)
	require.NoError(t, err)

	got, err := repo.GetArtifactCheckpoint(ctx, runID, input.Kind, input.Key)
	require.Nil(t, got)
	require.ErrorIs(t, err, scriptgen.ErrArtifactCheckpointCorrupt,
		"a matching digest must not make malformed JSON readable")
}

func TestArtifactCheckpointRepositoryRunDeleteCascades(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)
	const deletedRun = "run-artifact-cascade-deleted"
	const keptRun = "run-artifact-cascade-kept"
	insertArtifactCheckpointRun(t, db, deletedRun)
	insertArtifactCheckpointRun(t, db, keptRun)

	deletedCheckpoint := artifactCheckpointFixture(deletedRun)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, deletedCheckpoint))
	keptCheckpoint := artifactCheckpointFixture(keptRun)
	require.NoError(t, repo.SaveArtifactCheckpoint(ctx, keptCheckpoint))

	_, err := db.ExecContext(ctx, `DELETE FROM run_observability WHERE run_id = ?`, deletedRun)
	require.NoError(t, err)

	var deletedCount, keptCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_artifact_checkpoints WHERE run_id = ?`, deletedRun).Scan(&deletedCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_artifact_checkpoints WHERE run_id = ?`, keptRun).Scan(&keptCount))
	require.Zero(t, deletedCount, "deleting a run must cascade to its artifact checkpoints")
	require.Equal(t, 1, keptCount, "the cascade must not touch another run's checkpoints")
}

func TestArtifactCheckpointRepositoryRejectsInvalidIdentityBeforeSQL(t *testing.T) {
	ctx := context.Background()
	repo, db := newArtifactCheckpointTestRepo(t)

	invalid := artifactCheckpointFixture("")
	require.ErrorIs(t, repo.SaveArtifactCheckpoint(ctx, invalid), scriptgen.ErrInvalidArtifactCheckpoint)

	_, err := repo.GetArtifactCheckpoint(ctx, "run-invalid", "made_up", "key")
	require.True(t, errors.Is(err, scriptgen.ErrInvalidArtifactCheckpoint))

	_, err = repo.ListArtifactCheckpoints(ctx, "")
	require.True(t, errors.Is(err, scriptgen.ErrInvalidArtifactCheckpoint))

	require.ErrorIs(t, repo.InvalidateArtifactCheckpoint(ctx, "run-invalid", scriptgen.ArtifactCheckpointSceneText, ""), scriptgen.ErrInvalidArtifactCheckpoint)

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_artifact_checkpoints`).Scan(&count))
	require.Zero(t, count, "invalid identities must be rejected before any SQL write")
}
