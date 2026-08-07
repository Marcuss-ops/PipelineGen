package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ── parseBackfillArgs ──────────────────────────────────────────────────
//
// Positive tests pin the happy-path flag broadcast; negative tests pin
// the error contract for malformed args. The Source field is intentionally
// recorded verbatim (no enum validation) because the backfill SQL filter
// silently degrades an unrecognized value to zero rows — operators see
// the gap in the dry-run counter rather than an error, which is the
// "be liberal in what you accept" stance documented in the cmd header.
// SourceRecordedVerbatim pins that behavior so a future enum-validation
// patch breaks loudly instead of regressing quietly.

func TestParseBackfillArgs_Defaults(t *testing.T) {
	t.Parallel()

	deps, err := parseBackfillArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deps.Apply {
		t.Fatalf("Apply=true; default false expected")
	}
	if deps.JSON {
		t.Fatalf("JSON=true; default false expected")
	}
	if deps.Limit != 0 {
		t.Fatalf("Limit=%d; default 0 expected", deps.Limit)
	}
	if deps.BatchSize != defaultBackfillBatchSize {
		t.Fatalf("BatchSize=%d; default %d expected", deps.BatchSize, defaultBackfillBatchSize)
	}
	if deps.Source != "" {
		t.Fatalf("Source=%q; default empty expected", deps.Source)
	}
}

func TestParseBackfillArgs_AllFlags(t *testing.T) {
	t.Parallel()

	deps, err := parseBackfillArgs([]string{
		"--apply", "--json",
		"--limit=100", "--batch=200",
		"--source=artlist",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deps.Apply {
		t.Fatalf("Apply=false; want true")
	}
	if !deps.JSON {
		t.Fatalf("JSON=false; want true")
	}
	if deps.Limit != 100 {
		t.Fatalf("Limit=%d; want 100", deps.Limit)
	}
	if deps.BatchSize != 200 {
		t.Fatalf("BatchSize=%d; want 200", deps.BatchSize)
	}
	if deps.Source != "artlist" {
		t.Fatalf("Source=%q; want artlist", deps.Source)
	}
}

func TestParseBackfillArgs_SourceRecordedVerbatim(t *testing.T) {
	t.Parallel()

	// --source=garbage is intentionally NOT validated against the
	// {artlist, youtube, stock, image} enum. The SQL filter will simply
	// return zero rows; the operator sees the gap in the dry-run counter.
	// This test pins that behavior so a future enum-validation patch
	// breaks loudly instead of silently.
	deps, err := parseBackfillArgs([]string{"--source=garbage"})
	if err != nil {
		t.Fatalf("unexpected error on verbatim source; got %v", err)
	}
	if deps.Source != "garbage" {
		t.Fatalf("verbatim pass-through failed; got %q want %q", deps.Source, "garbage")
	}
}

func TestParseBackfillArgs_NegativeLimit(t *testing.T) {
	t.Parallel()

	_, err := parseBackfillArgs([]string{"--limit=-1"})
	if err == nil {
		t.Fatalf("expected error on --limit=-1")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error text should mention non-negative; got %v", err)
	}
}

func TestParseBackfillArgs_NonNumericLimit(t *testing.T) {
	t.Parallel()

	_, err := parseBackfillArgs([]string{"--limit=abc"})
	if err == nil {
		t.Fatalf("expected error on --limit=abc")
	}
	if !strings.Contains(err.Error(), "integer") {
		t.Fatalf("error text should mention integer; got %v", err)
	}
}

func TestParseBackfillArgs_NegativeBatch(t *testing.T) {
	t.Parallel()

	_, err := parseBackfillArgs([]string{"--batch=-100"})
	if err == nil {
		t.Fatalf("expected error on --batch=-100")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error text should mention non-negative; got %v", err)
	}
}

// ── applyBackfillBatch (RFC3339 updated_at regression) ─────────────────
//
// TestApplyBackfillBatch_WritesRFC3339UpdatedAt pins the canonical
// timestamp contract: the backfill must write updated_at as RFC3339
// (`YYYY-MM-DDTHH:MM:SSZ`), NOT SQLite's bare `datetime('now')` format
// (`YYYY-MM-DD HH:MM:SS`). The bare format cannot be parsed by
// timeutil.ParseRFC3339 and fails-closed the deletion-reconciler scan
// (stuck_row_scanner.go), blocking the whole deletion chain for 15-minute
// ticks. Regression for the 2026-08-01 `yt_0vnOfawuQF4_*` rows.
func TestApplyBackfillBatch_WritesRFC3339UpdatedAt(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		source TEXT,
		name TEXT,
		filename TEXT,
		category TEXT,
		tags TEXT,
		search_text TEXT,
		metadata_json TEXT,
		search_terms TEXT,
		updated_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO media_assets (id, source, name, filename, category, tags, search_text, metadata_json, search_terms, updated_at)
		VALUES ('a1', 'youtube', 'clip', 'clip.mp4', 'fight', '["x"]', 'text', '{}', '[]', '')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	batch := []pendingMediaAssetRow{{id: "a1", source: "youtube", name: "clip", filename: "clip.mp4", category: "fight", tagsJSON: `["x"]`, searchText: "text", metadataJSON: "{}"}}
	updated, skipped, err := applyBackfillBatch(context.Background(), db, batch, zap.NewNop())
	if err != nil {
		t.Fatalf("applyBackfillBatch: %v", err)
	}
	if updated != 1 || skipped != 0 {
		t.Fatalf("updated=%d skipped=%d; want 1/0", updated, skipped)
	}

	var updatedAt string
	if err := db.QueryRow(`SELECT updated_at FROM media_assets WHERE id='a1'`).Scan(&updatedAt); err != nil {
		t.Fatalf("select updated_at: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		t.Fatalf("updated_at %q is not RFC3339 (deletion-reconciler will fail-closed): %v", updatedAt, err)
	}
	if parsed.IsZero() {
		t.Fatalf("updated_at parsed to zero time")
	}
	if strings.Contains(updatedAt, " ") {
		t.Fatalf("updated_at %q uses the legacy space-separated datetime('now') format", updatedAt)
	}
}

func TestParseBackfillArgs_ZeroBatchFallsBackToDefault(t *testing.T) {
	t.Parallel()

	// --batch=0 is caught by the post-loop guard in parseBackfillArgs;
	// the parser silently coerces 0 back to defaultBackfillBatchSize
	// rather than rejecting the arg. Operators who pass --batch=0 usually
	// mean "use the server default" — accept it.
	deps, err := parseBackfillArgs([]string{"--batch=0"})
	if err != nil {
		t.Fatalf("unexpected error on --batch=0; got %v", err)
	}
	if deps.BatchSize != defaultBackfillBatchSize {
		t.Fatalf("BatchSize=%d; default %d expected on zero", deps.BatchSize, defaultBackfillBatchSize)
	}
}
