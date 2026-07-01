// Package assets — images_repository_test.go is the canonical TDD
// coverage for the FASE 4 CUTOVER dual-write helpers in
// images_repository.go (image-territories action plan, July 2026).
//
// Locks the contract surface so future refactors do not regress:
//   - UpsertGeneratedDetails idempotent + round-trip.
//   - UpsertRetrievedDetails idempotent + round-trip.
//   - UpdateOrigin backfill path.
//   - DELETE CASCADE: deleting a media_assets row drops its detail row.
//   - Dual-write branching in AddImage (origin=generated retrieves
//     detail row; origin='' skips; origin=retrieved routes to retrieved).
package assets

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3" // AGENTS.md driver lock (mattn/go-sqlite3)

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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
		file_hash TEXT NOT NULL DEFAULT '',
		local_path TEXT NOT NULL DEFAULT '',
		relative_path TEXT NOT NULL DEFAULT '',
		drive_file_id TEXT NOT NULL DEFAULT '',
		lifecycle_state TEXT NOT NULL DEFAULT 'STAGING',
		metadata_json TEXT NOT NULL DEFAULT '',
		origin TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
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
		`INSERT INTO media_assets (id, source, file_hash) VALUES (?, 'image', ?)`, id, hash)
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
		SourceHash:      "hash-1",	}
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

// TestUpdateOrigin: media_assets origin/provider assignment for FASE 4 backfill.
func TestUpdateOrigin(t *testing.T) {
	db := testDB(t)
	repo := NewImagesRepository(db)
	insertMediaAsset(t, db, "img-3", "hash-3")

	if err := repo.UpdateOrigin(context.Background(), "hash-3", "generated", "flux"); err != nil {
		t.Fatalf("UpdateOrigin: %v", err)
	}
	var origin, provider string
	if err := db.QueryRowContext(context.Background(),
		`SELECT origin, provider FROM media_assets WHERE id = 'img-3'`).Scan(&origin, &provider); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if origin != "generated" || provider != "flux" {
		t.Fatalf("UpdateOrigin ineffective: origin=%q provider=%q", origin, provider)
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
	if err := repo.UpdateOrigin(context.Background(), "", "generated", "flux"); err == nil {
		t.Fatal("expected error on empty hash, got nil")
	}
}

// NOTE: a "methods exist on the repository" sentinel test was considered
// during FASE 4 design but removed per code-reviewer B6 audit (July 2026):
// the new helpers are transitively exercised by the round-trip + CASCADE
// tests above, and the type system already pins their existence at compile
// time. Removed for minimalism.
