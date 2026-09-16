package media

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// TestClipLocationsDropsPhantomDriveRow pins the clip-commit invariant that a
// clip with neither a Drive identity nor a local path contributes NO
// asset_locations row.
//
// The canonical committer upserts on (asset_id, location_kind), so emitting a
// 'drive' location unconditionally wrote a PHANTOM primary location — empty
// external_id, empty uri — for every YouTube-sourced clip that was never
// published to Drive (observed live on yt_nYRefC7E9gU_0_60_whisper_v1, whose
// only drive row pointed at nothing). A reader that trusts the primary location
// then resolves an empty Drive id and fails far away from the cause.
//
// The invariant is about PHANTOM rows, not about the absence of a Drive id: a
// clip that does have bytes on disk gets a 'local' location instead (see
// TestClipLocationsRecordsLocalArtifactWhenNoDriveIdentity).
func TestClipLocationsDropsPhantomDriveRow(t *testing.T) {
	if got := clipLocations(youtubetypes.ClipAsset{}); len(got) != 0 {
		t.Fatalf("a clip without a Drive file id or a local path must contribute no location, got %+v", got)
	}
	if got := clipLocations(youtubetypes.ClipAsset{Drive: youtubetypes.ClipAssetDrive{FileID: "   "}}); len(got) != 0 {
		t.Fatalf("a whitespace-only Drive file id must contribute no location, got %+v", got)
	}

	locs := clipLocations(youtubetypes.ClipAsset{Drive: youtubetypes.ClipAssetDrive{
		FileID:      "drive-1",
		WebViewLink: "https://drive.example/drive-1",
	}})
	if len(locs) != 1 {
		t.Fatalf("a Drive-backed clip must contribute exactly one location, got %d", len(locs))
	}
	loc := locs[0]
	if loc.Kind != "drive" || loc.ExternalID != "drive-1" || loc.URI != "drive://drive-1" || !loc.IsPrimary {
		t.Fatalf("unexpected location %+v", loc)
	}
	if loc.WebViewLink != "https://drive.example/drive-1" {
		t.Fatalf("WebViewLink = %q, want the clip's Drive link", loc.WebViewLink)
	}
}

// TestClipLocationsCarriesRealDriveArtifactIdentity pins that the canonical
// Drive location declares the artifact's REAL size and type instead of the
// 0 / ” placeholders.
//
// Observed live (2026-09-16) on yt_gT0amKtXWdU_0_10_v1: the Drive object was
// 11,078,716 bytes (verified against the API) while asset_locations recorded
// file_size_bytes=0 and mime_type=” — the row could not be told apart from one
// whose Drive artifact was never measured. The bytes are already verified at
// upload time (the artifact SHA-256 is the asset's content identity), so a
// reader must never have to re-probe Drive for a fact the commit already knew.
//
// Non-vacuity: the pre-fix literal 0 / ” fails this test.
func TestClipLocationsCarriesRealDriveArtifactIdentity(t *testing.T) {
	const artifactSize = int64(11078716)
	locs := clipLocations(youtubetypes.ClipAsset{Drive: youtubetypes.ClipAssetDrive{
		FileID:      "drive-1",
		WebViewLink: "https://drive.example/drive-1",
		SizeBytes:   artifactSize,
	}})
	if len(locs) != 1 {
		t.Fatalf("a Drive-backed clip must contribute exactly one location, got %d", len(locs))
	}
	loc := locs[0]
	if loc.FileSizeBytes != artifactSize {
		t.Errorf("FileSizeBytes = %d, want the measured artifact size %d (the 0 placeholder makes a known size look unmeasured)",
			loc.FileSizeBytes, artifactSize)
	}
	if loc.MimeType != clipDriveMimeType {
		t.Errorf("MimeType = %q, want %q (the empty string cannot be interpreted by a reader)",
			loc.MimeType, clipDriveMimeType)
	}

	// Fail-closed on an unmeasured size: an absent measurement must stay 0
	// rather than being invented, so "unknown" remains representable.
	unmeasured := clipLocations(youtubetypes.ClipAsset{Drive: youtubetypes.ClipAssetDrive{FileID: "drive-2"}})
	if len(unmeasured) != 1 {
		t.Fatalf("a Drive-backed clip must contribute exactly one location, got %d", len(unmeasured))
	}
	if unmeasured[0].FileSizeBytes != 0 {
		t.Errorf("unmeasured FileSizeBytes = %d, want 0 (no invented size)", unmeasured[0].FileSizeBytes)
	}
}

