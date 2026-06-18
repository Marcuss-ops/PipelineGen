package assetlocations

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// testDB creates an in-memory SQLite database with the asset_locations
// table, a minimal media_assets FK parent, and an outbox_events table
// (used by the transactional outbox emission assertions below).
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable FKs: %v", err)
	}
	schema := `
		CREATE TABLE media_assets (id TEXT PRIMARY KEY, source TEXT, name TEXT);
		CREATE TABLE asset_locations (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id        TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_kind   TEXT NOT NULL CHECK (location_kind IN ('local', 'drive', 'object_storage')),
			uri             TEXT NOT NULL,
			external_id     TEXT NOT NULL DEFAULT '',
			access_url      TEXT NOT NULL DEFAULT '',
			download_url    TEXT NOT NULL DEFAULT '',
			mime_type       TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			file_hash       TEXT NOT NULL DEFAULT '',
			is_primary      INTEGER NOT NULL DEFAULT 0,
			created_at      TEXT NOT NULL DEFAULT '',
			updated_at      TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, location_kind)
		);
		CREATE INDEX IF NOT EXISTS idx_asset_locations_asset ON asset_locations (asset_id);
		CREATE INDEX IF NOT EXISTS idx_asset_locations_primary ON asset_locations (asset_id) WHERE is_primary = 1;

		CREATE TABLE outbox_events (
			id TEXT PRIMARY KEY,
			aggregate_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT '',
			published_at TEXT
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// seedAsset inserts a minimal row into media_assets so FK constraints pass.
func seedAsset(t *testing.T, db *sql.DB, id, source string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO media_assets(id, source, name) VALUES (?,?,?)`, id, source, id+"_name"); err != nil {
		t.Fatalf("seed asset %s: %v", id, err)
	}
}

// outboxCount returns the number of outbox_events rows for (aggregateID, event).
func outboxCount(t *testing.T, db *sql.DB, aggregateID, event string) int {
	t.Helper()
	var n int
	q := `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`
	args := []any{aggregateID}
	if event != "" {
		q += ` AND event_type = ?`
		args = append(args, event)
	}
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("outbox count: %v", err)
	}
	return n
}

// ── Contract tests ──────────────────────────────────────────────────────

func TestUpsertAndListByAssetRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-1", "youtube")

	r := New(db, nil)

	if err := r.Upsert(ctx, &asset.Location{
		AssetID:       "asset-1",
		LocationKind:  asset.LocationKindLocal,
		URI:           "/data/clips/asset-1.mp4",
		MimeType:      "video/mp4",
		FileSizeBytes: 1024000,
		FileHash:      "abc123",
		IsPrimary:     true,
	}); err != nil {
		t.Fatalf("Upsert local: %v", err)
	}

	if err := r.Upsert(ctx, &asset.Location{
		AssetID:      "asset-1",
		LocationKind: asset.LocationKindDrive,
		URI:          "drive_file_xyz",
		ExternalID:   "xyz",
		AccessURL:    "https://drive.google.com/file/d/xyz",
		DownloadURL:  "https://drive.google.com/uc?id=xyz",
	}); err != nil {
		t.Fatalf("Upsert drive: %v", err)
	}

	locs, err := r.ListByAsset(ctx, "asset-1")
	if err != nil {
		t.Fatalf("ListByAsset: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locs))
	}

	var local, drive *asset.Location
	for i, l := range locs {
		if l.LocationKind == asset.LocationKindLocal {
			local = locs[i]
		}
		if l.LocationKind == asset.LocationKindDrive {
			drive = locs[i]
		}
	}
	if local == nil || !local.IsPrimary || local.URI != "/data/clips/asset-1.mp4" || local.FileHash != "abc123" || local.FileSizeBytes != 1024000 {
		t.Errorf("local mismatch: %+v", local)
	}
	if drive == nil || drive.IsPrimary || drive.ExternalID != "xyz" || drive.AccessURL == "" || drive.DownloadURL == "" {
		t.Errorf("drive mismatch (external_id/access_url/download_url from 061 not preserved): %+v", drive)
	}
}

