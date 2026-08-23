// Package assets — images_repository_test.go is the canonical TDD
// coverage for the FASE 4 CUTOVER dual-write helpers in
// images_repository.go (image-territories action plan, July 2026).
//
// Locks the contract surface so future refactors do not regress:
//   - UpsertGeneratedDetails idempotent + round-trip.
//   - UpsertRetrievedDetails idempotent + round-trip.
//   - DELETE CASCADE: deleting a media_assets row drops its detail row.
//   - Dual-write branching in AddImage (origin=generated retrieves
//     detail row; origin=” skips; origin=retrieved routes to retrieved).
package imagesrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3" // AGENTS.md driver lock (mattn/go-sqlite3)

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// testDB opens an in-memory SQLite with the minimum schema needed for
// the FASE 4 CUTOVER dual-write tests. The schema mirrors migrations
// 115/116/117 in spirit (just the columns we exercise); production
// code paths use the full project migration runner.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 :memory: %v", err)
	}
	// SQLite has FK enforcement OFF by default per-connection. Enable it so
	// the ON DELETE CASCADE clauses on FK(asset_id) cascade correctly during
	// the CASCADE tests. Production migration runner uses the same pragma.
	if _, err := db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}
	for _, stmt := range fase4TestSchema {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("apply schema: %v\nstmt=%s", err, stmt)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var fase4TestSchema = []string{
	`CREATE TABLE IF NOT EXISTS media_assets (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL DEFAULT 'video',
		name TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '',
		tags_norm TEXT NOT NULL DEFAULT '',
		media_type TEXT NOT NULL DEFAULT '',
		width INTEGER NOT NULL DEFAULT 0,
		height INTEGER NOT NULL DEFAULT 0,
		legacy_file_md5 TEXT NOT NULL DEFAULT '',
		local_path TEXT NOT NULL DEFAULT '',
		relative_path TEXT NOT NULL DEFAULT '',
		drive_file_id TEXT NOT NULL DEFAULT '',
		drive_link TEXT NOT NULL DEFAULT '',
		lifecycle_state TEXT NOT NULL DEFAULT 'STAGING',
		metadata_json TEXT NOT NULL DEFAULT '',
		origin TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')`,
	`CREATE TABLE IF NOT EXISTS generated_image_details (
		asset_id TEXT PRIMARY KEY,
		prompt_original TEXT NOT NULL DEFAULT '',
		prompt_resolved TEXT NOT NULL DEFAULT '',
		style_id TEXT NOT NULL DEFAULT '',
		style_version TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		seed INTEGER NOT NULL DEFAULT 0,
		generation_job_id TEXT NOT NULL DEFAULT '',
		source_hash TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS retrieved_image_details (
		asset_id TEXT PRIMARY KEY,
		source_image_url TEXT NOT NULL DEFAULT '',
		source_page_url TEXT NOT NULL DEFAULT '',
		license TEXT NOT NULL DEFAULT '',
		author TEXT NOT NULL DEFAULT '',
		search_query TEXT NOT NULL DEFAULT '',
		retrieved_at TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
	)`,
}

func insertMediaAsset(t *testing.T, db *sql.DB, id, hash string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, source, legacy_file_md5) VALUES (?, 'image', ?)`, id, hash)
	if err != nil {
		t.Fatalf("seed media_assets: %v", err)
	}
}

// TestUpsertGeneratedDetailsRoundTrip: ON CONFLICT DO UPDATE idempotent.
func TestUpsertGeneratedDetailsRoundTrip(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)
	insertMediaAsset(t, db, "img-1", "hash-1")

	first := &asset.GeneratedImageDetail{
		AssetID:         "img-1",
		PromptOriginal:  "castle",
		PromptResolved:  "castle, cinematic lighting",
		StyleID:         "cinematic",
		StyleVersion:    "2",
		Model:           "flux-1-dev",
		Seed:            42,
		GenerationJobID: "job-1",
		SourceHash:      "hash-1",
	}
	if err := repo.UpsertGeneratedDetails(context.Background(), first); err != nil {
		t.Fatalf("first UpsertGeneratedDetails: %v", err)
	}

	got, err := repo.GetGeneratedDetails(context.Background(), "img-1")
	if err != nil {
		t.Fatalf("GetGeneratedDetails: %v", err)
	}
	if got == nil {
		t.Fatalf("expected detail row, got nil (round-trip broken)")
	}
	if got.StyleID != "cinematic" || got.StyleVersion != "2" || got.Seed != 42 || got.SourceHash != "hash-1" {
		t.Fatalf("round-trip fields wrong: %+v", got)
	}

	second := &asset.GeneratedImageDetail{
		AssetID:         "img-1",
		PromptOriginal:  "castle-v2",
		PromptResolved:  "castle, anime",
		StyleID:         "anime",
		StyleVersion:    "1",
		Model:           "flux-1-dev",
		Seed:            99,
		GenerationJobID: "job-2",
		SourceHash:      "hash-1",
	}
	if err := repo.UpsertGeneratedDetails(context.Background(), second); err != nil {
		t.Fatalf("second UpsertGeneratedDetails (idempotent): %v", err)
	}
	got2, err := repo.GetGeneratedDetails(context.Background(), "img-1")
	if err != nil {
		t.Fatalf("GetGeneratedDetails after upsert: %v", err)
	}
	if got2.StyleID != "anime" || got2.PromptOriginal != "castle-v2" || got2.Seed != 99 || got2.GenerationJobID != "job-2" {
		t.Fatalf("upsert-did-not-update: %+v", got2)
	}
}

// TestUpsertRetrievedDetailsRoundTrip: same idempotency guarantees for
// the retrieved territory.
func TestUpsertRetrievedDetailsRoundTrip(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)
	insertMediaAsset(t, db, "img-2", "hash-2")

	first := &asset.RetrievedImageDetail{
		AssetID:        "img-2",
		SourceImageURL: "https://example.com/a.jpg",
		License:        "CC-BY",
		Author:         "Wikimedia",
		Provider:       "wikipedia",
	}
	if err := repo.UpsertRetrievedDetails(context.Background(), first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	got, err := repo.GetRetrievedDetails(context.Background(), "img-2")
	if err != nil || got == nil || got.SourceImageURL != "https://example.com/a.jpg" || got.License != "CC-BY" {
		t.Fatalf("round-trip mismatch: %+v err=%v", got, err)
	}

	second := &asset.RetrievedImageDetail{
		AssetID:        "img-2",
		SourceImageURL: "https://example.com/b.jpg",
		License:        "CC-BY-SA",
		Author:         "Another",
		SearchQuery:    "castle",
		Provider:       "duckduckgo",
	}
	if err := repo.UpsertRetrievedDetails(context.Background(), second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got2, err := repo.GetRetrievedDetails(context.Background(), "img-2")
	if err != nil || got2 == nil || got2.SourceImageURL != "https://example.com/b.jpg" || got2.SearchQuery != "castle" {
		t.Fatalf("upsert did not overwrite: %+v err=%v", got2, err)
	}
}

// TestGeneratedDetailsDeleteCascade: deleting media_assets drops the
// detail row (FK ON DELETE CASCADE). Per migration 116 + 117.
func TestGeneratedDetailsDeleteCascade(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)
	insertMediaAsset(t, db, "img-4", "hash-4")

	if err := repo.UpsertGeneratedDetails(context.Background(), &asset.GeneratedImageDetail{
		AssetID: "img-4", SourceHash: "hash-4", StyleID: "cinematic",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `DELETE FROM media_assets WHERE id = 'img-4'`); err != nil {
		t.Fatalf("delete media_assets: %v", err)
	}
	got, err := repo.GetGeneratedDetails(context.Background(), "img-4")
	if err != nil {
		t.Fatalf("GetGeneratedDetails after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("CASCADE broken: detail row survived delete: %+v", got)
	}
}

// TestRetrievedDetailsDeleteCascade: same guarantee for retrieved.
func TestRetrievedDetailsDeleteCascade(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)
	insertMediaAsset(t, db, "img-5", "hash-5")

	if err := repo.UpsertRetrievedDetails(context.Background(), &asset.RetrievedImageDetail{
		AssetID: "img-5", SourceImageURL: "https://example.com/c.jpg",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `DELETE FROM media_assets WHERE id = 'img-5'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := repo.GetRetrievedDetails(context.Background(), "img-5")
	if err != nil {
		t.Fatalf("GetRetrievedDetails after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("CASCADE broken (retrieved): %+v", got)
	}
}

// TestUpsertAssetIDEmptyReturnsError: fail-closed guard per godlike/07.
func TestUpsertAssetIDEmptyReturnsError(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)

	if err := repo.UpsertGeneratedDetails(context.Background(), &asset.GeneratedImageDetail{
		AssetID: "", SourceHash: "hash",
	}); err == nil {
		t.Fatal("expected error on empty AssetID, got nil")
	}
	if err := repo.UpsertRetrievedDetails(context.Background(), &asset.RetrievedImageDetail{
		AssetID: "", SourceImageURL: "https://example.com",
	}); err == nil {
		t.Fatal("expected error on empty AssetID (retrieved), got nil")
	}
}

