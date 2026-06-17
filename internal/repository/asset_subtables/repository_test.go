package asset_subtables

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"velox/go-master/internal/media/models"
)

// testSchema mirrors migrations/sqlite/036_canonical_asset_subtables.sql
// plus a minimal media_assets shadow so FKs are valid. The schema is
// maintained in lockstep with the migration — drift is a bug.
const testSchema = `
	-- minimal media_assets shadow (FK target)
	CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT ''
	);

	-- 1. asset_locations
	CREATE TABLE asset_locations (
		id            TEXT PRIMARY KEY,
		asset_id      TEXT NOT NULL,
		location_kind TEXT NOT NULL
			CHECK (location_kind IN ('local','drive','s3','r2','gcs','minio','http')),
		uri           TEXT NOT NULL,
		path          TEXT NOT NULL DEFAULT '',
		external_id   TEXT NOT NULL DEFAULT '',
		is_primary    INTEGER NOT NULL DEFAULT 0,
		status        TEXT NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending','available','missing','corrupted')),
		checksum      TEXT NOT NULL DEFAULT '',
		size_bytes    INTEGER NOT NULL DEFAULT 0,
		mime_type     TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
	);
	CREATE INDEX idx_asset_locations_asset_id ON asset_locations(asset_id);
	CREATE INDEX idx_asset_locations_uri ON asset_locations(location_kind, uri);
	CREATE UNIQUE INDEX idx_asset_locations_one_primary
		ON asset_locations(asset_id) WHERE is_primary = 1;

	-- 2. asset_processing
	CREATE TABLE asset_processing (
		id              TEXT PRIMARY KEY,
		asset_id        TEXT NOT NULL,
		step            TEXT NOT NULL
			CHECK (step IN ('download','normalize','transcribe','translate',
				'embed_text','embed_visual','embed_audio','index_qdrant','thumbnail','dedup')),
		status          TEXT NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending','running','completed','failed','skipped')),
		attempt_count   INTEGER NOT NULL DEFAULT 0,
		max_attempts    INTEGER NOT NULL DEFAULT 3,
		last_error      TEXT NOT NULL DEFAULT '',
		last_attempt_at TEXT,
		last_success_at TEXT,
		worker_id       TEXT,
		metadata_json   TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
		UNIQUE (asset_id, step)
	);
	CREATE INDEX idx_asset_processing_sweeper ON asset_processing(status, updated_at);

	-- 3. asset_relations
	CREATE TABLE asset_relations (
		id              TEXT PRIMARY KEY,
		parent_asset_id TEXT NOT NULL,
		child_asset_id  TEXT NOT NULL,
		relation_kind   TEXT NOT NULL
			CHECK (relation_kind IN ('derived_from','part_of','used_by',
				'version_of','duplicate_of','transcript_of')),
		metadata_json   TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (parent_asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
		FOREIGN KEY (child_asset_id)  REFERENCES media_assets(id) ON DELETE CASCADE,
		UNIQUE (parent_asset_id, child_asset_id, relation_kind),
		CHECK (parent_asset_id != child_asset_id)
	);
	CREATE INDEX idx_asset_relations_child ON asset_relations(child_asset_id);

	-- 4. asset_versions
	CREATE TABLE asset_versions (
		id            TEXT PRIMARY KEY,
		asset_id      TEXT NOT NULL,
		version       INTEGER NOT NULL CHECK (version > 0),
		snapshot_json TEXT NOT NULL,
		change_kind   TEXT NOT NULL
			CHECK (change_kind IN ('created','updated','replaced','deleted','restored')),
		changed_by    TEXT,
		change_reason TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE (asset_id, version)
	);
`

// newTestDB opens an in-memory SQLite, applies testSchema, and seeds one
// media_assets row so FK tests pass. Returns the active *sql.DB — caller
// must close it (typically via t.Cleanup).
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=MEMORY&_busy_timeout=5000&_txlock=immediate")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(testSchema)
	require.NoError(t, err)

	return db
}