// TestClipLocationsRecordsLocalArtifactWhenNoDriveIdentity pins the fix for the
// gap the live run exposed: a clip that was cut, measured, committed and
// INDEXED without any Drive delivery had ZERO asset_locations rows.
//
// Observed live (2026-09-16) on yt_iHaK0M-207o_60_70_v1, the artifact of an
// extraction that asked for no destination: media_assets held
// local_path=data/media/clips/general/.../yt_iHaK0M-207o_60_70_v1_verify-local-only.mp4
// (2,733,575 bytes on disk, verified with ls) while asset_locations was empty.
// The location surface is the canonical one — the schema header calls
// media_assets.local_path "deprecated in favour of asset_locations" — so a
// location-resolving consumer saw no content for an asset whose bytes were
// sitting on disk.
//
// Non-vacuity: the pre-fix projection returns nil here.
func TestClipLocationsRecordsLocalArtifactWhenNoDriveIdentity(t *testing.T) {
	const artifactSize = int64(2733575)
	const localPath = "data/media/clips/general/yt_iHaK0M-207o/yt_iHaK0M-207o_60_70_v1_verify-local-only.mp4"
	locs := clipLocations(youtubetypes.ClipAsset{
		LocalPath:     localPath,
		LegacyFileMD5: "437d7377a2a8adbe7f5be03a99aebff4ccb9f6edb8cf884da1ccc2c2e45de533",
		Drive:         youtubetypes.ClipAssetDrive{SizeBytes: artifactSize},
	})
	if len(locs) != 1 {
		t.Fatalf("a local-only clip must contribute exactly one location, got %d (%+v)", len(locs), locs)
	}
	loc := locs[0]
	if loc.Kind != "local" || loc.Provider != "local" {
		t.Errorf("Kind/Provider = %q/%q, want local/local", loc.Kind, loc.Provider)
	}
	if loc.URI != localPath {
		t.Errorf("URI = %q, want the artifact path %q", loc.URI, localPath)
	}
	if loc.FileSizeBytes != artifactSize {
		t.Errorf("FileSizeBytes = %d, want the measured artifact size %d", loc.FileSizeBytes, artifactSize)
	}
	if loc.MimeType != clipDriveMimeType {
		t.Errorf("MimeType = %q, want %q", loc.MimeType, clipDriveMimeType)
	}
	if loc.LegacyFileMD5 == "" {
		t.Error("LegacyFileMD5 is empty: the local bytes are verifiable, so their content digest must travel with the location")
	}
	if !loc.IsPrimary {
		t.Error("a local-only location must be primary: with no delivered copy it IS the asset's content")
	}

	// A whitespace-only local path is not a location, and neither is a clip
	// with no path at all — "no bytes this commit can point at" stays
	// representable.
	if got := clipLocations(youtubetypes.ClipAsset{LocalPath: "   "}); len(got) != 0 {
		t.Fatalf("a whitespace-only local path must contribute no location, got %+v", got)
	}

	// A Drive id still wins: the delivered copy is the canonical one, and the
	// local projection must not add a second row that would compete with it.
	driveBacked := clipLocations(youtubetypes.ClipAsset{
		LocalPath: localPath,
		Drive:     youtubetypes.ClipAssetDrive{FileID: "drive-9", SizeBytes: artifactSize},
	})
	if len(driveBacked) != 1 || driveBacked[0].Kind != "drive" {
		t.Fatalf("a Drive-backed clip must contribute exactly one drive location, got %+v", driveBacked)
	}
}