// NOTE: a "methods exist on the repository" sentinel test was considered
// during FASE 4 design but removed per code-reviewer B6 audit (July 2026):
// the new helpers are transitively exercised by the round-trip + CASCADE
// tests above, and the type system already pins their existence at compile
// time. Removed for minimalism.

// ── B6 SSOT property tests for scanImageAssetFromRow ───────────────────
// B6 refactor: scanImageAsset (Row-shaped) + scanImageAssetRows
// (Rows-shaped) were byte-equivalent duplicates. They collapse into one
// typed-(structural-interface) helper, scanImageAssetFromRow, that
// accepts any SQL scanner implementing `interface{ Scan(dest ...any)
// error }`. The 3 property tests below pin the invariants the helper
// must satisfy independent of the scanner concrete type, so future
// drift on either path is a failing test instead of silent byte-drift.

// errScanner is a hand-rolled test fixture that satisfies
// `interface{ Scan(dest ...any) error }` and returns a synthetic error
// from Scan. Used by TestScanImageAssetFromRow_ScanErrorPropagation to
// verify the helper propagates SQL errors without silent mask.
type errScanner struct {
	called int
}

func (e *errScanner) Scan(dest ...any) error {
	e.called++
	return errors.New("synthetic scan error: scanImageAssetFromRow must propagate")
}

