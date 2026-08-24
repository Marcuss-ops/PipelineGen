package sqlite

import "testing"

func TestMigrations_223ArtlistCatalogSchema(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	for table, want := range map[string][]string{
		"artlist_clips": {
			"clip_id", "title", "author", "duration_ms", "canonical_clip_url",
			"thumbnail_url", "tags_json", "categories_json", "description",
			"metadata_json", "first_seen_at", "last_seen_at", "updated_at",
			"active", "downloaded", "drive_file_id", "drive_link", "local_path", "file_hash",
		},
		"artlist_queries": {
			"query_id", "query", "normalized_query", "query_key", "filters_json",
			"provider_sort_type", "provider_total", "provider_total_authoritative",
			"result_count", "first_synced_at", "last_synced_at", "expires_at",
			"sync_status", "last_error", "created_at", "updated_at",
		},
		"artlist_query_clips": {
			"query_id", "clip_id", "provider_rank", "provider_page", "first_seen_at", "last_seen_at",
		},
	} {
		columns := scanColumnNames(t, db, table)
		for _, column := range want {
			if _, ok := columns[column]; !ok {
				t.Errorf("%s is missing column %q", table, column)
			}
		}
	}

	if _, err := db.Exec(`
		INSERT INTO artlist_clips (
			clip_id, title, author, canonical_clip_url, tags_json, categories_json
		) VALUES ('clip-847392', 'Female boxer training', 'Author',
			'https://artlist.io/stock-footage/clip/female-boxer-training/847392',
			'["boxing","training"]', '["sports"]')
	`); err != nil {
		t.Fatalf("insert artlist clip: %v", err)
	}

	var clipCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artlist_clips WHERE clip_id = 'clip-847392'`).Scan(&clipCount); err != nil {
		t.Fatalf("count clip: %v", err)
	}
	if clipCount != 1 {
		t.Fatalf("clip count = %d, want 1", clipCount)
	}

	if _, err := db.Exec(`
		INSERT INTO artlist_queries (
			query, normalized_query, query_key, provider_total, result_count,
			last_synced_at, sync_status
		) VALUES ('female boxer', 'female boxer', 'query-key-female-boxer', 854808, 50,
			datetime('now'), 'succeeded')
	`); err != nil {
		t.Fatalf("insert artlist query: %v", err)
	}

	var queryID int64
	if err := db.QueryRow(`SELECT query_id FROM artlist_queries WHERE query_key = 'query-key-female-boxer'`).Scan(&queryID); err != nil {
		t.Fatalf("read query id: %v", err)
	}

	insertRelation := `
		INSERT INTO artlist_query_clips (query_id, clip_id, provider_rank, provider_page)
		VALUES (?, 'clip-847392', ?, ?)
		ON CONFLICT(query_id, clip_id) DO UPDATE SET
			provider_rank = excluded.provider_rank,
			provider_page = excluded.provider_page,
			last_seen_at = excluded.last_seen_at
	`
	if _, err := db.Exec(insertRelation, queryID, 1, 1); err != nil {
		t.Fatalf("insert query relation: %v", err)
	}
	if _, err := db.Exec(insertRelation, queryID, 4, 1); err != nil {
		t.Fatalf("upsert query relation: %v", err)
	}

	var relationCount, rank int
	if err := db.QueryRow(`
		SELECT COUNT(*), MAX(provider_rank)
		FROM artlist_query_clips
		WHERE query_id = ? AND clip_id = 'clip-847392'
	`, queryID).Scan(&relationCount, &rank); err != nil {
		t.Fatalf("read query relation: %v", err)
	}
	if relationCount != 1 || rank != 4 {
		t.Fatalf("deduplicated relation = count %d rank %d, want count 1 rank 4", relationCount, rank)
	}

	if err := db.QueryRow(`SELECT COUNT(*) FROM artlist_clips WHERE clip_id = 'clip-847392'`).Scan(&clipCount); err != nil {
		t.Fatalf("final clip count: %v", err)
	}
	if clipCount != 1 {
		t.Fatalf("final clip count = %d, want 1", clipCount)
	}

}
