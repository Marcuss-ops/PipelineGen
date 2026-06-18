// Package assetrepo — contract_test.go covers the canonical Repository
// contract. Tests use storage.NewTestDBWithSchema to spin up an isolated
// SQLite with only the tables this repo reads from.
//
// The schema includes the full media_assets column set referenced by
// scanner.go's selectColumns (47 source columns). Column additions are
// safe to drop but column renames will require updating both this file
// AND scanner.go simultaneously.
package assetrepo

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/storage"
)

// minimalSchema is the subset of media_assets columns this repo reads/writes.
// Matches what scanner.go expects in its selectColumns target list.
const minimalSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    clip_page_url TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    tags TEXT NOT NULL DEFAULT '[]',
    search_terms TEXT NOT NULL DEFAULT '[]',
    search_text TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'ready',
    deleted_at TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    embedding_json TEXT NOT NULL DEFAULT '',
    visual_embedding TEXT NOT NULL DEFAULT '',
    transcript_embedding TEXT NOT NULL DEFAULT '',
    visual_embedding_json TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    parent_folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    is_folder INTEGER NOT NULL DEFAULT 0,
    scene_type TEXT NOT NULL DEFAULT '',
    quality_score REAL NOT NULL DEFAULT 0.0,
    reuse_count INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT NOT NULL DEFAULT '',
    usable_for TEXT NOT NULL DEFAULT '[]',
    avoid_for TEXT NOT NULL DEFAULT '[]',
    phash TEXT NOT NULL DEFAULT '',
    child_count INTEGER NOT NULL DEFAULT 0,
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id TEXT PRIMARY KEY,
    aggregate_id TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT '',
    published_at TEXT
);
`

// ── Helpers ────────────────────────────────────────────────────────────

func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, minimalSchema)
	return New(db, nil)
}

func mustUpsert(t *testing.T, r *Repository, m *asset.MediaAsset) {
	t.Helper()
	if err := r.Upsert(context.Background(), m); err != nil {
		t.Fatalf("Upsert(%s) failed: %v", m.ID, err)
	}
}

// ── Contract tests ─────────────────────────────────────────────────────

func TestUpsertGetRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	m := &asset.MediaAsset{
		ID:            "asset_rt_001",
		Source:        "youtube",
		Name:          "Round Trip Test",
		Filename:      "rt_001.mp4",
		MediaType:     "video",
		Category:      "demo",
		SourceURL:     "https://youtube.com/watch?v=rt_001",
		DurationMs:    12345,
		Tags:          []string{"a", "b"},
		QualityScore:  0.92,
		LifecycleState: asset.StateReady,
	}
	mustUpsert(t, r, m)

	got, err := r.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", m.ID, err)
	}
	if got == nil {
		t.Fatalf("Get(%s): nil result", m.ID)
	}
	if got.Source != m.Source || got.DurationMs != m.DurationMs || got.QualityScore != m.QualityScore {
		t.Errorf("round-trip mismatch:\n want=%+v\n  got=%+v", m, got)
	}
}

func TestGetNotFoundReturnsNil(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("expected nil err for missing asset, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing asset, got %+v", got)
	}
}

func TestGetInvalidID(t *testing.T) {
	r := newTestRepo(t)
	_, err := r.Get(context.Background(), "")
	if err != asset.ErrInvalidID {
		t.Errorf("expected ErrInvalidID for empty id, got %v", err)
	}
}

func TestListBySource(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustUpsert(t, r, &asset.MediaAsset{ID: "a1", Source: "youtube", Name: "A1", LifecycleState: asset.StateReady})
	mustUpsert(t, r, &asset.MediaAsset{ID: "a2", Source: "artlist", Name: "A2", LifecycleState: asset.StateReady})
	mustUpsert(t, r, &asset.MediaAsset{ID: "a3", Source: "youtube", Name: "A3", LifecycleState: asset.StateReady})

	out, err := r.List(ctx, asset.Filter{Source: "youtube"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("expected 2 youtube assets, got %d", len(out))
	}
	for _, m := range out {
		if m.Source != "youtube" {
			t.Errorf("non-youtube in result: %s", m.Source)
		}
	}
}

func TestCountBySource(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustUpsert(t, r, &asset.MediaAsset{ID: "a1", Source: "youtube", Name: "A1", LifecycleState: asset.StateReady})
	mustUpsert(t, r, &asset.MediaAsset{ID: "a2", Source: "youtube", Name: "A2", LifecycleState: asset.StateReady})

	n, err := r.Count(ctx, asset.Filter{Source: "youtube"})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2, got %d", n)
	}
}

func TestSoftDeleteRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustUpsert(t, r, &asset.MediaAsset{ID: "del_001", Source: "youtube", LifecycleState: asset.StateReady})

	if err := r.SoftDelete(ctx, "del_001"); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	// Get should now return ErrSoftDeleted, distinguishing delete from missing.
	_, err := r.Get(ctx, "del_001")
	if err != asset.ErrSoftDeleted {
		t.Errorf("expected ErrSoftDeleted, got %v", err)
	}

	// Restore should bring it back.
	if err := r.Restore(ctx, "del_001"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := r.Get(ctx, "del_001")
	if err != nil {
		t.Fatalf("Get after Restore: %v", err)
	}
	if got == nil {
		t.Errorf("expected asset after restore")
	}
}

func TestOutboxEmitOnUpsert(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustUpsert(t, r, &asset.MediaAsset{ID: "tx_001", Source: "youtube", LifecycleState: asset.StateReady})

	var n int
	row := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = 'tx_001' AND event_type = 'asset.upserted'`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("outbox count query: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 outbox row, got %d", n)
	}
}

func TestOutboxNotEmittedOnRollback(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	// Use WithTx to provoke a rollback and assert no outbox row is written.
	err := repo.WithTx(ctx, func(tx *Tx) error {
		m := &asset.MediaAsset{ID: "rb_001", Source: "youtube", LifecycleState: asset.StateReady}
		// Emit outbox event, but don't commit.
		if err := tx.OnCommit(ctx, m.ID, "asset.upserted", m); err != nil {
			return err
		}
		return errors.New("simulated failure")
	})
	if err == nil {
		t.Fatalf("expected error from WithTx")
	}

	var n int
	row := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = 'rb_001'`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("outbox count query: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 outbox rows after rollback, got %d", n)
	}
}

func TestNoMetadataJSONExtractForCanonicalFields(t *testing.T) {
	// The contract guarantee: canonical fields come from real columns,
	// NEVER from json_extract(metadata_json, '$.field').
	r := newTestRepo(t)
	ctx := context.Background()
	m := &asset.MediaAsset{
		ID:             "no_extract_001",
		Source:         "youtube",
		Name:           "NoExtract",
		Category:       "explicit_category",
		SearchText:     "explicit_search_text",
		LifecycleState: asset.StateReady,
		Metadata:       map[string]any{"category": "wrong_via_metadata"},
	}
	mustUpsert(t, r, m)

	got, err := r.Get(ctx, "no_extract_001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Category != "explicit_category" {
		t.Errorf("expected Category='explicit_category' from real column, got %q (should not pull from metadata_json)", got.Category)
	}
	if got.SearchText != "explicit_search_text" {
		t.Errorf("expected SearchText='explicit_search_text' from real column, got %q", got.SearchText)
	}
}