// seedAsset creates a media_assets row so the subtables' FKs are valid.
func seedAsset(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets (id, name) VALUES (?, ?)`, id, "test-asset-"+id)
	require.NoError(t, err)
}

func newTestRepo(db *sql.DB) *Repository {
	return NewRepository(db, zap.NewNop())
}

// ────────────────────────────────────────────────────────────────────────────
// LOCATION TESTS
// ────────────────────────────────────────────────────────────────────────────

func TestRepository_Location_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_loc_1")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	loc := &models.AssetLocation{
		ID:           "loc_1",
		AssetID:      "asset_loc_1",
		LocationKind: models.LocationKindDrive,
		URI:          "https://drive.google.com/file/d/abc123",
		Path:         "artlist/asset_loc_1.mp4",
		ExternalID:   "abc123",
		IsPrimary:    true,
		Status:       "available",
		Checksum:     "sha256:deadbeef",
		SizeBytes:    1024 * 1024 * 5,
		MimeType:     "video/mp4",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	require.NoError(t, repo.UpsertLocation(ctx, loc))

	got, err := repo.GetLocation(ctx, "loc_1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, loc.ID, got.ID)
	assert.Equal(t, loc.AssetID, got.AssetID)
	assert.Equal(t, loc.LocationKind, got.LocationKind)
	assert.Equal(t, loc.URI, got.URI)
	assert.Equal(t, loc.Path, got.Path)
	assert.Equal(t, loc.ExternalID, got.ExternalID)
	assert.Equal(t, loc.IsPrimary, got.IsPrimary)
	assert.Equal(t, loc.Status, got.Status)
	assert.Equal(t, loc.Checksum, got.Checksum)
	assert.Equal(t, loc.SizeBytes, got.SizeBytes)
	assert.Equal(t, loc.MimeType, got.MimeType)
}

func TestRepository_Location_ListByAsset_Ordering(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_loc_2")
	repo := newTestRepo(db)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Second)
	// Three locations: secondary, then secondary, then primary (created later).
	for i, primary := range []bool{false, false, true} {
		require.NoError(t, repo.UpsertLocation(ctx, &models.AssetLocation{
			ID:           "loc_" + string(rune('a'+i)),
			AssetID:      "asset_loc_2",
			LocationKind: models.LocationKindLocal,
			URI:          "/data/clip-" + string(rune('a'+i)) + ".mp4",
			IsPrimary:    primary,
			Status:       "available",
			CreatedAt:    base.Add(time.Duration(i) * time.Second),
			UpdatedAt:    base.Add(time.Duration(i) * time.Second),
		}))
	}

	got, err := repo.ListLocationsByAsset(ctx, "asset_loc_2")
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Primary comes first (is_primary DESC) regardless of created_at order.
	assert.True(t, got[0].IsPrimary, "expected primary first")
}

func TestRepository_Location_FindByURI(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_loc_3")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.UpsertLocation(ctx, &models.AssetLocation{
		ID:           "loc_uri",
		AssetID:      "asset_loc_3",
		LocationKind: models.LocationKindS3,
		URI:          "s3://my-bucket/clip.mp4",
		Status:       "available",
		CreatedAt:    now,
		UpdatedAt:    now,
	}))

	got, err := repo.FindLocationByURI(ctx, models.LocationKindS3, "s3://my-bucket/clip.mp4")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "loc_uri", got.ID)
}

func TestRepository_Location_RejectInvalidKind(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_loc_4")
	repo := newTestRepo(db)
	ctx := context.Background()

	err := repo.UpsertLocation(ctx, &models.AssetLocation{
		ID:           "loc_bad",
		AssetID:      "asset_loc_4",
		LocationKind: models.LocationKind("smb"), // unknown
		URI:          "smb://share/clip.mp4",
		Status:       "available",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	})
	assert.Error(t, err, "invalid location_kind must be rejected before SQL")
}

// ────────────────────────────────────────────────────────────────────────────
// PROCESSING TESTS
// ────────────────────────────────────────────────────────────────────────────

func TestRepository_Processing_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_proc_1")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	step := &models.AssetProcessingStep{
		ID:            "proc_1",
		AssetID:       "asset_proc_1",
		Step:          models.ProcessingStepTranscribe,
		Status:        models.ProcessingStatusCompleted,
		AttemptCount:  1,
		MaxAttempts:   3,
		LastError:     "",
		LastAttemptAt: &now,
		LastSuccessAt: &now,
		WorkerID:      "worker-1",
		MetadataJSON:  `{"model":"whisper-large-v3","segments":42}`,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	require.NoError(t, repo.UpsertProcessingStep(ctx, step))

	got, err := repo.GetProcessingStep(ctx, "asset_proc_1", models.ProcessingStepTranscribe)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, step.Step, got.Step)
	assert.Equal(t, step.Status, got.Status)
	assert.Equal(t, step.AttemptCount, got.AttemptCount)
	assert.Equal(t, step.MaxAttempts, got.MaxAttempts)
	assert.Equal(t, step.LastError, got.LastError)
	require.NotNil(t, got.LastAttemptAt)
	require.NotNil(t, got.LastSuccessAt)
	assert.True(t, got.LastAttemptAt.Equal(*step.LastAttemptAt))
	assert.True(t, got.LastSuccessAt.Equal(*step.LastSuccessAt))
	assert.Equal(t, step.WorkerID, got.WorkerID)
	assert.Equal(t, step.MetadataJSON, got.MetadataJSON)
}

func TestRepository_Processing_RerunIsUpsert(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_proc_2")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)

	// First attempt: failed
	require.NoError(t, repo.UpsertProcessingStep(ctx, &models.AssetProcessingStep{
		ID: "proc_re", AssetID: "asset_proc_2",
		Step: models.ProcessingStepDownload, Status: models.ProcessingStatusFailed,
		AttemptCount: 1, MaxAttempts: 3,
		LastError: "network timeout", CreatedAt: now, UpdatedAt: now,
	}))

	// Retry: should overwrite, NOT insert a duplicate row.
	got, err := repo.ListProcessingByAsset(ctx, "asset_proc_2")
	require.NoError(t, err)
	require.Len(t, got, 1, "UNIQUE(asset_id, step) forbids duplicate rows")

	require.NoError(t, repo.UpsertProcessingStep(ctx, &models.AssetProcessingStep{
		ID: "proc_re", AssetID: "asset_proc_2",
		Step: models.ProcessingStepDownload, Status: models.ProcessingStatusCompleted,
		AttemptCount: 2, MaxAttempts: 3,
		CreatedAt: now, UpdatedAt: now,
	}))

	got2, err := repo.ListProcessingByAsset(ctx, "asset_proc_2")
	require.NoError(t, err)
	require.Len(t, got2, 1, "still one row after upsert")
	assert.Equal(t, models.ProcessingStatusCompleted, got2[0].Status)
	assert.Equal(t, 2, got2[0].AttemptCount)
}

func TestRepository_Processing_ListPending(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_proc_3")
	seedAsset(t, db, "asset_proc_4")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.UpsertProcessingStep(ctx, &models.AssetProcessingStep{
		ID: "p_a", AssetID: "asset_proc_3",
		Step: models.ProcessingStepDownload, Status: models.ProcessingStatusPending,
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.UpsertProcessingStep(ctx, &models.AssetProcessingStep{
		ID: "p_b", AssetID: "asset_proc_4",
		Step: models.ProcessingStepNormalize, Status: models.ProcessingStatusPending,
		CreatedAt: now, UpdatedAt: now,
	}))

	got, err := repo.ListPendingProcessing(ctx, models.ProcessingStatusPending, 10)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestRepository_Processing_RejectSelfRelationThroughInvalidStep(t *testing.T) {
	repo := newTestRepo(newTestDB(t))
	err := repo.UpsertProcessingStep(context.Background(), &models.AssetProcessingStep{
		ID: "p_invalid", AssetID: "any",
		Step:    models.ProcessingStep("nuke"),
		Status:  models.ProcessingStatusPending,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	assert.Error(t, err, "validates enum before SQL")
}

// ────────────────────────────────────────────────────────────────────────────
// RELATION TESTS (including the self-loop invariant)
// ────────────────────────────────────────────────────────────────────────────

func TestRepository_Relation_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "project_a")
	seedAsset(t, db, "clip_b")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	rel := &models.AssetRelation{
		ID:            "rel_1",
		ParentAssetID: "project_a",
		ChildAssetID:  "clip_b",
		RelationKind:  models.RelationKindPartOf,
		MetadataJSON:  `{"order":1}`,
		CreatedAt:     now,
	}

	require.NoError(t, repo.UpsertRelation(ctx, rel))

	children, err := repo.ListChildren(ctx, "project_a", "")
	require.NoError(t, err)
	require.Len(t, children, 1)
	assert.Equal(t, "rel_1", children[0].ID)
	assert.Equal(t, models.RelationKindPartOf, children[0].RelationKind)

	parents, err := repo.ListParents(ctx, "clip_b", "")
	require.NoError(t, err)
	require.Len(t, parents, 1)
	assert.Equal(t, "rel_1", parents[0].ID)
}

func TestRepository_Relation_RejectsSelfLoop(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_self")
	repo := newTestRepo(db)
	ctx := context.Background()

	err := repo.UpsertRelation(ctx, &models.AssetRelation{
		ID:            "rel_self",
		ParentAssetID: "asset_self",
		ChildAssetID:  "asset_self",
		RelationKind:  models.RelationKindPartOf,
		CreatedAt:     time.Now(),
	})
	assert.Error(t, err, "self-relation forbidden by app + DB CHECK")
}

func TestRepository_Relation_RejectsUnknownKind(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_rel_1")
	seedAsset(t, db, "asset_rel_2")
	repo := newTestRepo(db)

	err := repo.UpsertRelation(context.Background(), &models.AssetRelation{
		ID: "rel_bad_kind", ParentAssetID: "asset_rel_1", ChildAssetID: "asset_rel_2",
		RelationKind: models.RelationKind("has_a_dance_with"),
		CreatedAt:    time.Now(),
	})
	assert.Error(t, err)
}

func TestRepository_Relation_DeleteEdge(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_rel_3")
	seedAsset(t, db, "asset_rel_4")
	repo := newTestRepo(db)
	ctx := context.Background()

	require.NoError(t, repo.UpsertRelation(ctx, &models.AssetRelation{
		ID: "rel_del", ParentAssetID: "asset_rel_3", ChildAssetID: "asset_rel_4",
		RelationKind: models.RelationKindUsedBy, CreatedAt: time.Now(),
	}))
	require.NoError(t, repo.DeleteRelation(ctx, "asset_rel_3", "asset_rel_4", models.RelationKindUsedBy))

	children, err := repo.ListChildren(ctx, "asset_rel_3", "")
	require.NoError(t, err)
	assert.Empty(t, children)
}

// ────────────────────────────────────────────────────────────────────────────
// VERSION TESTS (immutable audit log)
// ────────────────────────────────────────────────────────────────────────────

func TestRepository_Version_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_ver_1")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	v := &models.AssetVersion{
		ID:           "ver_1_1",
		AssetID:      "asset_ver_1",
		Version:      1,
		SnapshotJSON: `{"name":"foo","tags":["a","b"]}`,
		ChangeKind:   models.VersionChangeKindCreated,
		ChangedBy:    "user:42",
		ChangeReason: "initial ingest",
		CreatedAt:    now,
	}

	require.NoError(t, repo.AppendVersion(ctx, v))

	got, err := repo.GetLatestVersion(ctx, "asset_ver_1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, v.Version, got.Version)
	assert.Equal(t, v.SnapshotJSON, got.SnapshotJSON)
	assert.Equal(t, v.ChangeKind, got.ChangeKind)
	assert.Equal(t, v.ChangedBy, got.ChangedBy)
	assert.Equal(t, v.ChangeReason, got.ChangeReason)
}

func TestRepository_Version_SequenceEnforced(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_ver_2")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	// Append two versions in order
	require.NoError(t, repo.AppendVersion(ctx, &models.AssetVersion{
		ID: "v2_a", AssetID: "asset_ver_2", Version: 1,
		SnapshotJSON: `{"v":1}`, ChangeKind: models.VersionChangeKindCreated,
		CreatedAt: now,
	}))
	require.NoError(t, repo.AppendVersion(ctx, &models.AssetVersion{
		ID: "v2_b", AssetID: "asset_ver_2", Version: 2,
		SnapshotJSON: `{"v":2}`, ChangeKind: models.VersionChangeKindUpdated,
		CreatedAt: now,
	}))

	// Replay v=1 must reject (audit rows are immutable)
	err := repo.AppendVersion(ctx, &models.AssetVersion{
		ID: "v2_c", AssetID: "asset_ver_2", Version: 1,
		SnapshotJSON: `{"v":1,"tampered":true}`,
		ChangeKind:   models.VersionChangeKindUpdated,
		CreatedAt:    now,
	})
	assert.Error(t, err, "UNIQUE(asset_id, version) forbids duplicates")

	versions, err := repo.ListVersions(ctx, "asset_ver_2")
	require.NoError(t, err)
	require.Len(t, versions, 2)
	// newest first
	assert.Equal(t, 2, versions[0].Version)
	assert.Equal(t, 1, versions[1].Version)
}

func TestRepository_Version_RejectsZeroVersion(t *testing.T) {
	repo := newTestRepo(newTestDB(t))
	err := repo.AppendVersion(context.Background(), &models.AssetVersion{
		ID:           "v_zero",
		AssetID:      "x",
		Version:      0,
		SnapshotJSON: `{}`,
		ChangeKind:   models.VersionChangeKindCreated,
		CreatedAt:    time.Now(),
	})
	assert.Error(t, err)
}

func TestRepository_NewRepository_NilDB(t *testing.T) {
	assert.Nil(t, NewRepository(nil, zap.NewNop()))
}

// TestRepository_Location_RejectsSecondPrimary enforces the partial UNIQUE
// index `idx_asset_locations_one_primary ON asset_locations(asset_id) WHERE
// is_primary = 1` and the typed-error wrapping that converts opaque SQL
// failures into ErrPrimaryLocationExists. A second primary for the same
// asset (different id) must be rejected; an upsert updating the same id
// to primary must still succeed.
func TestRepository_Location_RejectsSecondPrimary(t *testing.T) {
	db := newTestDB(t)
	seedAsset(t, db, "asset_dup_primary")
	repo := newTestRepo(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)

	// First primary — accepted.
	require.NoError(t, repo.UpsertLocation(ctx, &models.AssetLocation{
		ID: "loc_p1", AssetID: "asset_dup_primary",
		LocationKind: models.LocationKindLocal, URI: "/data/a.mp4",
		IsPrimary: true, Status: "available",
		CreatedAt: now, UpdatedAt: now,
	}))

	// Second primary for the same asset — must be rejected with typed error.
	err := repo.UpsertLocation(ctx, &models.AssetLocation{
		ID: "loc_p2", AssetID: "asset_dup_primary",
		LocationKind: models.LocationKindLocal, URI: "/data/b.mp4",
		IsPrimary: true, Status: "available",
		CreatedAt: now, UpdatedAt: now,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPrimaryLocationExists,
		"second primary must surface ErrPrimaryLocationExists, not raw SQL UNIQUE violation")

	// Upserting the SAME id back to primary=true is allowed (idempotent).
	require.NoError(t, repo.UpsertLocation(ctx, &models.AssetLocation{
		ID: "loc_p1", AssetID: "asset_dup_primary",
		LocationKind: models.LocationKindLocal, URI: "/data/a.mp4",
		IsPrimary: true, Status: "available",
		CreatedAt: now, UpdatedAt: now,
	}))
}