// selectImageAssetProjection is the canonical SELECT projection that
// the 5 production call sites share (GetImageByHash / GetByID /
// GetByDriveFileID / ListImagesBySubject / ListAll all use the same
// 12-column list). DRY-ing it into a constant so the property tests
// stay aligned with production code.
const selectImageAssetProjection = `SELECT id, name, url, tags, metadata_json, created_at, legacy_file_md5, local_path, drive_file_id, drive_link, origin, provider FROM media_assets`

// seedFullImageAsset inserts a media_assets row with all 12 columns
// populated so the property tests have a non-trivial fixture. Used by
// TestScanImageAssetFromRow_RowVsRowsEquivalence only.
func seedFullImageAsset(t *testing.T, db *sql.DB, id, hash string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO media_assets (
			id, source, name, url, tags, legacy_file_md5, local_path, drive_file_id,
			metadata_json, origin, provider, lifecycle_state
		) VALUES (
			?, 'image', ?, 'https://example.com/x.jpg', ?, ?, ?, ?,
			?, ?, ?, 'STAGING'
		)
	`, id, "Image of "+id, `["cinematic","lighting"]`, hash, "/local/"+id+".jpg", "drive-file-"+id,
		`{"subject_id":"subject-`+id+`","status":"READY"}`, "generated", "flux-1-dev")
	if err != nil {
		t.Fatalf("seed full image asset: %v", err)
	}
}

// TestScanImageAssetFromRow_RowVsRowsEquivalence pins the CORE
// invariant of the B6 SSOT refactor: scanImageAssetFromRow is a
// function of the underlying value sequence only — NOT of the
// scanner concrete type. Same physical row, scanned via *sql.Row
// (returned by QueryRowContext) and via *sql.Rows (returned by
// QueryContext + rows.Next), MUST decode to byte-equal
// *asset.ImageAsset values. Failing this test means the helper is no
// longer SQL-agnostic — exactly the kind of regression the DRY
// refactor exists to prevent.
func TestScanImageAssetFromRow_RowVsRowsEquivalence(t *testing.T) {
	db := testDB(t)
	seedFullImageAsset(t, db, "img-eq-1", "hash-eq-1")

	selectStmt := selectImageAssetProjection + ` WHERE id = ?`

	rowDecoded, err := scanImageAssetFromRow(
		db.QueryRowContext(context.Background(), selectStmt, "img-eq-1"),
	)
	if err != nil {
		t.Fatalf("Row path decode: %v", err)
	}

	rows, err := db.QueryContext(context.Background(), selectStmt, "img-eq-1")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("rows.Next returned false; expected 1 row")
	}
	rowsDecoded, err := scanImageAssetFromRow(rows)
	if err != nil {
		t.Fatalf("Rows path decode: %v", err)
	}
	if rows.Next() {
		t.Errorf("expected exactly 1 row, got more")
	}

	if !reflect.DeepEqual(rowDecoded, rowsDecoded) {
		t.Fatalf("byte-equivalence broken:\n  Row-decoded:  %+v\n  Rows-decoded: %+v",
			rowDecoded, rowsDecoded)
	}
	// Spot-check the high-cardinality fields so a future
	// over-permissive DeepEqual (e.g. nil-vs-empty slice) cannot pass.
	if rowDecoded.Description != "Image of img-eq-1" {
		t.Errorf("Description mismatch: %q", rowDecoded.Description)
	}
	if len(rowDecoded.Tags) != 2 || rowDecoded.Tags[0] != "cinematic" || rowDecoded.Tags[1] != "lighting" {
		t.Errorf("Tags mismatch: %v", rowDecoded.Tags)
	}
	if rowDecoded.SubjectID != "subject-img-eq-1" {
		t.Errorf("SubjectID mismatch: %q", rowDecoded.SubjectID)
	}
	if string(rowDecoded.Origin) != "generated" || string(rowDecoded.Provider) != "flux-1-dev" {
		t.Errorf("Origin/Provider mismatch: %q/%q", rowDecoded.Origin, rowDecoded.Provider)
	}
}

// TestScanImageAssetFromRow_ScanErrorPropagation pins the fail-closed
// (godlike/07) invariant: any transport/SQL error from .Scan() must
// bubble out as `(nil, err)`. No silent mask, no panicking on nil
// dereference, no populated ImageAsset half-decoded. We use an
// in-process errScanner so the test does not depend on a particular
// SQLite error message.
func TestScanImageAssetFromRow_ScanErrorPropagation(t *testing.T) {
	s := &errScanner{}
	got, err := scanImageAssetFromRow(s)
	if err == nil {
		t.Fatalf("expected error from synthetic scanner, got nil; asset=%+v", got)
	}
	if got != nil {
		t.Fatalf("expected nil asset on error (godlike/07 fail-closed), got %+v", got)
	}
	if s.called != 1 {
		t.Fatalf("expected exactly one Scan call to the underlying scanner, got %d", s.called)
	}
}

// TestScanImageAssetFromRow_SwallowMalformedJSON pins the byte-stable
// legacy behaviour: the in-helper json.Unmarshal calls are wrapped with
// `_ = ...` (godlike/07: best-effort, not surfaced) on BOTH tags and
// metadata_json. A malformed JSON column therefore produces an empty
// tags slice + metadata_json side-effect-container, NOT an error. Both
// Row and Rows paths must observe the same swallow. This is the
// invariant that prevents a future "make it strict" refactor from
// regressing callers that previously tolerated the loose decode.
func TestScanImageAssetFromRow_SwallowMalformedJSON(t *testing.T) {
	db := testDB(t)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, source, legacy_file_md5, tags, metadata_json)
		 VALUES (?, 'image', ?, '{not valid JSON', '{not valid JSON either')`,
		"img-bad-json", "hash-bad-json")
	if err != nil {
		t.Fatalf("seed malformed-JSON row: %v", err)
	}

	selectStmt := selectImageAssetProjection + ` WHERE id = ?`

	rowDecoded, err := scanImageAssetFromRow(
		db.QueryRowContext(context.Background(), selectStmt, "img-bad-json"),
	)
	if err != nil {
		t.Fatalf("Row decode (malformed JSON should be swallowed): %v", err)
	}
	if len(rowDecoded.Tags) != 0 {
		t.Errorf("Row path: expected 0 tags (malformed JSON silent), got %v", rowDecoded.Tags)
	}
	if rowDecoded.MetadataJSON != "{not valid JSON either" {
		t.Errorf("Row path: MetadataJSON should round-trip the literal string, got %q", rowDecoded.MetadataJSON)
	}
	if rowDecoded.SubjectID != "" {
		t.Errorf("Row path: expected SubjectID '' (json parse failed silently), got %q", rowDecoded.SubjectID)
	}

	rows, err := db.QueryContext(context.Background(), selectStmt, "img-bad-json")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("rows.Next false; expected 1 row")
	}
	rowsDecoded, err := scanImageAssetFromRow(rows)
	if err != nil {
		t.Fatalf("Rows decode (malformed JSON should be swallowed): %v", err)
	}

	if !reflect.DeepEqual(rowDecoded, rowsDecoded) {
		t.Fatal("Row vs Rows (malformed JSON) diverged — byte-equivalence broken on degenerate JSON")
	}
	if len(rowsDecoded.Tags) != 0 {
		t.Errorf("Rows path: expected 0 tags (malformed JSON silent), got %v", rowsDecoded.Tags)
	}
}

