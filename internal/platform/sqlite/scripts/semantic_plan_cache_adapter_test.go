package scripts

import (
	"context"
	"database/sql"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	_ "github.com/mattn/go-sqlite3"
)

const semanticPlanCacheTestSchema = `CREATE TABLE semantic_plan_cache (
	cache_key TEXT PRIMARY KEY,
	payload_json TEXT NOT NULL,
	provider TEXT NOT NULL DEFAULT '',
	provider_lane TEXT NOT NULL DEFAULT '',
	stored_at TEXT NOT NULL,
	expires_at TEXT NOT NULL)`

func openSemanticPlanCacheDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(semanticPlanCacheTestSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func cachedAnswer(provider string, hits ...scriptgen.SemanticAssetHit) scriptgen.SemanticAssetSearchResponse {
	return scriptgen.SemanticAssetSearchResponse{Provider: provider, Hits: hits}
}

func testHit(id, provider string) scriptgen.SemanticAssetHit {
	return scriptgen.SemanticAssetHit{AssetID: id, URL: "https://example.test/" + id, SHA256: id, Provider: provider, MediaType: "image/jpeg"}
}

// A durable answer must be readable by a NEW adapter over the same database:
// that is the whole point of moving the plan cache out of process memory. The
// DriveLink is a provider locator, not an identity, so it must not reappear
// from the payload.
func TestSQLiteSemanticPlanCacheSurvivesRestart(t *testing.T) {
	db := openSemanticPlanCacheDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	writer := NewSQLiteSemanticPlanCacheAdapter(db, 2*time.Hour, nil)
	writer.now = func() time.Time { return now }
	hit := testHit("asset-1", "wikimedia_commons")
	hit.DriveLink = "https://drive.example/file/1"
	writer.Put(ctx, "alberto stasi\x00person\x00it", scriptgen.SemanticSearchCacheEntry{
		Response: cachedAnswer("wikimedia_commons", hit), StoredAt: now,
		Provider: "wikimedia_commons", ProviderLane: "wikimedia_commons",
	})

	reader := NewSQLiteSemanticPlanCacheAdapter(db, 2*time.Hour, nil)
	reader.now = func() time.Time { return now.Add(time.Hour) }
	entry, ok := reader.Get(ctx, "alberto stasi\x00person\x00it")
	if !ok {
		t.Fatal("warm answer was not readable by a fresh adapter")
	}
	if entry.StoredAt != now {
		t.Fatalf("stored_at = %s, want %s", entry.StoredAt, now)
	}
	if entry.Provider != "wikimedia_commons" || entry.ProviderLane != "wikimedia_commons" {
		t.Fatalf("provenance = %q/%q, want the answering provider and lane", entry.Provider, entry.ProviderLane)
	}
	if len(entry.Response.Hits) != 1 || entry.Response.Hits[0].AssetID != "asset-1" || entry.Response.Hits[0].SHA256 != "asset-1" {
		t.Fatalf("hits = %+v, want the certified asset restored", entry.Response.Hits)
	}
	if entry.Response.Hits[0].DriveLink != "" {
		t.Fatalf("DriveLink = %q, want it excluded from the durable payload", entry.Response.Hits[0].DriveLink)
	}
}

func TestSQLiteSemanticPlanCacheExpiresAfterTTL(t *testing.T) {
	db := openSemanticPlanCacheDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	cache := NewSQLiteSemanticPlanCacheAdapter(db, time.Hour, nil)
	cache.now = func() time.Time { return now }
	cache.Put(ctx, "key", scriptgen.SemanticSearchCacheEntry{
		Response: cachedAnswer("duckduckgo", testHit("asset-1", "duckduckgo")), StoredAt: now,
	})

	cache.now = func() time.Time { return now.Add(59 * time.Minute) }
	if _, ok := cache.Get(ctx, "key"); !ok {
		t.Fatal("answer expired before its TTL")
	}
	cache.now = func() time.Time { return now.Add(61 * time.Minute) }
	if _, ok := cache.Get(ctx, "key"); ok {
		t.Fatal("answer survived its TTL")
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_plan_cache`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("expired row was not deleted: %d rows", rows)
	}
}

// Provider invalidation is targeted: dropping one provider's answers must not
// cold-start the routes served by the others. It also accepts the budget lane,
// which is how a query class is invalidated.
func TestSQLiteSemanticPlanCacheInvalidatesOneProvider(t *testing.T) {
	db := openSemanticPlanCacheDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	cache := NewSQLiteSemanticPlanCacheAdapter(db, time.Hour, nil)
	cache.now = func() time.Time { return now }
	for key, entry := range map[string]scriptgen.SemanticSearchCacheEntry{
		"person":   {Response: cachedAnswer("duckduckgo", testHit("p", "duckduckgo")), StoredAt: now, Provider: "duckduckgo", ProviderLane: "duckduckgo"},
		"place":    {Response: cachedAnswer("wikimedia_commons", testHit("l", "wikimedia_commons")), StoredAt: now, Provider: "wikimedia_commons", ProviderLane: "wikimedia_commons"},
		"scenario": {Response: cachedAnswer("searxng", testHit("s", "searxng")), StoredAt: now, Provider: "searxng", ProviderLane: "generic"},
	} {
		cache.Put(ctx, key, entry)
	}

	removed, err := cache.InvalidateProvider(ctx, "Wikimedia_Commons")
	if err != nil {
		t.Fatalf("invalidate wikimedia_commons: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want exactly the Commons answer", removed)
	}
	if _, ok := cache.Get(ctx, "place"); ok {
		t.Fatal("invalidated provider answer is still warm")
	}
	for _, key := range []string{"person", "scenario"} {
		if _, ok := cache.Get(ctx, key); !ok {
			t.Fatalf("%s answer was dropped by an unrelated provider invalidation", key)
		}
	}

	removed, err = cache.InvalidateProvider(ctx, "duckduckgo")
	if err != nil || removed != 1 {
		t.Fatalf("invalidate lane = %d, %v; want the one person answer", removed, err)
	}
	if _, ok := cache.Get(ctx, "scenario"); !ok {
		t.Fatal("generic-lane answer was dropped by an unrelated lane invalidation")
	}
	if _, err := cache.InvalidateProvider(ctx, "  "); err == nil {
		t.Fatal("empty provider must be rejected instead of deleting the whole cache")
	}
}

func TestSQLiteSemanticPlanCacheRejectsEmptyAndMalformedRows(t *testing.T) {
	db := openSemanticPlanCacheDB(t)
	ctx := context.Background()
	cache := NewSQLiteSemanticPlanCacheAdapter(db, time.Hour, nil)

	cache.Put(ctx, "miss", scriptgen.SemanticSearchCacheEntry{Response: scriptgen.SemanticAssetSearchResponse{Provider: "duckduckgo"}})
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_plan_cache`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("empty answer was persisted: %d rows", rows)
	}

	if _, err := db.Exec(`INSERT INTO semantic_plan_cache(cache_key, payload_json, provider, provider_lane, stored_at, expires_at)
		VALUES ('broken', '{not json', 'duckduckgo', 'duckduckgo', ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed malformed row: %v", err)
	}
	if _, ok := cache.Get(ctx, "broken"); ok {
		t.Fatal("malformed payload was served as a hit")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_plan_cache WHERE cache_key = 'broken'`).Scan(&rows); err != nil {
		t.Fatalf("count malformed rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("malformed row was not deleted: %d rows", rows)
	}
}

func TestSQLiteSemanticPlanCacheWithoutDatabaseDegradesToMiss(t *testing.T) {
	cache := NewSQLiteSemanticPlanCacheAdapter(nil, time.Hour, nil)
	if _, ok := cache.Get(context.Background(), "key"); ok {
		t.Fatal("nil database must be a miss, never a hit")
	}
	cache.Put(context.Background(), "key", scriptgen.SemanticSearchCacheEntry{
		Response: cachedAnswer("duckduckgo", testHit("a", "duckduckgo")),
	})
	if _, err := cache.InvalidateProvider(context.Background(), "duckduckgo"); err == nil {
		t.Fatal("invalidation without a database must report the unavailable cache")
	}
}
