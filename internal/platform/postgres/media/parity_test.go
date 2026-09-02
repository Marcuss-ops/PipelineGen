// Package media — parity_test.go: behavioral parity between
// PostgresMediaCommitter and the SQLite canonical writer
// (internal/platform/sqlite/assets/imagesregistry).
//
// Each test mirrors the canonical SQLite test suite
// (media_committer_test.go) and asserts identical observable outcomes:
// same Created identity resolution, same taxonomy projection, same
// provenance/registry/text-track/outbox write counts, same rollback
// semantics, same provider-scoped idempotency keys, same terminal-status
// surfacing, same fail-closed mutations.
package media_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

func newPostgresCommitter(t *testing.T) (*pgmedia.PostgresMediaCommitter, *sql.DB) {
	t.Helper()
	db := newMediaTestDB(t)

	box := pgmedia.NewOutboxRepository(db)
	ledger, err := pgmedia.NewRegistry(db)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return pgmedia.NewPostgresMediaCommitter(db, box, ledger, nil), db
}

// fullCommitRequest mirrors the canonical SQLite fixture (media_committer_test.go).
func fullCommitRequest() mediacommit.CommitMediaAssetRequest {
	return mediacommit.CommitMediaAssetRequest{
		Asset: mediacommit.AssetDraft{
			AssetID:        "yt_abc123_10_60_v1",
			Source:         "youtube",
			Name:           "Funny Moment",
			Filename:       "clip.mp4",
			MediaType:      "video",
			ContentHash:    "sha256:content",
			Description:    "A funny moment",
			SearchText:     "funny moment",
			LifecycleState: "ACTIVE",
			IndexState:     "DISCOVERED",
		},
		Source: mediacommit.AssetSourceDraft{
			SourceType:    "youtube",
			SourceURI:     "https://www.youtube.com/watch?v=abc123",
			SourceVersion: "sha256:content",
			IsPrimary:     true,
		},
		Taxonomy: capregistry.AssetTaxonomy{
			Namespace:  "stock",
			MediaType:  capregistry.MediaVideo,
			AssetKind:  capregistry.AssetClip,
			SourceType: "youtube",
		},
		Content: &mediacommit.ContentIdentity{ContentSHA256: "sha256:bytes"},
		TextTracks: []mediacommit.TextTrack{
			{LanguageCode: "en", TextKind: "transcript", TextContent: "hello world"},
		},
		IndexPolicy: mediacommit.IndexPolicy{Indexable: true},
		Actor:       "test",
	}
}

