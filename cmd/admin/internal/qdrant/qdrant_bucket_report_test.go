package qdrant

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestComputeQdrantBuckets_ClassifiesFourBuckets pins the pure 4-bucket
// classification: healthy, missing_in_qdrant, indexed_but_ineligible,
// orphan_in_qdrant.
func TestComputeQdrantBuckets_ClassifiesFourBuckets(t *testing.T) {
	rows := []bucketAssetRow{
		{ID: "a1", Eligible: true, IndexState: "INDEXED"},     // A healthy
		{ID: "a2", Eligible: true, IndexState: "EMBEDDED"},    // B eligible, not in Qdrant
		{ID: "a3", Eligible: false, IndexState: "INDEXED"},    // C INDEXED but ineligible
		{ID: "a4", Eligible: false, IndexState: "DISCOVERED"}, // neither
		{ID: "a5", Eligible: true, IndexState: "INDEXED"},     // A healthy
	}
	qdrant := map[string]struct{}{
		"a1": {}, "a5": {}, "orphan-1": {},
	}

	got := computeQdrantBuckets(rows, qdrant)

	if got.TotalAssets != 5 {
		t.Fatalf("TotalAssets = %d, want 5", got.TotalAssets)
	}
	if got.IndexedCount != 3 {
		t.Fatalf("IndexedCount = %d, want 3", got.IndexedCount)
	}
	if got.EligibleSQLite != 3 {
		t.Fatalf("EligibleSQLite = %d, want 3", got.EligibleSQLite)
	}
	if got.Healthy != 2 {
		t.Fatalf("Healthy = %d, want 2", got.Healthy)
	}
	if got.MissingInQdrant != 1 {
		t.Fatalf("MissingInQdrant = %d, want 1", got.MissingInQdrant)
	}
	if got.IndexedButIneligible != 1 {
		t.Fatalf("IndexedButIneligible = %d, want 1", got.IndexedButIneligible)
	}
	if got.OrphanInQdrant != 1 {
		t.Fatalf("OrphanInQdrant = %d, want 1", got.OrphanInQdrant)
	}
	if !reflect.DeepEqual(got.MissingIDs, []string{"a2"}) {
		t.Fatalf("MissingIDs = %v, want [a2]", got.MissingIDs)
	}
	if !reflect.DeepEqual(got.IndexedButIneligibleIDs, []string{"a3"}) {
		t.Fatalf("IndexedButIneligibleIDs = %v, want [a3]", got.IndexedButIneligibleIDs)
	}
	if !reflect.DeepEqual(got.OrphanIDs, []string{"orphan-1"}) {
		t.Fatalf("OrphanIDs = %v, want [orphan-1]", got.OrphanIDs)
	}
}

// TestComputeQdrantBuckets_EmptyQdrant pins that a nil/empty Qdrant set is
// safe and classifies every eligible row as missing.
func TestComputeQdrantBuckets_EmptyQdrant(t *testing.T) {
	rows := []bucketAssetRow{
		{ID: "a1", Eligible: true, IndexState: "INDEXED"},
		{ID: "a2", Eligible: false, IndexState: "INDEXED"},
	}
	got := computeQdrantBuckets(rows, nil)
	if got.MissingInQdrant != 1 || got.Healthy != 0 || got.OrphanInQdrant != 0 {
		t.Fatalf("empty qdrant classification wrong: %+v", got)
	}
}

// TestBucketAssetQuery_UsesCanonicalEligibility pins that the report SQL
// embeds the canonical SearchIndexEligibilitySQL boundary: a complete
// video/clip row is eligible, a legacy row with empty taxonomy is not, and
// a folder row is excluded entirely.
func TestBucketAssetQuery_UsesCanonicalEligibility(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			media_type TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			deleted_at TEXT NOT NULL DEFAULT '',
			embedding_json TEXT NOT NULL DEFAULT '',
			index_state TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
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
	seed := []string{
		// complete video/clip → eligible
		`('asset-1', 'video', 'clip', 'stock', 'youtube', '', '[1.0]', 'INDEXED')`,
		// legacy row with empty taxonomy → NOT eligible despite INDEXED
		`('asset-2', 'clip', '', '', '', '', '[1.0]', 'INDEXED')`,
		// audio → NOT eligible
		`('asset-3', 'audio', 'sfx', 'audio', 'artlist', '', '[1.0]', 'EMBEDDED')`,
		// folder → excluded entirely
		`('folder-1', 'folder', '', '', '', '', '', '')`,
	}
	for _, row := range seed {
		if _, err := db.Exec(`INSERT INTO media_assets (id, media_type, asset_kind, namespace, source_type, deleted_at, embedding_json, index_state) VALUES ` + row); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := db.QueryContext(context.Background(), bucketAssetQuery)
	if err != nil {
		t.Fatalf("bucket query: %v", err)
	}
	defer rows.Close()
	var got []bucketAssetRow
	for rows.Next() {
		var r bucketAssetRow
		var eligible int
		if err := rows.Scan(&r.ID, &eligible, &r.IndexState); err != nil {
			t.Fatal(err)
		}
		r.Eligible = eligible == 1
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3 (folder excluded)", len(got))
	}
	byID := map[string]bucketAssetRow{}
	for _, r := range got {
		byID[r.ID] = r
	}
	if !byID["asset-1"].Eligible {
		t.Fatalf("asset-1 must be eligible, got %+v", byID["asset-1"])
	}
	if byID["asset-2"].Eligible {
		t.Fatalf("asset-2 (legacy empty taxonomy) must NOT be eligible, got %+v", byID["asset-2"])
	}
	if byID["asset-3"].Eligible {
		t.Fatalf("asset-3 (audio) must NOT be eligible, got %+v", byID["asset-3"])
	}
	if _, ok := byID["folder-1"]; ok {
		t.Fatal("folder-1 must be excluded from the report")
	}
}
