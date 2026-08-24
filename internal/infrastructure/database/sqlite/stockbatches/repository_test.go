// Package stockbatches_test pins the canonical Repository contract via
// in-memory SQLite integration tests (Fase 2, July 2026).
//
// godlike/06 SSOT: tests open a fresh `:memory:` database via
// mattn/go-sqlite3, apply migration 162 inline, then construct
// *Repository and exercise every StockBatchRepository method end-to-end.
package stockbatches_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver registration.
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/stockbatches"
)

// migration162Schema is the inline mirror of migrations/sqlite/162_stock_batches.sql.
// Keep in lockstep with the production DDL — divergence is caught by the
// schema_contract_test.go migration contract test suite.
const migration162Schema = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS stock_batches (
    id                TEXT PRIMARY KEY,
    fingerprint       TEXT NOT NULL DEFAULT '',
    source_url        TEXT NOT NULL DEFAULT '',
    source_cache_key  TEXT NOT NULL DEFAULT '',
    root_folder_id    TEXT NOT NULL DEFAULT '',
    root_folder_name  TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'PLANNED'
                      CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    expected_groups   INTEGER NOT NULL DEFAULT 0,
    expected_clips    INTEGER NOT NULL DEFAULT 0,
    verified_clips    INTEGER NOT NULL DEFAULT 0,
    policy_version    TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_stock_batches_status ON stock_batches(status);
CREATE INDEX IF NOT EXISTS idx_stock_batches_source_cache_key ON stock_batches(source_cache_key);
CREATE INDEX IF NOT EXISTS idx_stock_batches_fingerprint ON stock_batches(fingerprint);

CREATE TABLE IF NOT EXISTS stock_batch_groups (
    id               TEXT PRIMARY KEY,
    batch_id         TEXT NOT NULL REFERENCES stock_batches(id) ON DELETE CASCADE,
    group_key        TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL DEFAULT '',
    folder_name      TEXT NOT NULL DEFAULT '',
    drive_folder_id  TEXT NOT NULL DEFAULT '',
    start_sec        REAL NOT NULL DEFAULT 0,
    end_sec          REAL NOT NULL DEFAULT 0,
    expected_clips   INTEGER NOT NULL DEFAULT 0,
    verified_clips   INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'PLANNED'
                     CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    child_job_id     TEXT NOT NULL DEFAULT '',
    attempts         INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_batch_id ON stock_batch_groups(batch_id);
CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_status ON stock_batch_groups(status);
CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_child_job_id ON stock_batch_groups(child_job_id);

CREATE TABLE IF NOT EXISTS stock_artifacts (
    id                   TEXT PRIMARY KEY,
    batch_id             TEXT NOT NULL REFERENCES stock_batches(id) ON DELETE CASCADE,
    group_id             TEXT NOT NULL REFERENCES stock_batch_groups(id) ON DELETE CASCADE,
    ordinal              INTEGER NOT NULL DEFAULT 0,
    artifact_key         TEXT NOT NULL DEFAULT '',
    source_url           TEXT NOT NULL DEFAULT '',
    start_sec            REAL NOT NULL DEFAULT 0,
    end_sec              REAL NOT NULL DEFAULT 0,
    expected_duration_ms INTEGER NOT NULL DEFAULT 0,
    actual_duration_ms   INTEGER NOT NULL DEFAULT 0,
    local_path           TEXT NOT NULL DEFAULT '',
    sha256               TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'PLANNED'
                         CHECK (status IN ('PLANNED','EXTRACTING','EXTRACTED','COMPOSING','COMPOSED','PUBLISHING','PUBLISHED','VERIFIED','RETRY_WAIT','FAILED_PERMANENT','QUARANTINED')),
    drive_file_id        TEXT NOT NULL DEFAULT '',
    drive_folder_id      TEXT NOT NULL DEFAULT '',
    drive_link           TEXT NOT NULL DEFAULT '',
    attempts             INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_stock_artifacts_batch_id ON stock_artifacts(batch_id);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_group_id ON stock_artifacts(group_id);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_status ON stock_artifacts(status);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_ordinal ON stock_artifacts(group_id, ordinal);
`

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory SQLite")
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(migration162Schema)
	require.NoError(t, err, "apply migration 162 schema")
	return db
}

func newTestRepo(t *testing.T) *stockbatches.Repository {
	t.Helper()
	return stockbatches.NewRepository(newTestDB(t))
}

func TestNewRepository_NilDB_Panics(t *testing.T) {
	assert.Panics(t, func() {
		_ = stockbatches.NewRepository(nil)
	}, "nil db must panic (fail-fast)")
}

func TestRepository_BatchRoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	batch := &stockpipeline.StockBatch{
		ID:             "batch-1",
		Fingerprint:    "fp-1",
		SourceURL:      "https://example.com/source",
		SourceCacheKey: "cache-key-1",
		RootFolderID:   "root-folder-1",
		RootFolderName: "Root",
		Status:         stockpipeline.BatchStatePlanned,
		ExpectedGroups: 13,
		ExpectedClips:  195,
		VerifiedClips:  0,
		PolicyVersion:  "v1",
		LastError:      "",
	}

	require.NoError(t, repo.CreateBatch(ctx, batch))

	got, err := repo.GetBatch(ctx, batch.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, batch.Fingerprint, got.Fingerprint)
	assert.Equal(t, batch.SourceURL, got.SourceURL)
	assert.Equal(t, batch.ExpectedGroups, got.ExpectedGroups)
	assert.Equal(t, batch.ExpectedClips, got.ExpectedClips)
	assert.Equal(t, batch.Status, got.Status)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())

	require.NoError(t, repo.UpdateBatchStatus(ctx, batch.ID, stockpipeline.BatchStateRunning, ""))
	got, err = repo.GetBatch(ctx, batch.ID)
	require.NoError(t, err)
	assert.Equal(t, stockpipeline.BatchStateRunning, got.Status)
}

func TestRepository_GroupRoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	batch := &stockpipeline.StockBatch{
		ID:     "batch-1",
		Status: stockpipeline.BatchStatePlanned,
	}
	require.NoError(t, repo.CreateBatch(ctx, batch))

	group := &stockpipeline.StockBatchGroup{
		ID:            "group-1",
		BatchID:       batch.ID,
		GroupKey:      "round-1",
		Title:         "Round 1",
		FolderName:    "Round_1",
		DriveFolderID: "drive-folder-1",
		StartSec:      14,
		EndSec:        139,
		ExpectedClips: 15,
		VerifiedClips: 0,
		Status:        stockpipeline.GroupStatePlanned,
		ChildJobID:    "job-1",
		Attempts:      0,
	}
	require.NoError(t, repo.CreateGroup(ctx, group))

	got, err := repo.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, group.GroupKey, got.GroupKey)
	assert.Equal(t, group.Title, got.Title)
	assert.Equal(t, group.StartSec, got.StartSec)
	assert.Equal(t, group.Status, got.Status)

	require.NoError(t, repo.UpdateGroupStatus(ctx, group.ID, stockpipeline.GroupStateRunning, ""))
	got, err = repo.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	assert.Equal(t, stockpipeline.GroupStateRunning, got.Status)
}

func TestRepository_ArtifactRoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	batch := &stockpipeline.StockBatch{ID: "batch-1", Status: stockpipeline.BatchStatePlanned}
	require.NoError(t, repo.CreateBatch(ctx, batch))
	group := &stockpipeline.StockBatchGroup{ID: "group-1", BatchID: batch.ID, Status: stockpipeline.GroupStatePlanned}
	require.NoError(t, repo.CreateGroup(ctx, group))

	artifact := &stockpipeline.StockArtifact{
		ID:                 "artifact-1",
		BatchID:            batch.ID,
		GroupID:            group.ID,
		Ordinal:            1,
		ArtifactKey:        "clip_001_000014000_000018000",
		SourceURL:          "https://example.com/source",
		StartSec:           14,
		EndSec:             18,
		ExpectedDurationMs: 4000,
		ActualDurationMs:   4001,
		LocalPath:          "/tmp/clip_001.mp4",
		SHA256:             "deadbeef",
		Status:             stockpipeline.ArtifactStatePlanned,
	}
	require.NoError(t, repo.CreateArtifact(ctx, artifact))

	got, err := repo.GetArtifact(ctx, artifact.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, artifact.ArtifactKey, got.ArtifactKey)
	assert.Equal(t, artifact.SHA256, got.SHA256)
	assert.Equal(t, artifact.Status, got.Status)

	require.NoError(t, repo.MarkArtifactExtracting(ctx, artifact.ID))
	got, err = repo.GetArtifact(ctx, artifact.ID)
	require.NoError(t, err)
	assert.Equal(t, stockpipeline.ArtifactStateExtracting, got.Status)
	assert.Equal(t, 1, got.Attempts, "EXTRACTING transition must increment attempts")

	require.NoError(t, repo.MarkArtifactExtracted(ctx, artifact.ID, "/data/clip.mp4", "deadbeef", 4000))
	got, err = repo.GetArtifact(ctx, artifact.ID)
	require.NoError(t, err)
	assert.Equal(t, stockpipeline.ArtifactStateExtracted, got.Status)
	assert.Equal(t, "/data/clip.mp4", got.LocalPath)
	assert.Equal(t, "deadbeef", got.SHA256)
	assert.Equal(t, 4000, got.ActualDurationMs)
	assert.Equal(t, 1, got.Attempts, "EXTRACTED transition must NOT increment attempts")
}

func TestRepository_FindIncompleteArtifacts(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	batch := &stockpipeline.StockBatch{ID: "batch-1", Status: stockpipeline.BatchStatePlanned}
	require.NoError(t, repo.CreateBatch(ctx, batch))
	group := &stockpipeline.StockBatchGroup{ID: "group-1", BatchID: batch.ID, Status: stockpipeline.GroupStatePlanned}
	require.NoError(t, repo.CreateGroup(ctx, group))

	for i, status := range []stockpipeline.ArtifactState{
		stockpipeline.ArtifactStatePlanned,
		stockpipeline.ArtifactStateExtracting,
		stockpipeline.ArtifactStateExtracted,
		stockpipeline.ArtifactStateVerified,
		stockpipeline.ArtifactStateFailedPermanent,
	} {
		artifact := &stockpipeline.StockArtifact{
			ID:      "artifact-" + string(rune('a'+i)),
			BatchID: batch.ID,
			GroupID: group.ID,
			Ordinal: i,
			Status:  status,
		}
		require.NoError(t, repo.CreateArtifact(ctx, artifact))
	}

	incomplete, err := repo.FindIncompleteArtifacts(ctx, group.ID, 10)
	require.NoError(t, err)
	require.Len(t, incomplete, 3)
	assert.Equal(t, "artifact-a", incomplete[0].ID)
	assert.Equal(t, "artifact-b", incomplete[1].ID)
	assert.Equal(t, "artifact-c", incomplete[2].ID)

	// Artifacts whose attempts are exhausted should not be returned.
	incomplete, err = repo.FindIncompleteArtifacts(ctx, group.ID, 0)
	require.NoError(t, err)
	require.Empty(t, incomplete)
}

// TestRepository_ArtifactCreateIdempotent verifies that re-creating an
// already-progressed artifact is a no-op: existing state (status, attempts,
// local_path, sha256) must be preserved across retries.
func TestRepository_ArtifactCreateIdempotent(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	batch := &stockpipeline.StockBatch{ID: "batch-1", Status: stockpipeline.BatchStatePlanned}
	require.NoError(t, repo.CreateBatch(ctx, batch))
	group := &stockpipeline.StockBatchGroup{ID: "group-1", BatchID: batch.ID, Status: stockpipeline.GroupStatePlanned}
	require.NoError(t, repo.CreateGroup(ctx, group))

	artifact := &stockpipeline.StockArtifact{
		ID:      "artifact-1",
		BatchID: batch.ID,
		GroupID: group.ID,
		Ordinal: 0,
		Status:  stockpipeline.ArtifactStatePlanned,
	}
	require.NoError(t, repo.CreateArtifact(ctx, artifact))
	require.NoError(t, repo.MarkArtifactExtracting(ctx, artifact.ID))
	require.NoError(t, repo.MarkArtifactExtracted(ctx, artifact.ID, "/data/clip.mp4", "cafebabe", 4000))

	// Re-creating the same artifact with a different status/path must not
	// overwrite the persisted extraction result.
	artifact.Status = stockpipeline.ArtifactStatePlanned
	artifact.LocalPath = "/other/clip.mp4"
	artifact.SHA256 = "deadbeef"
	require.NoError(t, repo.CreateArtifact(ctx, artifact))

	got, err := repo.GetArtifact(ctx, artifact.ID)
	require.NoError(t, err)
	assert.Equal(t, stockpipeline.ArtifactStateExtracted, got.Status)
	assert.Equal(t, "/data/clip.mp4", got.LocalPath)
	assert.Equal(t, "cafebabe", got.SHA256)
	assert.Equal(t, 4000, got.ActualDurationMs)
	assert.Equal(t, 1, got.Attempts)
}

func TestRepository_TimestampsMoveForward(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	batch := &stockpipeline.StockBatch{ID: "batch-1", Status: stockpipeline.BatchStatePlanned}
	require.NoError(t, repo.CreateBatch(ctx, batch))

	before := time.Now().UTC().Add(-time.Second)
	require.NoError(t, repo.UpdateBatchStatus(ctx, batch.ID, stockpipeline.BatchStateRunning, "starting"))
	after := time.Now().UTC().Add(time.Second)

	got, err := repo.GetBatch(ctx, batch.ID)
	require.NoError(t, err)
	assert.True(t, got.UpdatedAt.After(before) || got.UpdatedAt.Equal(before))
	assert.True(t, got.UpdatedAt.Before(after) || got.UpdatedAt.Equal(after))
}