func countWhere(t *testing.T, db *sql.DB, table, col, val string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1`, table, col), val).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// ── 8-step canonical commit ─────────────────────────────────────────────

func TestParity_CommitMediaAsset_HappyPath_AllEightSteps(t *testing.T) {
	c, db := newPostgresCommitter(t)

	res, err := c.CommitMediaAsset(context.Background(), fullCommitRequest())
	if err != nil {
		t.Fatalf("CommitMediaAsset: %v", err)
	}
	if !res.Created {
		t.Fatal("expected Created=true for first commit")
	}
	if res.SourceID == "" || res.AssetID != "yt_abc123_10_60_v1" {
		t.Fatalf("unexpected result identity: %+v", res)
	}
	if res.ContentSHA256 != "sha256:bytes" {
		t.Fatalf("ContentSHA256 = %q, want sha256:bytes", res.ContentSHA256)
	}
	if res.RegistrySeq <= 0 {
		t.Fatalf("RegistrySeq = %d, want > 0", res.RegistrySeq)
	}

	// Asset row + taxonomy dimensions + content link.
	var namespace, assetKind, sourceType, contentSHA string
	if err := db.QueryRow(`SELECT namespace, asset_kind, source_type, content_sha256 FROM media_assets WHERE id = $1`, res.AssetID).
		Scan(&namespace, &assetKind, &sourceType, &contentSHA); err != nil {
		t.Fatalf("scan media_assets: %v", err)
	}
	if namespace != "stock" || assetKind != "clip" || sourceType != "youtube" {
		t.Fatalf("taxonomy mismatch: ns=%q kind=%q source_type=%q", namespace, assetKind, sourceType)
	}
	if contentSHA != "sha256:bytes" {
		t.Fatalf("content_sha256 = %q, want sha256:bytes", contentSHA)
	}

	// Source row.
	if got := countWhere(t, db, "media_asset_sources", "asset_id", res.AssetID); got != 1 {
		t.Fatalf("source rows = %d, want 1", got)
	}
	// Registry event.
	if got := countWhere(t, db, "registry_events", "asset_id", res.AssetID); got != 1 {
		t.Fatalf("registry events = %d, want 1", got)
	}
	// Text track.
	if got := countWhere(t, db, "asset_text_tracks", "asset_id", res.AssetID); got != 1 {
		t.Fatalf("current text tracks = %d, want 1", got)
	}
	// Outbox index request.
	if got := countWhere(t, db, "outbox_events", "aggregate_id", res.AssetID); got != 1 {
		t.Fatalf("outbox events = %d, want 1", got)
	}
}

func TestParity_CommitMediaAsset_SecondCommit_NotCreated_NoDuplicateSource(t *testing.T) {
	c, db := newPostgresCommitter(t)

	if _, err := c.CommitMediaAsset(context.Background(), fullCommitRequest()); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	res, err := c.CommitMediaAsset(context.Background(), fullCommitRequest())
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if res.Created {
		t.Fatal("expected Created=false for second commit")
	}
	if got := countWhere(t, db, "media_asset_sources", "asset_id", res.AssetID); got != 1 {
		t.Fatalf("source rows after re-commit = %d, want 1", got)
	}
	if got := countWhere(t, db, "registry_events", "asset_id", res.AssetID); got != 1 {
		t.Fatalf("registry events after replay = %d, want 1 (deterministic event id)", got)
	}
}

func TestParity_CommitMediaAsset_NotIndexable_SkipsOutbox(t *testing.T) {
	c, db := newPostgresCommitter(t)

	req := fullCommitRequest()
	req.IndexPolicy = mediacommit.IndexPolicy{Indexable: false}
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := countWhere(t, db, "outbox_events", "aggregate_id", req.Asset.AssetID); got != 0 {
		t.Fatalf("outbox events = %d, want 0 for non-indexable asset", got)
	}
}

func TestParity_CommitMediaAsset_UnknownContentAllowedForNonIndexableAsset(t *testing.T) {
	c, db := newPostgresCommitter(t)
	req := fullCommitRequest()
	req.Asset.ContentHash = ""
	req.Content = nil
	req.IndexPolicy = mediacommit.IndexPolicy{Indexable: false}
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatalf("Drive-only commit with unknown content: %v", err)
	}
	var contentSHA string
	if err := db.QueryRow(`SELECT content_sha256 FROM media_assets WHERE id = $1`, req.Asset.AssetID).Scan(&contentSHA); err != nil {
		t.Fatalf("read content hash: %v", err)
	}
	if contentSHA != "" {
		t.Fatalf("content_sha256 = %q, want unknown empty value", contentSHA)
	}
}

func TestParity_CommitMediaAsset_RollsBackAllStepsOnLateFailure(t *testing.T) {
	c, db := newPostgresCommitter(t)
	req := fullCommitRequest()
	req.TextTracks = []mediacommit.TextTrack{{TextKind: "transcript"}}
	if _, err := c.CommitMediaAsset(context.Background(), req); err == nil {
		t.Fatal("expected invalid text track error")
	}
	for _, table := range []string{"media_assets", "media_asset_sources", "registry_events", "outbox_events"} {
		col := map[string]string{"media_assets": "id", "media_asset_sources": "asset_id", "registry_events": "asset_id", "outbox_events": "aggregate_id"}[table]
		if got := countWhere(t, db, table, col, req.Asset.AssetID); got != 0 {
			t.Fatalf("%s rows = %d after rollback, want 0", table, got)
		}
	}
}

func TestParity_CommitMediaAsset_Validation(t *testing.T) {
	c, _ := newPostgresCommitter(t)

	req := fullCommitRequest()
	req.Asset.AssetID = ""
	if _, err := c.CommitMediaAsset(context.Background(), req); err == nil {
		t.Fatal("expected validation error for empty asset id")
	}
}

func TestParity_CommitLegacy(t *testing.T) {
	c, db := newPostgresCommitter(t)

	res, err := c.CommitLegacy(context.Background(), persistence.CommitRequest{
		AssetID:        "legacy_asset_1",
		Source:         "youtube",
		Filename:       "legacy.mp4",
		MediaType:      "video",
		ContentHash:    "sha256:legacy",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
	})
	if err != nil {
		t.Fatalf("CommitLegacy: %v", err)
	}
	if res.AssetID != "legacy_asset_1" {
		t.Fatalf("asset id = %q", res.AssetID)
	}
	if got := countWhere(t, db, "outbox_events", "aggregate_id", res.AssetID); got != 1 {
		t.Fatalf("outbox events = %d, want 1", got)
	}
}

// ── persistence.AssetCommitter port surface ─────────────────────────────

func TestParity_CompatibilityPortPreservesCallerTransaction(t *testing.T) {
	c, db := newPostgresCommitter(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	req := fullCommitRequest()
	commitResult, err := c.CommitTx(context.Background(), tx, persistence.CommitRequest{
		AssetID: req.Asset.AssetID, Source: req.Asset.Source, Filename: req.Asset.Filename,
		MediaType: req.Asset.MediaType, ContentHash: req.Asset.ContentHash,
		LifecycleState: req.Asset.LifecycleState, SearchText: req.Asset.SearchText,
		SourceVideoID: "abc123", SourceURL: req.Asset.SourceURL, EmitIndexEvent: true,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("CommitTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit caller tx: %v", err)
	}
	if commitResult.AssetRowsAffected != 1 || commitResult.OutboxEventKey == "" {
		t.Fatalf("unexpected compatibility result: %+v", commitResult)
	}
	if got := countWhere(t, db, "media_assets", "id", req.Asset.AssetID); got != 1 {
		t.Fatalf("committed asset rows = %d, want 1", got)
	}
}

func TestParity_CommitAndIndex_LocationsUpgradedAndIdempotent(t *testing.T) {
	c, db := newPostgresCommitter(t)

	locations := []persistence.LocationCommit{
		{Kind: "local", Provider: "local", URI: "/data/clips/a.mp4", MimeType: "video", LegacyFileMD5: "sha256:loc"},
		{Kind: "drive", Provider: "drive", ExternalID: "drive-123", URI: "drive://drive-123",
			WebViewLink: "https://drive.example/123", DownloadURL: "https://dl.example/123",
			MimeType: "video", LegacyFileMD5: "sha256:loc", IsPrimary: true},
	}
	req := persistence.CommitRequest{
		AssetID: "loc_asset_1", Source: "youtube", Name: "Located", Filename: "located.mp4",
		MediaType: "video", ContentHash: "sha256:loc", LifecycleState: "ACTIVE",
		SearchText: "located", Locations: locations, EmitIndexEvent: true,
	}
	res, err := c.CommitAndIndex(context.Background(), req)
	if err != nil {
		t.Fatalf("CommitAndIndex: %v", err)
	}
	if res.AssetRowsAffected != 1 || res.OutboxEventKey == "" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Both locations persisted; the drive projection columns on media_assets
	// mirror the primary location (same convention as SQLite).
	var driveCols int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_locations WHERE asset_id = $1`, req.AssetID).Scan(&driveCols); err != nil {
		t.Fatal(err)
	}
	if driveCols != 2 {
		t.Fatalf("asset_locations rows = %d, want 2", driveCols)
	}
	var driveFileID, driveLink string
	if err := db.QueryRow(`SELECT drive_file_id, drive_link FROM media_assets WHERE id = $1`, req.AssetID).Scan(&driveFileID, &driveLink); err != nil {
		t.Fatal(err)
	}
	if driveFileID != "drive-123" || driveLink != "https://drive.example/123" {
		t.Fatalf("primary drive projection mismatch: %q / %q", driveFileID, driveLink)
	}

	// Re-commit with a moved drive file: location upsert refreshes in place.
	req.Locations[1].ExternalID = "drive-456"
	req.Locations[1].URI = "drive://drive-456"
	if _, err := c.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("re-commit: %v", err)
	}
	var uri string
	if err := db.QueryRow(`SELECT uri FROM asset_locations WHERE asset_id = $1 AND location_kind = 'drive'`, req.AssetID).Scan(&uri); err != nil {
		t.Fatal(err)
	}
	if uri != "drive://drive-456" {
		t.Fatalf("drive location not upgraded: %q", uri)
	}
	if got := countWhere(t, db, "asset_locations", "asset_id", req.AssetID); got != 2 {
		t.Fatalf("asset_locations rows after re-commit = %d, want 2 (idempotent upsert)", got)
	}
}

