package main

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestBackfillSearchText_ComposesOnlyMissing pins that the search_text
// repair composes canonical search text from existing fields, never
// overwrites a populated search_text, and only touches the target sources.
func TestBackfillSearchText_ComposesOnlyMissing(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]',
			source_url TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			search_text TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE asset_text_tracks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL,
			text_kind TEXT NOT NULL DEFAULT '',
			is_current INTEGER NOT NULL DEFAULT 0,
			text TEXT NOT NULL DEFAULT ''
		);`); err != nil {
		t.Fatal(err)
	}
	seed := []string{
		// stock row with empty search_text → should be composed
		`('stock-1', 'stock', 'Boxing training', 'sports', '["boxing","gym"]', 'https://x/1', '{"description":"heavy bag drill"}', '', '')`,
		// youtube row with empty search_text + summary → should be composed
		`('yt-1', 'youtube', 'Mike Tyson interview', 'interview', '[]', 'https://y/2', '{"summary":"round one highlights"}', '', '')`,
		// row with populated search_text → never overwritten
		`('done-1', 'stock', 'Already done', 'sports', '[]', '', '{}', 'existing search text', '')`,
		// row from a non-target source → untouched
		`('vo-1', 'voiceover', 'Voiceover', 'audio', '[]', '', '{}', '', '')`,
	}
	for _, row := range seed {
		if _, err := db.Exec(`INSERT INTO media_assets (id, source, name, category, tags, source_url, metadata_json, search_text, updated_at) VALUES ` + row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO asset_text_tracks (asset_id, text_kind, is_current, text) VALUES ('stock-1', 'transcript', 1, 'left jab right cross')`); err != nil {
		t.Fatal(err)
	}

	matched, updated, err := backfillSearchText(context.Background(), db, []string{"stock", "youtube"}, 0, true)
	if err != nil {
		t.Fatalf("backfillSearchText: %v", err)
	}
	if matched != 2 {
		t.Fatalf("matched = %d, want 2", matched)
	}
	if updated != 2 {
		t.Fatalf("updated = %d, want 2", updated)
	}

	var stockText, ytText, doneText, voText string
	_ = db.QueryRow(`SELECT search_text FROM media_assets WHERE id='stock-1'`).Scan(&stockText)
	_ = db.QueryRow(`SELECT search_text FROM media_assets WHERE id='yt-1'`).Scan(&ytText)
	_ = db.QueryRow(`SELECT search_text FROM media_assets WHERE id='done-1'`).Scan(&doneText)
	_ = db.QueryRow(`SELECT search_text FROM media_assets WHERE id='vo-1'`).Scan(&voText)

	if !strings.Contains(stockText, "Boxing training") {
		t.Fatalf("stock search_text = %q, want composed text containing the title", stockText)
	}
	if !strings.Contains(stockText, "boxing") || !strings.Contains(stockText, "gym") {
		t.Fatalf("stock search_text = %q, want tags included", stockText)
	}
	if !strings.Contains(ytText, "Mike Tyson interview") || !strings.Contains(ytText, "round one highlights") {
		t.Fatalf("youtube search_text = %q, want title + summary", ytText)
	}
	if doneText != "existing search text" {
		t.Fatalf("populated search_text was overwritten: %q", doneText)
	}
	if voText != "" {
		t.Fatalf("non-target source must be untouched, got %q", voText)
	}
}

// TestBackfillSearchText_DryRunCountsOnly pins that dry-run counts the
// candidates but writes nothing.
func TestBackfillSearchText_DryRunCountsOnly(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]',
			source_url TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			search_text TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE asset_text_tracks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL,
			text_kind TEXT NOT NULL DEFAULT '',
			is_current INTEGER NOT NULL DEFAULT 0,
			text TEXT NOT NULL DEFAULT ''
		);`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media_assets (id, source, name, search_text) VALUES ('stock-1', 'stock', 'Boxing', '')`); err != nil {
		t.Fatal(err)
	}

	matched, updated, err := backfillSearchText(context.Background(), db, []string{"stock"}, 0, false)
	if err != nil {
		t.Fatalf("backfillSearchText dry-run: %v", err)
	}
	if matched != 1 || updated != 0 {
		t.Fatalf("dry-run matched/updated = %d/%d, want 1/0", matched, updated)
	}
	var text string
	_ = db.QueryRow(`SELECT search_text FROM media_assets WHERE id='stock-1'`).Scan(&text)
	if text != "" {
		t.Fatalf("dry-run must not write search_text, got %q", text)
	}
}

// TestParseRepairStockMetadataArgs pins default sources + --source override.
func TestParseRepairStockMetadataArgs(t *testing.T) {
	deps, err := parseRepairStockMetadataArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deps.Sources, defaultRepairSources) {
		t.Fatalf("default sources = %v, want %v", deps.Sources, defaultRepairSources)
	}
	if deps.Apply || deps.SkipTaxonomy || deps.SkipSearchText || deps.SkipEmbeddings {
		t.Fatalf("defaults must be dry-run with all phases enabled: %+v", deps)
	}

	deps, err = parseRepairStockMetadataArgs([]string{"--apply", "--source=stock", "--limit=50", "--skip-embeddings"})
	if err != nil {
		t.Fatal(err)
	}
	if !deps.Apply || !deps.SkipEmbeddings {
		t.Fatalf("apply/skip-embeddings not parsed: %+v", deps)
	}
	if !reflect.DeepEqual(deps.Sources, []string{"stock"}) {
		t.Fatalf("sources = %v, want [stock]", deps.Sources)
	}
	if deps.Limit != 50 {
		t.Fatalf("limit = %d, want 50", deps.Limit)
	}
}

// TestTruncateSearchTextBytes pins the byte-bounded, word-boundary truncation.
func TestTruncateSearchTextBytes(t *testing.T) {
	short := "hello world"
	if got := truncateSearchTextBytes(short, 1024); got != short {
		t.Fatalf("short text must pass through unchanged, got %q", got)
	}
	long := strings.Repeat("word ", 300) // 1500 bytes, spaces at every 5th byte
	got := truncateSearchTextBytes(long, 1024)
	if len(got) > 1024 {
		t.Fatalf("truncated length = %d, want <= 1024", len(got))
	}
	if !strings.HasSuffix(got, "word") {
		t.Fatalf("truncation must end at a word boundary, got %q", got[len(got)-10:])
	}
}
