package topicsourcecache

import (
	"context"
	"testing"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// researchCacheSchema mirrors the production research_cache columns that
// DeleteResearchCache and the json_extract ranking filter touch.
const researchCacheSchema = `
	CREATE TABLE research_cache (
		key TEXT PRIMARY KEY,
		topic TEXT NOT NULL,
		language TEXT NOT NULL,
		max_steps INTEGER NOT NULL,
		source_text TEXT NOT NULL,
		source_text_hash TEXT NOT NULL DEFAULT '',
		research_report_json TEXT NOT NULL DEFAULT '',
		sources_count INTEGER NOT NULL DEFAULT 0,
		claims_verified INTEGER NOT NULL DEFAULT 0,
		claims_rejected INTEGER NOT NULL DEFAULT 0,
		search_query_count INTEGER NOT NULL DEFAULT 0,
		pages_fetched INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		last_used TEXT NOT NULL DEFAULT (datetime('now')),
		concept_id TEXT,
		topic_fingerprint TEXT,
		source_fingerprint TEXT,
		resolver_version TEXT,
		research_version TEXT,
		hit_count INTEGER NOT NULL DEFAULT 0,
		expires_at DATETIME,
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
`

func newTestRepository(t *testing.T) *Repository {
	t.Helper()
	db := drive.NewTestDBWithSchema(t, researchCacheSchema)
	t.Cleanup(func() { db.Close() })
	return NewRepository(db)
}

func seedRow(t *testing.T, repo *Repository, key, topic, resolverVersion, rankingMetric string) {
	t.Helper()
	reportJSON := `{}`
	if rankingMetric != "" {
		reportJSON = `{"ranking":{"requested_metric":"` + rankingMetric + `"}}`
	}
	if _, err := repo.db.ExecContext(context.Background(), `
		INSERT INTO research_cache (key, topic, language, max_steps, source_text, research_report_json, resolver_version)
		VALUES (?, ?, 'en', 1, 'source', ?, ?)`,
		key, topic, reportJSON, resolverVersion,
	); err != nil {
		t.Fatalf("seed row %s: %v", key, err)
	}
}

func countRows(t *testing.T, repo *Repository) int {
	t.Helper()
	var n int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM research_cache`).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestDeleteResearchCache_AggregateScopeWithMetricFilter(t *testing.T) {
	repo := newTestRepository(t)
	seedRow(t, repo, "agg-networth", "The richest boxers", "webresearch-fanout", "estimated_net_worth")
	seedRow(t, repo, "agg-earnings", "The richest boxers", "webresearch-fanout", "career_earnings")
	seedRow(t, repo, "cand-canelo", "Canelo Álvarez", "webresearch", "")

	deleted, err := repo.DeleteResearchCache(context.Background(), "aggregate", "The richest boxers", "estimated_net_worth")
	if err != nil {
		t.Fatalf("DeleteResearchCache: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (only the estimated_net_worth aggregate)", deleted)
	}
	if got := countRows(t, repo); got != 2 {
		t.Fatalf("rows remaining = %d, want 2", got)
	}
}

func TestDeleteResearchCache_AggregateScopeWithoutMetricDeletesAllMetrics(t *testing.T) {
	repo := newTestRepository(t)
	seedRow(t, repo, "agg-networth", "The richest boxers", "webresearch-fanout", "estimated_net_worth")
	seedRow(t, repo, "agg-earnings", "The richest boxers", "webresearch-fanout", "career_earnings")
	seedRow(t, repo, "cand-canelo", "Canelo Álvarez", "webresearch", "")

	deleted, err := repo.DeleteResearchCache(context.Background(), "aggregate", "The richest boxers", "")
	if err != nil {
		t.Fatalf("DeleteResearchCache: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2 (both aggregate rows)", deleted)
	}
	if got := countRows(t, repo); got != 1 {
		t.Fatalf("rows remaining = %d, want 1 (only the candidate)", got)
	}
}

func TestDeleteResearchCache_CandidateScope(t *testing.T) {
	repo := newTestRepository(t)
	seedRow(t, repo, "cand-canelo", "Canelo Álvarez", "webresearch", "")
	seedRow(t, repo, "cand-floyd", "Floyd Mayweather Jr.", "webresearch", "")
	seedRow(t, repo, "agg", "The richest boxers", "webresearch-fanout", "estimated_net_worth")

	deleted, err := repo.DeleteResearchCache(context.Background(), "candidate", "Canelo Álvarez", "ignored")
	if err != nil {
		t.Fatalf("DeleteResearchCache: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (only the Canelo candidate)", deleted)
	}
	if got := countRows(t, repo); got != 2 {
		t.Fatalf("rows remaining = %d, want 2", got)
	}
}

func TestDeleteResearchCache_NoMatch(t *testing.T) {
	repo := newTestRepository(t)
	seedRow(t, repo, "cand-canelo", "Canelo Álvarez", "webresearch", "")

	deleted, err := repo.DeleteResearchCache(context.Background(), "candidate", "Nobody", "")
	if err != nil {
		t.Fatalf("DeleteResearchCache: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
}

func TestDeleteResearchCache_Validation(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	if _, err := repo.DeleteResearchCache(ctx, "bogus", "topic", ""); err == nil {
		t.Fatal("unsupported scope: want error, got nil")
	}
	if _, err := repo.DeleteResearchCache(ctx, "aggregate", "", ""); err == nil {
		t.Fatal("empty topic: want error, got nil")
	}
}

func TestDeleteResearchCache_NilRepository(t *testing.T) {
	var repo *Repository
	deleted, err := repo.DeleteResearchCache(context.Background(), "aggregate", "topic", "")
	if err != nil {
		t.Fatalf("nil repo should be a no-op, got error %v", err)
	}
	if deleted != 0 {
		t.Fatalf("nil repo deleted = %d, want 0", deleted)
	}
}