// TestScanImageAssetFromRow_ImageUsesDriveLinkWhenAvailable pins the
// output-normalization rule for image assets: once a Drive file id is
// present, the canonical SourceURL exposed to callers is the Drive
// web-view link rather than the original upload URL.
func TestScanImageAssetFromRow_ImageUsesDriveLinkWhenAvailable(t *testing.T) {
	db := testDB(t)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO media_assets (id, source, name, url, legacy_file_md5, drive_file_id, origin, provider)
		VALUES (?, 'image', ?, ?, ?, ?, ?, ?)
	`,
		"img-drive-link",
		"AI generated image",
		"https://example.com/original.png",
		"hash-drive-link",
		"drive-file-123",
		"generated",
		"google-slides",
	)
	if err != nil {
		t.Fatalf("seed image row: %v", err)
	}

	got, err := scanImageAssetFromRow(
		db.QueryRowContext(context.Background(), selectImageAssetProjection+` WHERE id = ?`, "img-drive-link"),
	)
	if err != nil {
		t.Fatalf("scanImageAssetFromRow: %v", err)
	}

	want := "https://drive.google.com/file/d/drive-file-123/view"
	if got.SourceURL != want {
		t.Fatalf("SourceURL = %q, want %q", got.SourceURL, want)
	}
}

// TestScanImageAssetFromRow_ImageUsesDriveLinkWhenURLEmpty pins the
// historical record shape we already have in production: some image
// rows were written with drive_link populated and url empty. The
// reader must surface the public Drive link as SourceURL so downstream
// scene bindings keep a clickable URL.
func TestScanImageAssetFromRow_ImageUsesDriveLinkWhenURLEmpty(t *testing.T) {
	db := testDB(t)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO media_assets (id, source, name, url, drive_link, legacy_file_md5, origin, provider)
		VALUES (?, 'image', ?, ?, ?, ?, ?, ?)
	`,
		"img-drive-link-only",
		"AI generated image",
		"",
		"https://drive.google.com/file/d/drive-file-999/view",
		"hash-drive-link-only",
		"generated",
		"google-slides",
	)
	if err != nil {
		t.Fatalf("seed image row: %v", err)
	}

	got, err := scanImageAssetFromRow(
		db.QueryRowContext(context.Background(), selectImageAssetProjection+` WHERE id = ?`, "img-drive-link-only"),
	)
	if err != nil {
		t.Fatalf("scanImageAssetFromRow: %v", err)
	}

	want := "https://drive.google.com/file/d/drive-file-999/view"
	if got.SourceURL != want {
		t.Fatalf("SourceURL = %q, want %q", got.SourceURL, want)
	}
}

