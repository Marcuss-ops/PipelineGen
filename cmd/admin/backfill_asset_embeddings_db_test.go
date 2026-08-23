package main

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/indexing/backfill"
	_ "github.com/mattn/go-sqlite3"
)

// newEmbeddingCandidateDB builds an in-memory media_assets table with the
// canonical taxonomy + embedding columns the fetcher reads.
func newEmbeddingCandidateDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			deleted_at TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			embedding_json TEXT NOT NULL DEFAULT '',
			transcript_embedding TEXT NOT NULL DEFAULT '',
			visual_embedding TEXT NOT NULL DEFAULT '',
			audio_embedding TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
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
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedEmbeddingCandidate(t *testing.T, db *sql.DB, id, source, mediaType, assetKind, namespace, sourceType, emb, transcript, visual, audio string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO media_assets (id, source, media_type, asset_kind, namespace, source_type, embedding_json, transcript_embedding, visual_embedding, audio_embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, source, mediaType, assetKind, namespace, sourceType, emb, transcript, visual, audio); err != nil {
		t.Fatal(err)
	}
}

// TestFetchEmbeddingCandidates_EligibleOnly pins the eligibility boundary:
// only taxonomy-eligible rows (video/image with valid kind + namespace +
// source_type) are candidates; audio and legacy empty-taxonomy rows are
// never returned.
func TestFetchEmbeddingCandidates_EligibleOnly(t *testing.T) {
	db := newEmbeddingCandidateDB(t)
	// Eligible video missing text embedding → candidate.
	seedEmbeddingCandidate(t, db, "a1", "stock", "video", "stock_video", "stock", "stock", "", "[1]", "[1]", "[1]")
	// Eligible video fully embedded → excluded in only-missing mode.
	seedEmbeddingCandidate(t, db, "a2", "stock", "video", "stock_video", "stock", "stock", "[1]", "[1]", "[1]", "[1]")
	// Audio (REGISTERED but not SEARCHABLE) → never a candidate.
	seedEmbeddingCandidate(t, db, "a3", "artlist", "audio", "sfx", "audio", "artlist", "", "", "", "")
	// Legacy row with empty taxonomy → never a candidate.
	seedEmbeddingCandidate(t, db, "a4", "youtube", "clip", "", "", "", "", "", "", "")

	cands, err := fetchEmbeddingCandidates(context.Background(), db, backfill.Deps{OnlyMissing: true}, nil)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates: %v", err)
	}
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	if want := []string{"a1"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("candidates = %v, want %v (only the eligible row missing an embedding)", ids, want)
	}
}

// TestFetchEmbeddingCandidates_AllModeIncludesComplete pins that --all
// (OnlyMissing=false) also returns fully-embedded eligible rows.
func TestFetchEmbeddingCandidates_AllModeIncludesComplete(t *testing.T) {
	db := newEmbeddingCandidateDB(t)
	seedEmbeddingCandidate(t, db, "a1", "stock", "video", "stock_video", "stock", "stock", "[1]", "[1]", "[1]", "[1]")
	seedEmbeddingCandidate(t, db, "a2", "stock", "video", "stock_video", "stock", "stock", "", "", "", "")

	cands, err := fetchEmbeddingCandidates(context.Background(), db, backfill.Deps{OnlyMissing: false}, nil)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates: %v", err)
	}
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	if want := []string{"a1", "a2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("candidates = %v, want %v", ids, want)
	}
}

// TestFetchEmbeddingCandidates_SourceFilterAndResume pins the source filter
// and the resume anchor (id > last processed).
func TestFetchEmbeddingCandidates_SourceFilterAndResume(t *testing.T) {
	db := newEmbeddingCandidateDB(t)
	seedEmbeddingCandidate(t, db, "b1", "stock", "video", "stock_video", "stock", "stock", "", "", "", "")
	seedEmbeddingCandidate(t, db, "b2", "youtube", "video", "clip", "stock", "youtube", "", "", "", "")

	// Source filter: only stock.
	cands, err := fetchEmbeddingCandidates(context.Background(), db, backfill.Deps{OnlyMissing: true, Source: "stock"}, nil)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].ID != "b1" {
		t.Fatalf("source-filtered candidates = %+v, want [b1]", cands)
	}

	// Resume: continue after b1 → only b2.
	cp := &backfill.Checkpoint{LastProcessedID: "b1"}
	cands, err = fetchEmbeddingCandidates(context.Background(), db, backfill.Deps{OnlyMissing: true, Resume: true}, cp)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates resume: %v", err)
	}
	if len(cands) != 1 || cands[0].ID != "b2" {
		t.Fatalf("resume candidates = %+v, want [b2]", cands)
	}
}