func TestGetPrimary(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-2", "artlist")
	r := New(db, nil)

	if loc, err := r.GetPrimary(ctx, "asset-2"); err != nil || loc != nil {
		t.Fatalf("GetPrimary empty: loc=%+v err=%v", loc, err)
	}

	if err := r.Upsert(ctx, &asset.Location{
		AssetID: "asset-2", LocationKind: asset.LocationKindLocal, URI: "/tmp/a2.mp4", IsPrimary: true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	loc, err := r.GetPrimary(ctx, "asset-2")
	if err != nil {
		t.Fatalf("GetPrimary: %v", err)
	}
	if loc == nil || loc.LocationKind != asset.LocationKindLocal {
		t.Fatalf("expected local primary, got %+v", loc)
	}
}

func TestSetPrimarySwapsDesignation(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-3", "stock")
	r := New(db, nil)

	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-3", LocationKind: asset.LocationKindLocal, URI: "/tmp/a3.mp4", IsPrimary: true})
	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-3", LocationKind: asset.LocationKindDrive, URI: "drive_aaa"})

	if err := r.SetPrimary(ctx, "asset-3", asset.LocationKindDrive); err != nil {
		t.Fatalf("SetPrimary drive: %v", err)
	}

	loc, err := r.GetPrimary(ctx, "asset-3")
	if err != nil {
		t.Fatalf("GetPrimary after swap: %v", err)
	}
	if loc == nil || loc.LocationKind != asset.LocationKindDrive {
		t.Fatalf("expected drive primary after swap, got %+v", loc)
	}

	locs, _ := r.ListByAsset(ctx, "asset-3")
	for _, l := range locs {
		if l.LocationKind == asset.LocationKindLocal && l.IsPrimary {
			t.Errorf("local should no longer be primary after swap")
		}
	}
}

func TestUpsertIdempotent(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-4", "youtube")
	r := New(db, nil)

	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-4", LocationKind: asset.LocationKindLocal, URI: "/tmp/v1.mp4", FileHash: "hash1"})
	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-4", LocationKind: asset.LocationKindLocal, URI: "/tmp/v2.mp4", FileHash: "hash2"})

	locs, err := r.ListByAsset(ctx, "asset-4")
	if err != nil {
		t.Fatalf("ListByAsset: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location after idempotent upsert, got %d", len(locs))
	}
	if locs[0].URI != "/tmp/v2.mp4" || locs[0].FileHash != "hash2" {
		t.Errorf("upsert did not update: %+v", locs[0])
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-5", "youtube")
	r := New(db, nil)

	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-5", LocationKind: asset.LocationKindLocal, URI: "/tmp/del.mp4"})
	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-5", LocationKind: asset.LocationKindDrive, URI: "drive_del"})

	if err := r.Delete(ctx, "asset-5", asset.LocationKindLocal); err != nil {
		t.Fatalf("Delete local: %v", err)
	}

	locs, _ := r.ListByAsset(ctx, "asset-5")
	if len(locs) != 1 {
		t.Fatalf("expected 1 location after delete, got %d", len(locs))
	}
	if locs[0].LocationKind != asset.LocationKindDrive {
		t.Errorf("expected drive to remain, got %s", locs[0].LocationKind)
	}
}

func TestDeleteAll(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-6", "youtube")
	r := New(db, nil)

	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-6", LocationKind: asset.LocationKindLocal, URI: "/tmp/a.mp4"})
	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-6", LocationKind: asset.LocationKindDrive, URI: "drive_a"})

	if err := r.DeleteAll(ctx, "asset-6"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	locs, _ := r.ListByAsset(ctx, "asset-6")
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations after DeleteAll, got %d", len(locs))
	}
}

func TestForeignKeyCascade(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-fk", "youtube")
	r := New(db, nil)

	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-fk", LocationKind: asset.LocationKindLocal, URI: "/tmp/fk.mp4"})

	if _, err := db.Exec(`DELETE FROM media_assets WHERE id = ?`, "asset-fk"); err != nil {
		t.Fatalf("delete parent asset: %v", err)
	}

	locs, _ := r.ListByAsset(ctx, "asset-fk")
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations after FK cascade, got %d", len(locs))
	}
}

// ── Outbox transaction tests ───────────────────────────────────────────

func TestOutboxEmittedOnUpsert(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-ob", "youtube")
	r := New(db, nil)

	if err := r.Upsert(ctx, &asset.Location{AssetID: "asset-ob", LocationKind: asset.LocationKindLocal, URI: "/tmp/ob.mp4"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if n := outboxCount(t, db, "asset-ob", "location.upserted"); n != 1 {
		t.Errorf("expected 1 location.upserted outbox row, got %d", n)
	}
}

func TestOutboxEmittedOnSetPrimary(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-sp", "youtube")
	r := New(db, nil)

	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-sp", LocationKind: asset.LocationKindLocal, URI: "/tmp/sp.mp4", IsPrimary: true})
	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-sp", LocationKind: asset.LocationKindDrive, URI: "drive_sp"})

	if err := r.SetPrimary(ctx, "asset-sp", asset.LocationKindDrive); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}

	if n := outboxCount(t, db, "asset-sp", "location.primary_set"); n != 1 {
		t.Errorf("expected 1 location.primary_set outbox row, got %d", n)
	}
}

func TestOutboxEmittedOnDelete(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-d", "youtube")
	r := New(db, nil)

	_ = r.Upsert(ctx, &asset.Location{AssetID: "asset-d", LocationKind: asset.LocationKindLocal, URI: "/tmp/d.mp4"})

	if err := r.Delete(ctx, "asset-d", asset.LocationKindLocal); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := outboxCount(t, db, "asset-d", "location.deleted"); n != 1 {
		t.Errorf("expected 1 location.deleted outbox row, got %d", n)
	}
}

func TestOutboxNotEmittedOnRollback(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-rb", "youtube")
	r := New(db, nil)

	err := r.WithTx(ctx, func(tx *Tx) error {
		if err := tx.OnCommit(ctx, "asset-rb", "location.upserted", map[string]string{"sim": "true"}); err != nil {
			return err
		}
		return errors.New("simulated failure")
	})
	if err == nil {
		t.Fatalf("expected error from WithTx")
	}

	if n := outboxCount(t, db, "asset-rb", ""); n != 0 {
		t.Errorf("expected 0 outbox rows after rollback, got %d", n)
	}
}

// ── Input validation ────────────────────────────────────────────────────

func TestInvalidArgsReturnErrInvalidID(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	r := New(db, nil)

	if err := r.Upsert(ctx, &asset.Location{}); err != asset.ErrInvalidID {
		t.Errorf("Upsert(empty): want ErrInvalidID, got %v", err)
	}
	if _, err := r.GetPrimary(ctx, ""); err != asset.ErrInvalidID {
		t.Errorf("GetPrimary(empty): want ErrInvalidID, got %v", err)
	}
	if _, err := r.ListByAsset(ctx, ""); err != asset.ErrInvalidID {
		t.Errorf("ListByAsset(empty): want ErrInvalidID, got %v", err)
	}
	if err := r.SetPrimary(ctx, "", asset.LocationKindLocal); err != asset.ErrInvalidID {
		t.Errorf("SetPrimary(empty assetID): want ErrInvalidID, got %v", err)
	}
	if err := r.SetPrimary(ctx, "asset-x", ""); err != asset.ErrInvalidID {
		t.Errorf("SetPrimary(empty kind): want ErrInvalidID, got %v", err)
	}
	if err := r.Delete(ctx, "", asset.LocationKindLocal); err != asset.ErrInvalidID {
		t.Errorf("Delete(empty assetID): want ErrInvalidID, got %v", err)
	}
	if err := r.Delete(ctx, "asset-x", ""); err != asset.ErrInvalidID {
		t.Errorf("Delete(empty kind): want ErrInvalidID, got %v", err)
	}
	if err := r.DeleteAll(ctx, ""); err != asset.ErrInvalidID {
		t.Errorf("DeleteAll(empty): want ErrInvalidID, got %v", err)
	}
}

func TestNewRepositoryBackwardCompatAlias(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	seedAsset(t, db, "asset-nra", "youtube")

	// NewRepository is the legacy entry point — must still produce a
	// working Repository that satisfies asset.LocationRepository.
	r := NewRepository(db)
	var _ asset.LocationRepository = r

	if err := r.Upsert(ctx, &asset.Location{AssetID: "asset-nra", LocationKind: asset.LocationKindLocal, URI: "/tmp/nra.mp4"}); err != nil {
		t.Fatalf("Upsert via NewRepository: %v", err)
	}
	locs, _ := r.ListByAsset(ctx, "asset-nra")
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
}