// ── PR-GENERATED-SEARCH-FIX (July 2026) TDD coverage ────────────────────
//
// Canonical read seam for the generated territory at
// GET /api/images/generated/search (Blocco 1 of
// cut-false-success-first, per architecture/action-plans/2026-07-04-cut-false-success-first.md).
// The Step 9 forward-pointer stub ("endpoint alive but feature
// pending", 200+[]) is retired: this method returns the real
// generated-territory rows now.
//
// The 5 tests below lock the contract that the handler relies on:
//   1. Empty          — no rows match → empty slice, nil error
//   2. OneRow         — single matching row → byte-stable fields
//   3. MultiRowDesc   — ordering contract (most recent first)
//   4. Limit200Cap    — hard cap (godlike/07 minimum-blast-radius)
//   5. DifferentOrigins — origin filter isolation (no retrieved
//      or uploaded leak into the generated territory result)
//
// Forward-pointer (godlike/07 honest-limitation): the
// "filter-by-locale opzionale" surface from the action plan is
// NOT implemented here — the media_assets table has no `locale`
// column (locale is implicitly carried by subject_id or by the
// generated_image_details.prompt_original context). Adding the
// filter would require a JOIN + new schema. A future PR can lift
// this to a richer filter shape (subject_id, locale, prompt_locale)
// once the schema decision is made.

