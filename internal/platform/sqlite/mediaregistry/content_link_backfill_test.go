package mediaregistry

import (
	"context"
	"database/sql"
	"testing"
)

const contentLinkBackfillSchema = `
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,
    media_type TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    content_sha256 TEXT NOT NULL DEFAULT ''
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
);
CREATE TABLE content_objects (
    sha256           TEXT PRIMARY KEY,
    size_bytes       INTEGER NOT NULL,
    mime_type        TEXT,
    storage_uri      TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    verified_at      TEXT,
    integrity_status TEXT NOT NULL
);
`

func newContentLinkBackfillResolver(t *testing.T) (*CanonicalIdentityResolver, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(contentLinkBackfillSchema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	resolver, err := NewCanonicalIdentityResolver(db)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	return resolver, db
}

func TestBackfillContentLinks_ApplyCreatesObjectsAndLinksSources(t *testing.T) {
	r, db := newContentLinkBackfillResolver(t)
	ctx := context.Background()
	// asset-a: known digest, no source row.
	// asset-b: known digest + source row with empty content_sha256.
	// asset-c: unknown digest (no content).
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, media_type, lifecycle_state, content_sha256) VALUES
			('asset-a', 'video', 'ACTIVE', 'sha256:a1'),
			('asset-b', 'video', 'ACTIVE', 'sha256:b1'),
			('asset-c', 'video', 'ACTIVE', '');
		INSERT INTO media_asset_sources (source_id, asset_id, content_sha256, source_type, source_uri, source_version, discovered_at, is_primary) VALUES
			('src-b', 'asset-b', '', 'drive', 'drive://b', '', '2026-08-14T00:00:00Z', 1),
			('src-solo', 'asset-x', 'sha256:solo', 'url', 'https://x', '', '2026-08-14T00:00:00Z', 1);`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	report, err := r.BackfillContentLinks(ctx, true)
	if err != nil {
		t.Fatalf("BackfillContentLinks: %v", err)
	}
	if report.AssetsScanned != 3 {
		t.Fatalf("AssetsScanned = %d, want 3", report.AssetsScanned)
	}
	if report.ContentSHAKnown != 2 || report.ContentSHAUnknown != 1 {
		t.Fatalf("content sha known/unknown = %d/%d, want 2/1", report.ContentSHAKnown, report.ContentSHAUnknown)
	}
	// Three distinct digests: sha256:a1, sha256:b1 (asset) + sha256:solo (source-only).
	if report.ContentObjectsCreated != 3 {
		t.Fatalf("ContentObjectsCreated = %d, want 3", report.ContentObjectsCreated)
	}
	if report.SourceLinksBackfilled != 1 {
		t.Fatalf("SourceLinksBackfilled = %d, want 1", report.SourceLinksBackfilled)
	}
	if report.BrokenCASLinks != 0 {
		t.Fatalf("BrokenCASLinks after apply = %d, want 0", report.BrokenCASLinks)
	}

	// The source-only digest also got a content_object.
	var objCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM content_objects WHERE sha256 = 'sha256:solo'`).Scan(&objCount); err != nil {
		t.Fatalf("count solo object: %v", err)
	}
	if objCount != 1 {
		t.Fatalf("source-only digest must get a content object, got %d rows", objCount)
	}
	// The source row for asset-b now carries the asset digest.
	var srcSHA string
	if err := db.QueryRow(`SELECT content_sha256 FROM media_asset_sources WHERE source_id = 'src-b'`).Scan(&srcSHA); err != nil {
		t.Fatalf("read src-b: %v", err)
	}
	if srcSHA != "sha256:b1" {
		t.Fatalf("src-b content_sha256 = %q, want sha256:b1", srcSHA)
	}
}

func TestBackfillContentLinks_PreviewDoesNotMutate(t *testing.T) {
	r, db := newContentLinkBackfillResolver(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO media_assets (id, content_sha256) VALUES ('asset-a', 'sha256:a1')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	report, err := r.BackfillContentLinks(ctx, false)
	if err != nil {
		t.Fatalf("BackfillContentLinks(preview): %v", err)
	}
	if report.ContentObjectsCreated != 1 {
		t.Fatalf("preview ContentObjectsCreated = %d, want 1", report.ContentObjectsCreated)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM content_objects`).Scan(&count); err != nil {
		t.Fatalf("count objects: %v", err)
	}
	if count != 0 {
		t.Fatalf("preview must not create content_objects, got %d rows", count)
	}
}

func TestBackfillContentLinks_Idempotent(t *testing.T) {
	r, db := newContentLinkBackfillResolver(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO media_assets (id, content_sha256) VALUES ('asset-a', 'sha256:a1')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := r.BackfillContentLinks(ctx, true); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	report, err := r.BackfillContentLinks(ctx, true)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if report.ContentObjectsCreated != 0 {
		t.Fatalf("second backfill must be idempotent: created=%d, want 0", report.ContentObjectsCreated)
	}
	if report.BrokenCASLinks != 0 {
		t.Fatalf("BrokenCASLinks = %d, want 0", report.BrokenCASLinks)
	}
}

func TestBackfillContentLinks_ExistingObjectPreserved(t *testing.T) {
	r, db := newContentLinkBackfillResolver(t)
	ctx := context.Background()
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, content_sha256) VALUES ('asset-a', 'sha256:a1');
		INSERT INTO content_objects (sha256, size_bytes, storage_uri, created_at, integrity_status)
			VALUES ('sha256:a1', 999, 'cas://existing', '2026-08-14T00:00:00Z', 'UNVERIFIED');`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	report, err := r.BackfillContentLinks(ctx, true)
	if err != nil {
		t.Fatalf("BackfillContentLinks: %v", err)
	}
	if report.ContentObjectsExisting != 1 || report.ContentObjectsCreated != 0 {
		t.Fatalf("existing/created = %d/%d, want 1/0", report.ContentObjectsExisting, report.ContentObjectsCreated)
	}
	// The pre-existing storage_uri/size must be preserved (ON CONFLICT DO NOTHING).
	var uri string
	var size int
	if err := db.QueryRow(`SELECT storage_uri, size_bytes FROM content_objects WHERE sha256 = 'sha256:a1'`).Scan(&uri, &size); err != nil {
		t.Fatalf("read object: %v", err)
	}
	if uri != "cas://existing" || size != 999 {
		t.Fatalf("existing object was overwritten: uri=%q size=%d", uri, size)
	}
}