func TestParity_CommitAndIndex_OutboxKeyProviderScopedAndIdempotent(t *testing.T) {
	c, db := newPostgresCommitter(t)

	req := fullCommitRequest()
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	var key1 string
	if err := db.QueryRow(`SELECT event_key FROM outbox_events WHERE aggregate_id = $1`, req.Asset.AssetID).Scan(&key1); err != nil {
		t.Fatal(err)
	}
	// Provider-scoped key: asset id + source version inside the youtube scope.
	if !strings.Contains(key1, "youtube") || !strings.Contains(key1, req.Asset.AssetID) {
		t.Fatalf("event key %q is not provider-scoped", key1)
	}
	// Same source_version replay does not duplicate the event.
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatalf("replay commit: %v", err)
	}
	if got := countWhere(t, db, "outbox_events", "aggregate_id", req.Asset.AssetID); got != 1 {
		t.Fatalf("outbox events after replay = %d, want 1", got)
	}
}

func TestParity_CommitAndIndex_TerminalOutboxConflictSurfaces(t *testing.T) {
	c, db := newPostgresCommitter(t)

	req := fullCommitRequest()
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// Simulate the event having been dispatched and dead-lettered.
	if _, err := db.Exec(`UPDATE outbox_events SET status = 'dead_letter' WHERE aggregate_id = $1`, req.Asset.AssetID); err != nil {
		t.Fatal(err)
	}
	// Same source_version → same event key → arbiter hits the terminal row.
	// The terminal check lives on the RAW committer's standalone path
	// (PostgresAssetCommitter.CommitAndIndex), exactly as in SQLite:
	// SQLiteAssetCommitter.CommitAndIndex performs the post-commit terminal
	// check; the aggregate SQLiteMediaCommitter wraps the same raw path.
	raw := pgmedia.NewPostgresAssetCommitter(db, pgmedia.NewOutboxRepository(db), nil)
	rawReq := persistence.CommitRequest{
		AssetID: req.Asset.AssetID, Source: req.Asset.Source, Filename: req.Asset.Filename,
		MediaType: req.Asset.MediaType, ContentHash: req.Asset.ContentHash,
		LifecycleState: req.Asset.LifecycleState,
		Metadata:       persistence.TypedMetadata{SourceVersion: req.Source.SourceVersion},
		EmitIndexEvent: true,
	}
	_, err := raw.CommitAndIndex(context.Background(), rawReq)
	if err == nil {
		t.Fatal("expected terminal-outbox error to surface")
	}
	if !strings.Contains(err.Error(), "dead_letter") {
		t.Fatalf("error should reference the terminal status: %v", err)
	}
}