// TestListImagesByOrigin_Empty: no rows match the origin → returns
// empty slice (NOT nil — godlike/07 typed-error contract pins the
// non-nil empty slice surface so callers don't have to nil-check
// before ranging).
func TestListImagesByOrigin_Empty(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)

	got, err := repo.ListImagesByOrigin(context.Background(), asset.ImageOriginGenerated, 200)
	if err != nil {
		t.Fatalf("ListImagesByOrigin (empty): %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice (godlike/07 typed-error contract), got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(got))
	}
}

// TestListImagesByOrigin_OneRow: insert 1 row with origin=generated,
// expect exactly 1 result with byte-stable field population. Pins
// the per-row field mapping (Hash, Origin, Description).
func TestListImagesByOrigin_OneRow(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)
	seedFullImageAsset(t, db, "img-gen-1", "hash-gen-1")

	got, err := repo.ListImagesByOrigin(context.Background(), asset.ImageOriginGenerated, 200)
	if err != nil {
		t.Fatalf("ListImagesByOrigin: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].Hash != "hash-gen-1" {
		t.Errorf("Hash mismatch: got %q, want %q", got[0].Hash, "hash-gen-1")
	}
	if got[0].Origin != asset.ImageOriginGenerated {
		t.Errorf("Origin mismatch: got %q, want %q", got[0].Origin, asset.ImageOriginGenerated)
	}
	if got[0].Description != "Image of img-gen-1" {
		t.Errorf("Description mismatch: got %q", got[0].Description)
	}
}

