package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	_ "github.com/mattn/go-sqlite3"
)

// newSourceWriteDB builds an in-memory DB with the minimal schema for the
// asset-source write helper: media_assets and media_asset_sources.
func newSourceWriteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			content_sha256 TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			media_type TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE media_asset_sources (
			source_id      TEXT PRIMARY KEY,
			asset_id       TEXT NOT NULL,
			content_sha256 TEXT NOT NULL DEFAULT '',
			source_type    TEXT NOT NULL,
			source_uri     TEXT NOT NULL,
			source_version TEXT NOT NULL DEFAULT '',
			discovered_at  TEXT NOT NULL,
			is_primary     INTEGER NOT NULL DEFAULT 0
		);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRegisterSourceIdempotent(t *testing.T) {
	db := newSourceWriteDB(t)
	ctx := context.Background()

	src := capregistry.AssetSource{
		SourceID:      "src-drive-827",
		AssetID:       "asset-1",
		ContentSHA256: "a1f8c72e",
		SourceType:    "drive",
		SourceURI:     "drive://file/827",
		SourceVersion: "etag-v1",
		DiscoveredAt:  "2026-08-12T00:00:00Z",
		IsPrimary:     true,
	}
	if err := registerSource(ctx, db, src); err != nil {
		t.Fatalf("register source: %v", err)
	}

	// Re-registering the same source_id must not duplicate the row; it
	// refreshes the content link and the primary flag.
	src.ContentSHA256 = "b2f8d999"
	src.IsPrimary = false
	if err := registerSource(ctx, db, src); err != nil {
		t.Fatalf("re-register source: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_asset_sources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("re-register must not duplicate: got %d rows, want 1", count)
	}

	var (
		sha256  string
		primary int
	)
	if err := db.QueryRowContext(ctx,
		`SELECT content_sha256, is_primary FROM media_asset_sources WHERE source_id = ?`,
		src.SourceID).Scan(&sha256, &primary); err != nil {
		t.Fatal(err)
	}
	if sha256 != "b2f8d999" || primary != 0 {
		t.Fatalf("upsert must refresh content link + primary: sha=%q primary=%d", sha256, primary)
	}
}

func TestRegisterSourceValidation(t *testing.T) {
	db := newSourceWriteDB(t)
	ctx := context.Background()
	bad := capregistry.AssetSource{SourceID: "src-1", AssetID: "asset-1"}
	if err := registerSource(ctx, db, bad); !errors.Is(err, capregistry.ErrAssetSourceInvalid) {
		t.Fatalf("missing fields: want ErrAssetSourceInvalid, got %v", err)
	}
}