// ── Mutations ───────────────────────────────────────────────────────────

func TestParity_Mutations_FailClosedOnUnknownAsset(t *testing.T) {
	c, _ := newPostgresCommitter(t)
	ctx := context.Background()
	if err := c.PatchMetadataJSON(ctx, "missing", "{}", ""); err == nil {
		t.Fatal("metadata patch on missing asset must fail closed")
	}
	if err := c.SetIndexState(ctx, "missing", "INDEXING", ""); err == nil {
		t.Fatal("index state on missing asset must fail closed")
	}
	if err := c.ReplaceMetadataJSON(ctx, "missing", "{}", ""); err == nil {
		t.Fatal("metadata replace on missing asset must fail closed")
	}
	if err := c.LinkContent(ctx, "missing", "sha256:x"); err == nil {
		t.Fatal("content link on missing asset must fail closed")
	}
	// SetIndexed is a CAS: a missing asset is a zero-row miss, not an error
	// (SQLite parity: SetMediaAssetIndexed returns ok=false, err=nil).
	ok, err := c.SetIndexed(ctx, "missing", "sha256:x", "sha256:x", "m", "v", "h")
	if err != nil || ok {
		t.Fatalf("SetIndexed on missing asset must be a CAS miss (false, nil); got ok=%v err=%v", ok, err)
	}
}

