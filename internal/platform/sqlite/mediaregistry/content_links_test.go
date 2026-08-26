package mediaregistry

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"database/sql"
	"errors"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	_ "github.com/mattn/go-sqlite3"
)

// newContentLinkDB builds an in-memory DB with the minimal schema for the
// asset-content linkage: media_assets (with the content_sha256 column added
// by migration 197) and asset_sources.
func newContentLinkDB(t *testing.T) (*sql.DB, *Ledger) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
	ledger, err := NewLedger(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, ledger
}

func TestLinkContentRoundTrip(t *testing.T) {
	_, ledger := newContentLinkDB(t)
	ctx := context.Background()
	if _, err := ledger.db.ExecContext(ctx,
		`INSERT INTO media_assets (id, content_sha256) VALUES ('asset-1', '')`); err != nil {
		t.Fatal(err)
	}

	if err := ledger.LinkContent(ctx, "asset-1", "a1f8c72e"); err != nil {
		t.Fatalf("link content: %v", err)
	}
	got, err := ledger.ContentForAsset(ctx, "asset-1")
	if err != nil || got != "a1f8c72e" {
		t.Fatalf("read back link: got=%q err=%v", got, err)
	}

	// Re-linking is an idempotent overwrite.
	if err := ledger.LinkContent(ctx, "asset-1", "b2f8d999"); err != nil {
		t.Fatalf("re-link content: %v", err)
	}
	got, err = ledger.ContentForAsset(ctx, "asset-1")
	if err != nil || got != "b2f8d999" {
		t.Fatalf("read back re-link: got=%q err=%v", got, err)
	}
}

func TestLinkContentValidation(t *testing.T) {
	_, ledger := newContentLinkDB(t)
	ctx := context.Background()
	if err := ledger.LinkContent(ctx, "", "abc"); !errors.Is(err, capregistry.ErrAssetSourceInvalid) {
		t.Fatalf("empty asset_id: want ErrAssetSourceInvalid, got %v", err)
	}
	if err := ledger.LinkContent(ctx, "asset-1", ""); !errors.Is(err, capregistry.ErrAssetSourceInvalid) {
		t.Fatalf("empty content_sha256: want ErrAssetSourceInvalid, got %v", err)
	}
}

func TestLinkContentMissingAssetFailsClosed(t *testing.T) {
	_, ledger := newContentLinkDB(t)
	ctx := context.Background()
	// godlike/07 fail-closed: we never link content to a phantom asset.
	if err := ledger.LinkContent(ctx, "does-not-exist", "abc"); err == nil {
		t.Fatal("linking a missing asset must fail closed, got nil")
	}
	if got, err := ledger.ContentForAsset(ctx, "does-not-exist"); err != nil || got != "" {
		t.Fatalf("missing asset must read back empty: got=%q err=%v", got, err)
	}
}

func TestRegisterSourceIdempotent(t *testing.T) {
	db, ledger := newContentLinkDB(t)
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
	if err := ledger.RegisterSource(ctx, src); err != nil {
		t.Fatalf("register source: %v", err)
	}

	// Re-registering the same source_id must not duplicate the row; it
	// refreshes the content link (the source_identity_registry contract).
	src.ContentSHA256 = "b2f8d999"
	src.IsPrimary = false
	if err := ledger.RegisterSource(ctx, src); err != nil {
		t.Fatalf("re-register source: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_asset_sources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("re-register must not duplicate: got %d rows, want 1", count)
	}

	sources, err := ledger.SourcesForAsset(ctx, "asset-1")
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources after upsert: len=%d err=%v", len(sources), err)
	}
	if sources[0].ContentSHA256 != "b2f8d999" || sources[0].IsPrimary {
		t.Fatalf("upsert must refresh content link + primary: %+v", sources[0])
	}
}

func TestSourcesForAssetOrderingPrimaryFirst(t *testing.T) {
	_, ledger := newContentLinkDB(t)
	ctx := context.Background()

	secondary := capregistry.AssetSource{
		SourceID:     "src-yt-001",
		AssetID:      "asset-1",
		SourceType:   "youtube",
		SourceURI:    "https://youtu.be/abc",
		DiscoveredAt: "2026-08-12T00:00:00Z",
		IsPrimary:    false,
	}
	primary := capregistry.AssetSource{
		SourceID:     "src-drive-827",
		AssetID:      "asset-1",
		SourceType:   "drive",
		SourceURI:    "drive://file/827",
		DiscoveredAt: "2026-08-11T00:00:00Z",
		IsPrimary:    true,
	}
	if err := ledger.RegisterSource(ctx, secondary); err != nil {
		t.Fatal(err)
	}
	if err := ledger.RegisterSource(ctx, primary); err != nil {
		t.Fatal(err)
	}

	sources, err := ledger.SourcesForAsset(ctx, "asset-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("want 2 sources, got %d", len(sources))
	}
	if !sources[0].IsPrimary || sources[0].SourceID != "src-drive-827" {
		t.Fatalf("primary source must come first: %+v", sources)
	}
	// Discovery order within the same primary bucket (both secondary here).
	if sources[1].SourceID != "src-yt-001" {
		t.Fatalf("secondary source order: %+v", sources)
	}
}

func TestSourcesForAssetMissingAsset(t *testing.T) {
	_, ledger := newContentLinkDB(t)
	ctx := context.Background()
	sources, err := ledger.SourcesForAsset(ctx, "nope")
	if err != nil || len(sources) != 0 {
		t.Fatalf("no sources for unknown asset: len=%d err=%v", len(sources), err)
	}
}

func TestRegisterSourceValidation(t *testing.T) {
	_, ledger := newContentLinkDB(t)
	ctx := context.Background()
	bad := capregistry.AssetSource{SourceID: "src-1", AssetID: "asset-1"}
	if err := ledger.RegisterSource(ctx, bad); !errors.Is(err, capregistry.ErrAssetSourceInvalid) {
		t.Fatalf("missing fields: want ErrAssetSourceInvalid, got %v", err)
	}
}