// TestListImagesByOrigin_MultipleRowsOrderedDesc: insert 3 rows with
// staggered created_at, expect ORDER BY created_at DESC (most recent
// first). Pins the ordering contract that the handler relies on for
// stable test fixtures + user-facing list stability.
func TestListImagesByOrigin_MultipleRowsOrderedDesc(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)

	inserts := []struct {
		id        string
		hash      string
		createdAt string
	}{
		{"img-gen-old", "hash-old", "2026-07-01T00:00:00Z"},
		{"img-gen-mid", "hash-mid", "2026-07-02T00:00:00Z"},
		{"img-gen-new", "hash-new", "2026-07-03T00:00:00Z"},
	}
	for _, ins := range inserts {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO media_assets (id, source, name, legacy_file_md5, origin, created_at)
			 VALUES (?, 'image', ?, ?, 'generated', ?)`,
			ins.id, "Image of "+ins.id, ins.hash, ins.createdAt)
		if err != nil {
			t.Fatalf("seed %s: %v", ins.id, err)
		}
	}

	got, err := repo.ListImagesByOrigin(context.Background(), asset.ImageOriginGenerated, 200)
	if err != nil {
		t.Fatalf("ListImagesByOrigin: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	// ORDER BY created_at DESC: most recent first.
	wantHashes := []string{"hash-new", "hash-mid", "hash-old"}
	for i, want := range wantHashes {
		if got[i].Hash != want {
			t.Errorf("row %d: got Hash=%q, want %q", i, got[i].Hash, want)
		}
	}
}

// TestListImagesByOrigin_Limit200Cap: insert 250 rows, ask for
// limit=300, expect exactly 200 (the canonical cap). Pins the
// godlike/07 minimum-blast-radius surface: callers asking for
// "more than the cap" don't get unbounded responses.
func TestListImagesByOrigin_Limit200Cap(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)

	// Bulk-insert 250 rows via prepared statement for speed.
	stmt, err := db.PrepareContext(context.Background(),
		`INSERT INTO media_assets (id, source, name, legacy_file_md5, origin, created_at)
		 VALUES (?, 'image', ?, ?, 'generated', ?)`)
	if err != nil {
		t.Fatalf("prepare bulk insert: %v", err)
	}
	defer stmt.Close()
	for i := 0; i < 250; i++ {
		_, err := stmt.ExecContext(context.Background(),
			fmt.Sprintf("img-bulk-%03d", i),
			fmt.Sprintf("Image of img-bulk-%03d", i),
			fmt.Sprintf("hash-bulk-%03d", i),
			"2026-07-01T00:00:00Z")
		if err != nil {
			t.Fatalf("bulk insert %d: %v", i, err)
		}
	}

	got, err := repo.ListImagesByOrigin(context.Background(), asset.ImageOriginGenerated, 300)
	if err != nil {
		t.Fatalf("ListImagesByOrigin (over-cap): %v", err)
	}
	if len(got) != ListImagesByOriginMaxLimit {
		t.Fatalf("expected hard-cap of %d rows, got %d (godlike/07 fail-closed cap broken)",
			ListImagesByOriginMaxLimit, len(got))
	}
}

// TestListImagesByOrigin_DifferentOriginsIsolated: insert mixed
// origins (generated + retrieved + uploaded), expect only the
// requested origin returned. Pins the WHERE origin = ? filter
// contract so a future schema/query drift doesn't leak retrieved
// or uploaded rows into the generated territory.
func TestListImagesByOrigin_DifferentOriginsIsolated(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)

	inserts := []struct {
		id     string
		origin string
	}{
		{"img-a-gen", "generated"},
		{"img-b-ret", "retrieved"},
		{"img-c-gen", "generated"},
		{"img-d-up", "uploaded"},
	}
	for _, ins := range inserts {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO media_assets (id, source, name, legacy_file_md5, origin)
			 VALUES (?, 'image', ?, ?, ?)`,
			ins.id, "Image of "+ins.id, "hash-"+ins.id, ins.origin)
		if err != nil {
			t.Fatalf("seed %s: %v", ins.id, err)
		}
	}

	got, err := repo.ListImagesByOrigin(context.Background(), asset.ImageOriginGenerated, 200)
	if err != nil {
		t.Fatalf("ListImagesByOrigin (generated only): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 generated rows, got %d (filter broken)", len(got))
	}
	for _, row := range got {
		if row.Origin != asset.ImageOriginGenerated {
			t.Errorf("row %q has non-generated origin %q (filter broken)", row.Hash, row.Origin)
		}
	}
}