func TestParity_MutationSurface_FullRoundTrip(t *testing.T) {
	c, db := newPostgresCommitter(t)
	ctx := context.Background()

	res, err := c.CommitMediaAsset(ctx, fullCommitRequest())
	if err != nil {
		t.Fatal(err)
	}
	id := res.AssetID

	// Metadata patch merge (jsonb || merge).
	if err := c.PatchMetadataJSON(ctx, id, `{"enrich":"v1"}`, ""); err != nil {
		t.Fatalf("patch metadata: %v", err)
	}
	var meta string
	if err := db.QueryRow(`SELECT metadata_json FROM media_assets WHERE id = $1`, id).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	var metaMap map[string]any
	if err := json.Unmarshal([]byte(meta), &metaMap); err != nil {
		t.Fatalf("metadata not JSON: %v (%s)", err, meta)
	}
	if metaMap["enrich"] != "v1" || metaMap["content_hash"] != "sha256:content" {
		t.Fatalf("metadata merge lost canonical keys: %v", metaMap)
	}

	// Replace.
	if err := c.ReplaceMetadataJSON(ctx, id, `{"replaced":true}`, ""); err != nil {
		t.Fatalf("replace metadata: %v", err)
	}
	if err := db.QueryRow(`SELECT metadata_json FROM media_assets WHERE id = $1`, id).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, `"replaced"`) {
		t.Fatalf("metadata replace failed: %s", meta)
	}

	// Lifecycle + search text + folder path.
	if err := c.UpdateLifecycle(ctx, id, "DELETE_REQUESTED", "2026-09-02T00:00:00Z", ""); err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if err := c.UpdateSearchText(ctx, id, "new search", ""); err != nil {
		t.Fatalf("search text: %v", err)
	}
	if err := c.UpdateFolderPath(ctx, id, "folder-9", "/9", ""); err != nil {
		t.Fatalf("folder path: %v", err)
	}
	var lifecycle, searchText, folderID string
	if err := db.QueryRow(`SELECT lifecycle_state, search_text, folder_id FROM media_assets WHERE id = $1`, id).Scan(&lifecycle, &searchText, &folderID); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "DELETE_REQUESTED" || searchText != "new search" || folderID != "folder-9" {
		t.Fatalf("mutation round-trip mismatch: %q %q %q", lifecycle, searchText, folderID)
	}

	// Orphan marker.
	if err := c.UpdateOrphanMetadata(ctx, id, time.Now().UTC(), "local"); err != nil {
		t.Fatalf("orphan metadata: %v", err)
	}

	// PersistEmbeddingJSON typed channels.
	if err := c.PersistEmbeddingJSON(ctx, id, "visual", []float64{0.1, 0.2}, "embedded"); err != nil {
		t.Fatalf("visual embedding: %v", err)
	}
	if err := c.PersistEmbeddingJSON(ctx, id, "bogus", nil, ""); err == nil {
		t.Fatal("unknown embedding channel must be rejected")
	}
	var visual string
	if err := db.QueryRow(`SELECT visual_embedding FROM media_assets WHERE id = $1`, id).Scan(&visual); err != nil {
		t.Fatal(err)
	}
	if visual != "[0.1,0.2]" {
		t.Fatalf("visual embedding = %q, want [0.1,0.2]", visual)
	}

	// INDEXED CAS: requires INDEXING + matching source_version (parity with
	// the SQLite SetMediaAssetIndexed fence).
	var sourceVersion string
	if err := db.QueryRow(`SELECT source_version FROM media_assets WHERE id = $1`, id).Scan(&sourceVersion); err != nil {
		t.Fatal(err)
	}
	if err := c.SetIndexState(ctx, id, "INDEXING", ""); err != nil {
		t.Fatalf("set INDEXING: %v", err)
	}
	ok, err := c.SetIndexed(ctx, id, "sha256:bytes", sourceVersion, "e5", "v1", "hash1")
	if err != nil {
		t.Fatalf("SetIndexed: %v", err)
	}
	if !ok {
		t.Fatal("SetIndexed CAS should succeed with matching source_version + INDEXING")
	}
	// Replay with wrong version fails the CAS (zero rows, ok=false).
	ok, err = c.SetIndexed(ctx, id, "sha256:other", "wrong-version", "e5", "v1", "hash1")
	if err != nil {
		t.Fatalf("SetIndexed stale: %v", err)
	}
	if ok {
		t.Fatal("stale SetIndexed must be a zero-row no-op (ok=false)")
	}
}

