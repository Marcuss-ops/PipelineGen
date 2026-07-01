// images_repository_test.go
//
// FASE 4A EXPAND tests (July 2026, image-territories action plan).
// Mirrors channels_repository_test.go: in-memory SQLite with FK
// enforcement ON via `?_foreign_keys=on` DSN, manually recreated
// minimum schema (decoupled from production migration runner).

package assets

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openImageDetailsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := []string{
		`CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT 'image',
			name TEXT,
			url TEXT,
			tags TEXT,
			tags_norm TEXT,
			media_type TEXT,
			width INTEGER,
			height INTEGER,
			file_hash TEXT,
			local_path TEXT,
			relative_path TEXT,
			drive_file_id TEXT,
			lifecycle_state TEXT NOT NULL DEFAULT 'STAGING',
			metadata_json TEXT,
			origin TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE generated_image_details (
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
		`CREATE TABLE retrieved_image_details (
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
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema exec: %v", err)
		}
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable FK: %v", err)
	}
	return db
}

func insertMediaAssetRow(t *testing.T, db *sql.DB, id, origin, provider string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets
		(id, source, name, media_type, origin, provider, lifecycle_state, created_at, updated_at)
		VALUES (?, 'image', ?, 'image', ?, ?, 'STAGING', datetime('now'), datetime('now'))`,
		id, "img-"+id, origin, provider)
	if err != nil {
		t.Fatalf("insert media_assets %q: %v", id, err)
	}
}

func TestGetGeneratedDetails_RoundTrip(t *testing.T) {
	db := openImageDetailsTestDB(t)
	r := NewImagesRepository(db)
	ctx := context.Background()
	id := "img_abc123"
	insertMediaAssetRow(t, db, id, "generated", "flux")

	if _, err := db.Exec(`INSERT INTO generated_image_details
		(asset_id, prompt_original, prompt_resolved, style_id, style_version, model, seed, generation_job_id, source_hash)
		VALUES (?, 'a castle on a hill', 'a castle on a hill, cinematic lighting', 'cinematic', 'v1', 'flux.1-schnell', 42, 'job-uuid-1', 'sha-abc')`, id); err != nil {
		t.Fatalf("insert generated detail: %v", err)
	}

	d, err := r.GetGeneratedDetails(ctx, id)
	if err != nil {
		t.Fatalf("GetGeneratedDetails: %v", err)
	}
	if d == nil {
		t.Fatalf("GetGeneratedDetails returned nil row")
	}
	if d.AssetID != id {
		t.Errorf("AssetID = %q; want %q", d.AssetID, id)
	}
	if d.PromptOriginal != "a castle on a hill" {
		t.Errorf("PromptOriginal = %q", d.PromptOriginal)
	}
	if d.PromptResolved != "a castle on a hill, cinematic lighting" {
		t.Errorf("PromptResolved = %q", d.PromptResolved)
	}
	if d.StyleID != "cinematic" {
		t.Errorf("StyleID = %q", d.StyleID)
	}
	if d.StyleVersion != "v1" {
		t.Errorf("StyleVersion = %q", d.StyleVersion)
	}
	if d.Model != "flux.1-schnell" {
		t.Errorf("Model = %q", d.Model)
	}
	if d.Seed != 42 {
		t.Errorf("Seed = %d; want 42", d.Seed)
	}
	if d.GenerationJobID != "job-uuid-1" {
		t.Errorf("GenerationJobID = %q", d.GenerationJobID)
	}
	if d.SourceHash != "sha-abc" {
		t.Errorf("SourceHash = %q", d.SourceHash)
	}
}

func TestGetRetrievedDetails_RoundTrip(t *testing.T) {
	db := openImageDetailsTestDB(t)
	r := NewImagesRepository(db)
	ctx := context.Background()
	id := "img_xyz789"
	insertMediaAssetRow(t, db, id, "retrieved", "duckduckgo")

	if _, err := db.Exec(`INSERT INTO retrieved_image_details
		(asset_id, source_image_url, source_page_url, license, author, search_query, retrieved_at, provider)
		VALUES (?, 'https://upload.wikimedia.org/x.png', 'https://en.wikipedia.org/wiki/Y', 'CC-BY-SA-4.0', 'Alice', 'castle sunset', '2026-07-01T10:00:00Z', 'duckduckgo')`, id); err != nil {
		t.Fatalf("insert retrieved detail: %v", err)
	}

	d, err := r.GetRetrievedDetails(ctx, id)
	if err != nil {
		t.Fatalf("GetRetrievedDetails: %v", err)
	}
	if d == nil {
		t.Fatalf("GetRetrievedDetails returned nil row")
	}
	if d.AssetID != id {
		t.Errorf("AssetID = %q", d.AssetID)
	}
	if d.SourceImageURL != "https://upload.wikimedia.org/x.png" {
		t.Errorf("SourceImageURL = %q", d.SourceImageURL)
	}
	if d.SourcePageURL != "https://en.wikipedia.org/wiki/Y" {
		t.Errorf("SourcePageURL = %q", d.SourcePageURL)
	}
	if d.License != "CC-BY-SA-4.0" {
		t.Errorf("License = %q", d.License)
	}
	if d.Author != "Alice" {
		t.Errorf("Author = %q", d.Author)
	}
	if d.SearchQuery != "castle sunset" {
		t.Errorf("SearchQuery = %q", d.SearchQuery)
	}
	if d.RetrievedAt != "2026-07-01T10:00:00Z" {
		t.Errorf("RetrievedAt = %q", d.RetrievedAt)
	}
	if d.Provider != "duckduckgo" {
		t.Errorf("Provider = %q", d.Provider)
	}
}

func TestFK_Cascade_DeleteMediaAsset_CascadesDetail(t *testing.T) {
	db := openImageDetailsTestDB(t)
	id := "img_cascade"
	insertMediaAssetRow(t, db, id, "generated", "flux")
	if _, err := db.Exec(`INSERT INTO generated_image_details (asset_id, prompt_original) VALUES (?, 'p')`, id); err != nil {
		t.Fatalf("insert gen: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO retrieved_image_details (asset_id, source_image_url) VALUES (?, 'u')`, id); err != nil {
		t.Fatalf("insert ret: %v", err)
	}

	nGen, nRet := 1, 1
	if err := db.QueryRow(`SELECT COUNT(*) FROM generated_image_details WHERE asset_id = ?`, id).Scan(&nGen); err != nil {
		t.Fatalf("count gen: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM retrieved_image_details WHERE asset_id = ?`, id).Scan(&nRet); err != nil {
		t.Fatalf("count ret: %v", err)
	}
	if nGen != 1 || nRet != 1 {
		t.Fatalf("pre-delete counts: %d/%d; want 1,1", nGen, nRet)
	}

	if _, err := db.Exec(`DELETE FROM media_assets WHERE id = ?`, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	nGen, nRet = 99, 99
	if err := db.QueryRow(`SELECT COUNT(*) FROM generated_image_details WHERE asset_id = ?`, id).Scan(&nGen); err != nil {
		t.Fatalf("count gen after: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM retrieved_image_details WHERE asset_id = ?`, id).Scan(&nRet); err != nil {
		t.Fatalf("count ret after: %v", err)
	}
	if nGen != 0 || nRet != 0 {
		t.Errorf("post-cascade: %d/%d; want 0,0", nGen, nRet)
	}
}

func TestGetDetails_NoRow_ReturnsNil(t *testing.T) {
	db := openImageDetailsTestDB(t)
	r := NewImagesRepository(db)
	ctx := context.Background()
	id := "img_absent"
	insertMediaAssetRow(t, db, id, "", "")

	g, err := r.GetGeneratedDetails(ctx, id)
	if err != nil {
		t.Fatalf("GetGeneratedDetails err: %v", err)
	}
	if g != nil {
		t.Errorf("GetGeneratedDetails != nil for pre-FASE-4 row")
	}
	rv, err := r.GetRetrievedDetails(ctx, id)
	if err != nil {
		t.Fatalf("GetRetrievedDetails err: %v", err)
	}
	if rv != nil {
		t.Errorf("GetRetrievedDetails != nil for pre-FASE-4 row")
	}
}

func TestSchema_HasFKCascade(t *testing.T) {
	db := openImageDetailsTestDB(t)
	for _, table := range []string{"generated_image_details", "retrieved_image_details"} {
		var nFK int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list(?)`, table).Scan(&nFK); err != nil {
			t.Fatalf("pragma_foreign_key_list(%s): %v", table, err)
		}
		if nFK != 1 {
			t.Errorf("%s: %d FK definitions; want 1", table, nFK)
		}
		rows, err := db.Query(`SELECT "table", "from", "to", "on_delete" FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatalf("pragma_foreign_key_list(%s) detail: %v", table, err)
		}
		for rows.Next() {
			var tbl, from, to, onDelete string
			if err := rows.Scan(&tbl, &from, &to, &onDelete); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if from != "asset_id" {
				t.Errorf("%s: FK.from = %q; want asset_id", table, from)
			}
			if to != "id" {
				t.Errorf("%s: FK.to = %q; want id", table, to)
			}
			if !strings.EqualFold(onDelete, "CASCADE") {
				t.Errorf("%s: on_delete=%q; want CASCADE", table, onDelete)
			}
		}
		rows.Close()
	}
}