// TestClipCommitWritesLocalLocationForDeliverylessClip is the DB-live half of
// the same pin: the projection is only worth anything if the canonical commit
// actually lands the row. It drives the real clip commit request builder and
// the real PostgreSQL committer, then reads asset_locations back.
//
// Deliberately NON-DESTRUCTIVE: the write happens inside a caller-owned
// transaction that is rolled back, and the read happens before the rollback.
// The pin therefore runs against whatever TEST_POSTGRES_DSN names without
// needing a throwaway database and without leaving a row behind.
func TestClipCommitWritesLocalLocationForDeliverylessClip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping the DB-live clip-location pin")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for _, ddl := range []string{pgmigration.MediaSchemaDDL, pgmigration.MediaVectorSurfacesDDL} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("apply media migrations: %v", err)
		}
	}

	box := NewOutboxRepository(db)
	ledger, err := NewRegistry(db)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	committer := NewPostgresMediaCommitter(db, box, ledger, nil)

	const assetID = "yt_iHaK0M-207o_60_70_v1"
	const localPath = "data/media/clips/general/yt_iHaK0M-207o/yt_iHaK0M-207o_60_70_v1_verify-local-only.mp4"
	const artifactSize = int64(2733575)

	// The shape a destination-less extraction produces: bytes on disk, no
	// Drive identity at all.
	clipAsset := youtubetypes.ClipAsset{
		ID:            assetID,
		VideoID:       "iHaK0M-207o",
		LocalPath:     localPath,
		LegacyFileMD5: strings.Repeat("a", 64),
		SearchText:    "Mike Tyson clips",
		PolicyVersion: "v1",
		Drive:         youtubetypes.ClipAssetDrive{SizeBytes: artifactSize},
		Coordinates:   youtubetypes.ClipAssetCoordinates{StartSec: 60, EndSec: 70, Duration: 10},
		Metadata: youtubetypes.CanonicalClipMetadata{
			ClipID:          assetID,
			AssetID:         assetID,
			Title:           "local-only clip",
			Summary:         "local-only clip — first 10s",
			Description:     "local-only clip",
			SourceURL:       "https://www.youtube.com/watch?v=iHaK0M-207o",
			SourceProvider:  "youtube",
			VideoID:         "iHaK0M-207o",
			SourceTitle:     "local-only clip",
			SourceChannel:   "test channel",
			ClipStartSec:    60,
			ClipEndSec:      70,
			ClipDurationSec: 10,
			PolicyVersion:   "v1",
			SourceVersion:   "local-only-location-test",
		},
	}

	req, err := buildYouTubeCommitRequest(assetID, clipAsset)
	if err != nil {
		t.Fatalf("buildYouTubeCommitRequest: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := committer.CommitTx(ctx, tx, req); err != nil {
		t.Fatalf("CommitTx: %v", err)
	}

	var kind, uri, mime string
	var size int64
	var primary int
	if err := tx.QueryRowContext(ctx, `
		SELECT location_kind, uri, mime_type, file_size_bytes, is_primary
		FROM asset_locations WHERE asset_id = $1
	`, assetID).Scan(&kind, &uri, &mime, &size, &primary); err != nil {
		t.Fatalf("a clip with bytes on disk and no Drive identity must still record a location: %v", err)
	}
	if kind != "local" || uri != localPath {
		t.Errorf("location = %q/%q, want local/%q", kind, uri, localPath)
	}
	if mime != clipDriveMimeType || size != artifactSize {
		t.Errorf("location size/mime = %d/%q, want %d/%q", size, mime, artifactSize, clipDriveMimeType)
	}
	if primary != 1 {
		t.Errorf("is_primary = %d, want 1 (the local copy is the asset's only content)", primary)
	}
}

func TestMediaAssetRecord_TitleOrName(t *testing.T) {
	if got := (&MediaAssetRecord{Name: "n", Title: "t"}).TitleOrName(); got != "t" {
		t.Fatalf("TitleOrName = %q, want t", got)
	}
	if got := (&MediaAssetRecord{Name: "n"}).TitleOrName(); got != "n" {
		t.Fatalf("TitleOrName fallback = %q, want n", got)
	}
	if got := (*MediaAssetRecord)(nil).TitleOrName(); got != "" {
		t.Fatalf("nil TitleOrName = %q, want empty", got)
	}
}

func TestMediaAssetRecord_MetadataAccessors(t *testing.T) {
	rec := &MediaAssetRecord{MetadataJSON: `{
		"clip_summary": "summary",
		"topics": ["a", "b"],
		"speakers": ["s1"],
		"start_sec": 1.25,
		"count": 7
	}`}
	if got := rec.MetadataString("clip_summary"); got != "summary" {
		t.Fatalf("MetadataString = %q", got)
	}
	if got := rec.MetadataString("missing"); got != "" {
		t.Fatalf("missing MetadataString = %q", got)
	}
	if got := rec.MetadataStringSlice("topics"); len(got) != 2 || got[0] != "a" {
		t.Fatalf("MetadataStringSlice topics = %v", got)
	}
	if got := rec.MetadataStringSlice("speakers"); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("MetadataStringSlice speakers = %v", got)
	}
	if got := rec.MetadataStringSlice("missing"); got != nil {
		t.Fatalf("missing MetadataStringSlice = %v", got)
	}
	if got := rec.MetadataFloat("start_sec"); got != 1.25 {
		t.Fatalf("MetadataFloat start_sec = %v", got)
	}
	if got := rec.MetadataFloat("count"); got != 7 {
		t.Fatalf("MetadataFloat count = %v", got)
	}
	if got := rec.MetadataFloat("missing"); got != 0 {
		t.Fatalf("missing MetadataFloat = %v", got)
	}
}