func TestParity_UpdateDriveDeliveryByLegacyHash(t *testing.T) {
	c, db := newPostgresCommitter(t)
	ctx := context.Background()

	// The Drive projection keys on source='image' + legacy_file_md5.
	req := fullCommitRequest()
	req.Asset.Source = "image"
	req.Asset.Filename = "image.jpg"
	req.Asset.MediaType = "image"
	req.IndexPolicy = mediacommit.IndexPolicy{Indexable: false}
	if _, err := c.CommitMediaAsset(ctx, req); err != nil {
		t.Fatal(err)
	}

	err := c.UpdateDriveDeliveryByLegacyHash(ctx, "sha256:content", persistence.DriveDeliveryMutation{
		DriveFileID: "drv-1", DriveLink: "https://drive.example/1", DownloadLink: "https://dl.example/1", Status: "delivered",
	})
	if err != nil {
		t.Fatalf("UpdateDriveDeliveryByLegacyHash: %v", err)
	}
	var driveFileID, deliveryStatus string
	if err := db.QueryRow(`SELECT drive_file_id, metadata_json::jsonb->>'delivery_status' FROM media_assets WHERE id = $1`, req.Asset.AssetID).Scan(&driveFileID, &deliveryStatus); err != nil {
		t.Fatal(err)
	}
	if driveFileID != "drv-1" || deliveryStatus != "delivered" {
		t.Fatalf("drive delivery mismatch: %q %q", driveFileID, deliveryStatus)
	}

	// Unknown hash fails closed.
	if err := c.UpdateDriveDeliveryByLegacyHash(ctx, "missing", persistence.DriveDeliveryMutation{Status: "delivered"}); err == nil {
		t.Fatal("unknown legacy hash must fail closed")
	}
}

// ── Renditions + index event seams ──────────────────────────────────────

