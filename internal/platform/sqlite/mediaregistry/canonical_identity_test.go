package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

const canonicalIdentitySchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    source_video_id TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    content_sha256 TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS media_asset_sources (
    source_id      TEXT PRIMARY KEY,
    asset_id       TEXT NOT NULL,
    content_sha256 TEXT NOT NULL DEFAULT '',
    source_type    TEXT NOT NULL,
    source_uri     TEXT NOT NULL,
    source_version TEXT NOT NULL DEFAULT '',
    discovered_at  TEXT NOT NULL,
    is_primary     INTEGER NOT NULL DEFAULT 0
);
`

func newCanonicalIdentityResolver(t *testing.T) (*CanonicalIdentityResolver, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(canonicalIdentitySchema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	resolver, err := NewCanonicalIdentityResolver(db)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	return resolver, db
}

func seedSource(t *testing.T, db *sql.DB, assetID, sourceType, sourceRef, contentSHA string) {
	t.Helper()
	sourceID := capregistry.DeriveAssetSourceID(assetID, sourceType, sourceRef, "")
	if _, err := db.Exec(`INSERT INTO media_asset_sources (source_id, asset_id, content_sha256, source_type, source_uri, source_version, discovered_at, is_primary)
		VALUES (?, ?, ?, ?, ?, '', '2026-08-14T00:00:00Z', 1)`,
		sourceID, assetID, contentSHA, sourceType, sourceRef); err != nil {
		t.Fatalf("seed source: %v", err)
	}
}

func TestResolveSource_Found(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	seedSource(t, db, "asset-a", "youtube", "dQw4w9", "sha256:bytes")

	got, err := r.ResolveSource(context.Background(), "youtube", "dQw4w9")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got.AssetID != "asset-a" || got.SourceType != "youtube" || got.SourceRef != "dQw4w9" || got.ContentSHA256 != "sha256:bytes" {
		t.Fatalf("unexpected identity: %+v", got)
	}
}

func TestResolveSource_NotFound(t *testing.T) {
	r, _ := newCanonicalIdentityResolver(t)
	_, err := r.ResolveSource(context.Background(), "youtube", "missing")
	if !errors.Is(err, capregistry.ErrCanonicalIdentityNotFound) {
		t.Fatalf("err = %v, want ErrCanonicalIdentityNotFound", err)
	}
}

func TestResolveSource_Ambiguous(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	seedSource(t, db, "asset-a", "youtube", "dQw4w9", "")
	seedSource(t, db, "asset-b", "youtube", "dQw4w9", "")

	_, err := r.ResolveSource(context.Background(), "youtube", "dQw4w9")
	if !errors.Is(err, capregistry.ErrCanonicalIdentityAmbiguous) {
		t.Fatalf("err = %v, want ErrCanonicalIdentityAmbiguous", err)
	}
}

func TestCanonicalSourceID_DoesNotDependOnAssetID(t *testing.T) {
	a := capregistry.DeriveCanonicalSourceID("youtube", "dQw4w9", "")
	b := capregistry.DeriveCanonicalSourceID("youtube", "dQw4w9", "")
	if a == "" || a != b {
		t.Fatalf("canonical source id must be stable for the source tuple: %q vs %q", a, b)
	}
	if a == capregistry.DeriveAssetSourceID("asset-a", "youtube", "dQw4w9", "") {
		t.Fatal("canonical source id must not silently retain the legacy asset-dependent format")
	}
}

func TestResolveContent_SingleAsset(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	if _, err := db.Exec(`INSERT INTO media_assets (id, source, media_type, content_sha256) VALUES ('asset-a', 'youtube', 'video', 'sha256:bytes')`); err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	got, err := r.ResolveContent(context.Background(), "sha256:bytes")
	if err != nil {
		t.Fatalf("ResolveContent: %v", err)
	}
	if got.AssetID != "asset-a" || got.ContentSHA256 != "sha256:bytes" {
		t.Fatalf("unexpected identity: %+v", got)
	}
}

func TestResolveContent_MultiProvenance(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	for _, id := range []string{"asset-a", "asset-b", "asset-c"} {
		if _, err := db.Exec(`INSERT INTO media_assets (id, source, media_type, content_sha256) VALUES (?, 'youtube', 'video', 'sha256:bytes')`, id); err != nil {
			t.Fatalf("seed asset: %v", err)
		}
	}
	got, err := r.ResolveContent(context.Background(), "sha256:bytes")
	if err != nil {
		t.Fatalf("ResolveContent: %v", err)
	}
	if got.AssetID != "" {
		t.Fatalf("multi-provenance content must leave AssetID empty, got %q", got.AssetID)
	}
}

func TestResolveContent_NotFound(t *testing.T) {
	r, _ := newCanonicalIdentityResolver(t)
	_, err := r.ResolveContent(context.Background(), "sha256:missing")
	if !errors.Is(err, capregistry.ErrCanonicalIdentityNotFound) {
		t.Fatalf("err = %v, want ErrCanonicalIdentityNotFound", err)
	}
}

func TestBackfill_ReconstructsSourcesFailClosed(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	// youtube asset with a source_video_id → backfilled.
	// drive asset with a drive_file_id → backfilled.
	// artlist asset with no derivable ref → UNKNOWN.
	// youtube asset without source_video_id → UNKNOWN.
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, source, media_type, lifecycle_state, source_video_id, drive_file_id, content_sha256) VALUES
			('yt_vid_0_60_v1', 'youtube', 'video', 'ACTIVE', 'vid1', '', 'sha256:1'),
			('drive_asset', 'drive', 'video', 'ACTIVE', '', 'file1', 'sha256:2'),
			('artlist_x', 'artlist', 'video', 'ACTIVE', '', '', ''),
			('yt_no_vid', 'youtube', 'video', 'ACTIVE', '', '', 'sha256:3')`); err != nil {
		t.Fatalf("seed assets: %v", err)
	}

	report, err := r.Backfill(context.Background())
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if report.AssetsTotal != 4 {
		t.Fatalf("AssetsTotal = %d, want 4", report.AssetsTotal)
	}
	if report.SourcesBackfilled != 2 {
		t.Fatalf("SourcesBackfilled = %d, want 2", report.SourcesBackfilled)
	}
	if report.SourcesUnknown != 2 {
		t.Fatalf("SourcesUnknown = %d, want 2", report.SourcesUnknown)
	}
	if report.ContentSHAKnown != 3 || report.ContentSHAUnknown != 1 {
		t.Fatalf("content sha known/unknown = %d/%d, want 3/1", report.ContentSHAKnown, report.ContentSHAUnknown)
	}

	// Verify the two sources were actually written.
	for _, pair := range [][2]string{{"youtube", "vid1"}, {"drive", "file1"}} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM media_asset_sources WHERE source_type = ? AND source_uri = ?`, pair[0], pair[1]).Scan(&count); err != nil {
			t.Fatalf("count source: %v", err)
		}
		if count != 1 {
			t.Fatalf("source (%s, %s) rows = %d, want 1", pair[0], pair[1], count)
		}
	}
}

func TestBackfill_Idempotent(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	if _, err := db.Exec(`INSERT INTO media_assets (id, source, media_type, lifecycle_state, source_video_id) VALUES ('yt_vid_0_60_v1', 'youtube', 'video', 'ACTIVE', 'vid1')`); err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	if _, err := r.Backfill(context.Background()); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	report, err := r.Backfill(context.Background())
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if report.SourcesBackfilled != 0 || report.SourcesAlreadyKnown != 1 {
		t.Fatalf("second backfill must be idempotent: backfilled=%d already=%d", report.SourcesBackfilled, report.SourcesAlreadyKnown)
	}
}

// TestLegacyTaxonomy_ResolvesViaCanonicalResolver pins that the admin
// taxonomy backfill routes every legacy (source, media_type) row through the
// SINGLE canonical resolver (capregistry.ResolveTaxonomy), preserving the
// exact historical namespace / asset_kind / source_type values and the
// retired-media_type replacement.
func TestLegacyTaxonomy_ResolvesViaCanonicalResolver(t *testing.T) {
	cases := []struct {
		name        string
		source      string
		mediaType   string
		wantNS      string
		wantKind    capregistry.AssetKind
		wantSource  string
		wantMedia   capregistry.MediaType
		wantReplace string
		wantOK      bool
	}{
		{"voiceover audio", "voiceover", "audio", "audio", capregistry.AssetVoiceover, capregistry.SourceIdentityDrive, capregistry.MediaAudio, "", true},
		{"created audio", "created", "audio", "audio", capregistry.AssetVoiceover, "pipelinegen", capregistry.MediaAudio, "", true},
		{"created text", "created", "text", "text", capregistry.AssetMetadata, "pipelinegen", capregistry.MediaText, "", true},
		{"script text", "script", "text", "text", capregistry.AssetMetadata, "pipelinegen", capregistry.MediaText, "", true},
		{"stock metadata", "stock", "metadata", "metadata", capregistry.AssetMetadata, "stock", capregistry.MediaText, "text", true},
		{"stock video", "stock", "video", "stock", capregistry.AssetStockVideo, "stock", capregistry.MediaVideo, "", true},
		{"youtube clip", "youtube", "clip", "clips", capregistry.AssetClip, capregistry.SourceIdentityYouTube, capregistry.MediaVideo, "video", true},
		{"youtube video", "youtube", "video", "clips", capregistry.AssetClip, capregistry.SourceIdentityYouTube, capregistry.MediaVideo, "", true},
		{"clip_drive clip", "clip_drive", "clip", "clips", capregistry.AssetClip, capregistry.SourceIdentityDrive, capregistry.MediaVideo, "video", true},
		{"local video", "local", "video", "clips", capregistry.AssetClip, capregistry.SourceIdentityManual, capregistry.MediaVideo, "", true},
		{"created document", "created", "document", "outputs", capregistry.AssetDocument, "pipelinegen", capregistry.MediaDocument, "", true},
		{"document document", "document", "document", "outputs", capregistry.AssetDocument, "pipelinegen", capregistry.MediaDocument, "", true},
		{"unknown source", "mystery", "video", "", "", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tax, replacement, ok := legacyTaxonomy("asset-1", tc.source, tc.mediaType, "file.mp4")
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if tax.Namespace != tc.wantNS || tax.AssetKind != tc.wantKind || tax.SourceType != tc.wantSource || tax.MediaType != tc.wantMedia {
				t.Fatalf("legacyTaxonomy(%q, %q) = %+v, want ns=%q kind=%q source=%q media=%q",
					tc.source, tc.mediaType, tax, tc.wantNS, tc.wantKind, tc.wantSource, tc.wantMedia)
			}
			if replacement != tc.wantReplace {
				t.Fatalf("replacement = %q, want %q", replacement, tc.wantReplace)
			}
		})
	}
}

func TestBackfill_SourceCollisionFailsClosed(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, source, media_type, lifecycle_state, source_video_id) VALUES
			('asset-a', 'youtube', 'video', 'ACTIVE', 'same-video'),
			('asset-b', 'youtube', 'video', 'ACTIVE', 'same-video')`); err != nil {
		t.Fatalf("seed assets: %v", err)
	}

	report, err := r.Backfill(context.Background())
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if report.SourcesBackfilled != 1 || report.SourcesAmbiguous != 1 || report.SourcesUnknown != 1 {
		t.Fatalf("collision report = %+v, want one backfill and one fail-closed ambiguity", report)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_asset_sources WHERE source_type='youtube' AND source_uri='same-video'`).Scan(&count); err != nil {
		t.Fatalf("count source: %v", err)
	}
	if count != 1 {
		t.Fatalf("collision must not create multiple canonical source rows, got %d", count)
	}
}