func TestMediaAssetRecord_MalformedMetadataDegrades(t *testing.T) {
	rec := &MediaAssetRecord{MetadataJSON: "{not json"}
	if got := rec.MetadataString("anything"); got != "" {
		t.Fatalf("malformed metadata must degrade to empty, got %q", got)
	}
	if got := rec.MetadataFloat("anything"); got != 0 {
		t.Fatalf("malformed metadata must degrade to 0, got %v", got)
	}
}

func TestMediaHashCandidates(t *testing.T) {
	if got := mediaHashCandidates(""); got != nil {
		t.Fatalf("empty hash candidates = %v, want nil", got)
	}
	raw := mediaHashCandidates("abc")
	if len(raw) != 2 || raw[0] != "abc" || raw[1] != "abc" {
		t.Fatalf("raw hash candidates = %v", raw)
	}
	prefixed := mediaHashCandidates("sha256:abc")
	if len(prefixed) != 2 || prefixed[0] != "sha256:abc" || prefixed[1] != "abc" {
		t.Fatalf("prefixed hash candidates = %v", prefixed)
	}
}

// fakeScanRow asserts the scan destination count matches the supplied values and
// assigns them through reflection. It pins the shared projection/scan alignment:
// if a future edit adds a column without updating scanMediaAssetRecord (or vice
// versa) this test fails instead of silently mis-reading every media row.
type fakeScanRow struct {
	values []any
}

func (f *fakeScanRow) Scan(dest ...any) error {
	if len(dest) != len(f.values) {
		return fmt.Errorf("scan destination count %d != value count %d", len(dest), len(f.values))
	}
	for i, d := range dest {
		target := reflect.ValueOf(d)
		if target.Kind() != reflect.Ptr {
			return fmt.Errorf("dest[%d] is not a pointer", i)
		}
		value := reflect.ValueOf(f.values[i])
		if !value.Type().AssignableTo(target.Elem().Type()) {
			return fmt.Errorf("dest[%d]: cannot assign %T to %s", i, f.values[i], target.Elem().Type())
		}
		target.Elem().Set(value)
	}
	return nil
}

func TestScanMediaAssetRecord_ColumnAlignment(t *testing.T) {
	row := &fakeScanRow{values: []any{
		"id-1", "name", "file.mp4", "Title", "youtube", "video", "training", `["t1"]`,
		"ACTIVE", "INDEXED", int64(1234),
		"/local", "drive-1", "https://drive/1", "https://dl/1",
		"sha256:deadbeef", "https://thumb/1",
		"https://src", "youtube", "vid-1", "vid-1",
		int64(1000), int64(2000), "folder-1", "parent-1", "/a/b", "2026-09-13T10:00:00Z",
		`{"clip_summary":"s"}`, `["k1","k2"]`, "approved",
	}}
	rec, err := scanMediaAssetRecord(row)
	if err != nil {
		t.Fatalf("scanMediaAssetRecord: %v", err)
	}
	if rec.ID != "id-1" || rec.Title != "Title" || rec.SourceURL != "https://src" ||
		rec.SourceProvider != "youtube" || rec.SourceVideoID != "vid-1" ||
		rec.StartMS != 1000 || rec.EndMS != 2000 {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if len(rec.Tags) != 1 || rec.Tags[0] != "t1" {
		t.Fatalf("tags = %v", rec.Tags)
	}
	if rec.MetadataString("clip_summary") != "s" {
		t.Fatalf("metadata not retained: %q", rec.MetadataJSON)
	}
	if rec.FolderID != "folder-1" || rec.ParentFolderID != "parent-1" || rec.FolderPath != "/a/b" {
		t.Fatalf("folder projection = %q/%q/%q", rec.FolderID, rec.ParentFolderID, rec.FolderPath)
	}
	if rec.CreatedAtTime().IsZero() {
		t.Fatalf("created_at not parsed: %q", rec.CreatedAt)
	}
	if rec.SearchTerms != `["k1","k2"]` {
		t.Fatalf("search_terms = %q", rec.SearchTerms)
	}
	if rec.ReviewStatus != "approved" {
		t.Fatalf("review_status = %q", rec.ReviewStatus)
	}
	hydrated := rec.HydrateAsset()
	if len(hydrated.SearchTerms) != 2 || hydrated.SearchTerms[0] != "k1" || hydrated.SearchTerms[1] != "k2" {
		t.Fatalf("hydrated search_terms = %v", hydrated.SearchTerms)
	}
	if string(hydrated.ReviewStatus) != "approved" {
		t.Fatalf("hydrated review_status = %q", hydrated.ReviewStatus)
	}
	meta := rec.MetadataMap()
	meta["mutated"] = true
	if _, leaked := rec.MetadataMap()["mutated"]; leaked {
		t.Fatal("MetadataMap must return a copy, not the internal map")
	}
}