func TestParity_CommitRenditionTx(t *testing.T) {
	c, db := newPostgresCommitter(t)
	ctx := context.Background()

	res, err := c.CommitMediaAsset(ctx, fullCommitRequest())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = c.CommitRenditionTx(ctx, tx, res.AssetID, persistence.RenditionCommit{
		Kind: "proxy", Provider: "local", URI: "/data/proxy/1.mp4",
		MimeType: "video/mp4", SizeBytes: 1234, SHA256: "sha256:rend",
		Width: 640, Height: 360, FPS: 30, Bitrate: 800, Container: "mp4", Codec: "h264",
	}, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("CommitRenditionTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var kind, container string
	var locationID int64
	if err := db.QueryRow(`SELECT kind, container, location_id FROM asset_renditions WHERE asset_id = $1 AND kind = 'proxy'`, res.AssetID).Scan(&kind, &container, &locationID); err != nil {
		t.Fatalf("rendition row: %v", err)
	}
	if container != "mp4" || locationID == 0 {
		t.Fatalf("rendition mismatch: container=%q location_id=%d", container, locationID)
	}
	var locationKind string
	if err := db.QueryRow(`SELECT location_kind FROM asset_locations WHERE id = $1`, locationID).Scan(&locationKind); err != nil {
		t.Fatalf("rendition location: %v", err)
	}
	if locationKind != "local" {
		t.Fatalf("rendition location kind = %q, want local", locationKind)
	}
}

func TestParity_CommitIndexEventTx(t *testing.T) {
	c, db := newPostgresCommitter(t)
	ctx := context.Background()

	res, err := c.CommitMediaAsset(ctx, fullCommitRequest())
	if err != nil {
		t.Fatal(err)
	}
	// Strip the existing event to prove the seam can re-emit.
	if _, err := db.Exec(`DELETE FROM outbox_events WHERE aggregate_id = $1`, res.AssetID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CommitIndexEventTx(ctx, tx, res.AssetID, "youtube", "sha256:content", "video"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("CommitIndexEventTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := countWhere(t, db, "outbox_events", "aggregate_id", res.AssetID); got != 1 {
		t.Fatalf("outbox events after index event = %d, want 1", got)
	}
}

// ── Cross-engine parity: SQLite vs PostgreSQL ───────────────────────────

// TestParity_CrossEngine_SameObservableCommitResult commits the same
// canonical request through SQLiteMediaCommitter and PostgresMediaCommitter
// and asserts identical observable outcomes: Created identity, outbox event
// key, registry event id, provenance shape, and payload envelope fields.
func TestParity_CrossEngine_SameObservableCommitResult(t *testing.T) {
	// SQLite side (hermetic :memory:).
	sqliteDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()
	if _, err := sqliteDB.Exec(sqliteParitySchema); err != nil {
		t.Fatalf("sqlite schema: %v", err)
	}
	sqliteCommitter := imagesregistry.NewSQLiteMediaCommitter(
		sqliteDB, outboxevents.NewRepository(sqliteDB), mustSQLiteLedger(t, sqliteDB), nil)

	// PostgreSQL side (live fixture).
	pgCommitter, _ := newPostgresCommitter(t)

	req := fullCommitRequest()
	ctx := context.Background()

	pgRes, err := pgCommitter.CommitMediaAsset(ctx, req)
	if err != nil {
		t.Fatalf("pg commit: %v", err)
	}
	sqRes, err := sqliteCommitter.CommitMediaAsset(ctx, req)
	if err != nil {
		t.Fatalf("sqlite commit: %v", err)
	}

	// Same identity resolution + provenance.
	if pgRes.AssetID != sqRes.AssetID || pgRes.Created != sqRes.Created {
		t.Fatalf("identity mismatch: pg=%+v sqlite=%+v", pgRes, sqRes)
	}
	if pgRes.SourceID != sqRes.SourceID {
		t.Fatalf("source id mismatch: %q vs %q", pgRes.SourceID, sqRes.SourceID)
	}
	if pgRes.ContentSHA256 != sqRes.ContentSHA256 {
		t.Fatalf("content sha mismatch: %q vs %q", pgRes.ContentSHA256, sqRes.ContentSHA256)
	}

	// Same outbox event key (provider-scoped idempotency key is
	// engine-independent by construction).
	var pgKey, sqKey string
	if err := pgCommitter.DB().QueryRow(`SELECT event_key FROM outbox_events WHERE aggregate_id = $1`, req.Asset.AssetID).Scan(&pgKey); err != nil {
		t.Fatal(err)
	}
	if err := sqliteDB.QueryRow(`SELECT event_key FROM outbox_events WHERE aggregate_id = ?`, req.Asset.AssetID).Scan(&sqKey); err != nil {
		t.Fatal(err)
	}
	if pgKey != sqKey {
		t.Fatalf("outbox event key mismatch: pg=%q sqlite=%q", pgKey, sqKey)
	}

	// Same registry event id (deterministic SHA-1 UUID over the identity vector).
	var pgEventID, sqEventID string
	if err := pgCommitter.DB().QueryRow(`SELECT event_id FROM registry_events WHERE asset_id = $1`, req.Asset.AssetID).Scan(&pgEventID); err != nil {
		t.Fatal(err)
	}
	if err := sqliteDB.QueryRow(`SELECT event_id FROM registry_events WHERE asset_id = ?`, req.Asset.AssetID).Scan(&sqEventID); err != nil {
		t.Fatal(err)
	}
	if pgEventID != sqEventID {
		t.Fatalf("registry event id mismatch: pg=%q sqlite=%q", pgEventID, sqEventID)
	}

	// Same canonical envelope shape: the reindex envelope fields match.
	var pgPayload, sqPayload string
	if err := pgCommitter.DB().QueryRow(`SELECT payload_json FROM outbox_events WHERE aggregate_id = $1`, req.Asset.AssetID).Scan(&pgPayload); err != nil {
		t.Fatal(err)
	}
	if err := sqliteDB.QueryRow(`SELECT payload_json FROM outbox_events WHERE aggregate_id = ?`, req.Asset.AssetID).Scan(&sqPayload); err != nil {
		t.Fatal(err)
	}
	compareEnvelope(t, pgPayload, sqPayload)
}

// compareEnvelope asserts the two engine payloads agree on every canonical
// envelope field (byte-equality is not required: UUID event ids and
// timestamps are emission-scoped; semantic fields must match exactly).
func compareEnvelope(t *testing.T, pg, sq string) {
	t.Helper()
	var pgMap, sqMap map[string]any
	if err := json.Unmarshal([]byte(pg), &pgMap); err != nil {
		t.Fatalf("pg payload: %v", err)
	}
	if err := json.Unmarshal([]byte(sq), &sqMap); err != nil {
		t.Fatalf("sqlite payload: %v", err)
	}
	for _, field := range []string{
		"schema_version", "operation", "source_version", "index_revision",
		"target_index_version", "asset_id", "source", "media_type",
		"embedding_model", "embedding_version",
	} {
		if pgMap[field] != sqMap[field] {
			t.Fatalf("envelope field %q mismatch: pg=%v sqlite=%v", field, pgMap[field], sqMap[field])
		}
	}
	if _, ok := pgMap["requested_vectors"].([]any); !ok {
		t.Fatalf("pg envelope missing requested_vectors array: %s", pg)
	}
}

// sqliteParitySchema mirrors the minimal schema the canonical SQLite
// committer test suite uses (media_committer_test.go).
const sqliteParitySchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT, name TEXT, filename TEXT, media_type TEXT,
    category TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
    tags TEXT NOT NULL DEFAULT '', tags_norm TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT, drive_link TEXT, download_link TEXT,
    local_path TEXT, legacy_file_md5 TEXT NOT NULL DEFAULT '', binary_sha256 TEXT NOT NULL DEFAULT '',
    folder_id TEXT, folder_path TEXT,
    source_version TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    index_state TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    reuse_count INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    relative_path TEXT NOT NULL DEFAULT '',
    created_at TEXT, updated_at TEXT
);
CREATE TABLE IF NOT EXISTS asset_locations (
    asset_id TEXT NOT NULL,
    location_kind TEXT NOT NULL DEFAULT '',
    uri TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    web_view_link TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    legacy_file_md5 TEXT NOT NULL DEFAULT '',
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (asset_id, location_kind)
);
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT,
    worker_id TEXT,
    lease_id TEXT,
    lease_expiry TEXT,
    completed_at TEXT,
    next_attempt_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key ON outbox_events(event_key);
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
CREATE TABLE IF NOT EXISTS registry_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    asset_id TEXT,
    event_type TEXT NOT NULL,
    run_id TEXT,
    actor TEXT NOT NULL DEFAULT '',
    before_hash TEXT NOT NULL DEFAULT '',
    after_hash TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    git_sha TEXT NOT NULL DEFAULT '',
    app_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    language_code TEXT NOT NULL,
    text_kind TEXT NOT NULL,
    text_content TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'provided',
    is_current INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'READY',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`

func mustSQLiteLedger(t *testing.T, db *sql.DB) *sqlitemediaregistry.Ledger {
	t.Helper()
	ledger, err := sqlitemediaregistry.NewLedger(db)
	if err != nil {
		t.Fatalf("new sqlite ledger: %v", err)
	}
	return ledger
}
