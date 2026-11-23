package scripts

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteVidRushCacheRoundTripAndExpiry(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE vidrush_provider_cache (
		namespace TEXT NOT NULL, cache_key TEXT NOT NULL, payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		PRIMARY KEY(namespace, cache_key))`); err != nil {
		t.Fatal(err)
	}

	cache := &SQLiteVidRushCacheAdapter{db: db}
	ctx := context.Background()
	if err := cache.Put(ctx, "internet_images", "key-1", []byte(`{"candidates":[]}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	payload, hit, err := cache.Get(ctx, "internet_images", "key-1")
	if err != nil || !hit || string(payload) != `{"candidates":[]}` {
		t.Fatalf("get = %q, hit=%v, err=%v", payload, hit, err)
	}
	if _, err := db.Exec(`UPDATE vidrush_provider_cache SET updated_at = datetime('now', '-49 hours') WHERE namespace = 'internet_images' AND cache_key = 'key-1'`); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := cache.Get(ctx, "internet_images", "key-1"); err != nil || hit {
		t.Fatalf("expired get hit=%v err=%v", hit, err)
	}
}

// TestSQLiteVidRushCacheMaterializedNeverExpires pins the namespace policy: the
// materialization snapshot is a projection of durable media and must survive the
// discovery TTL, otherwise a warm job loses the URL -> published-bytes link
// after 48h and re-downloads/re-uploads the same image.
func TestSQLiteVidRushCacheMaterializedNeverExpires(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE vidrush_provider_cache (
		namespace TEXT NOT NULL, cache_key TEXT NOT NULL, payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		PRIMARY KEY(namespace, cache_key))`); err != nil {
		t.Fatal(err)
	}
	cache := &SQLiteVidRushCacheAdapter{db: db}
	ctx := context.Background()
	if err := cache.Put(ctx, "materialized", "asset-key", []byte(`{"drive_link":"https://drive.example/1"}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := db.Exec(`UPDATE vidrush_provider_cache SET updated_at = datetime('now', '-400 days') WHERE namespace = 'materialized'`); err != nil {
		t.Fatal(err)
	}
	payload, hit, err := cache.Get(ctx, "materialized", "asset-key")
	if err != nil || !hit || string(payload) != `{"drive_link":"https://drive.example/1"}` {
		t.Fatalf("materialized get = %q, hit=%v, err=%v — durable materialization must not expire", payload, hit, err)
	}
	if ttl := vidRushCacheTTLFor("internet_images"); ttl != vidRushCacheTTL {
		t.Fatalf("discovery ttl = %v, want %v", ttl, vidRushCacheTTL)
	}
}
