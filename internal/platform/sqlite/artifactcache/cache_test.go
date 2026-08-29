package artifactcache

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	_ "github.com/mattn/go-sqlite3"
)

const testSchema = `
CREATE TABLE artifact_cache_entries (
 cache_key TEXT PRIMARY KEY, source_sha256 TEXT NOT NULL, operation TEXT NOT NULL,
 parameters_json TEXT NOT NULL, processor_version TEXT NOT NULL, artifact_sha256 TEXT NOT NULL,
 size_bytes INTEGER NOT NULL DEFAULT 0, mime_type TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'READY', lease_id TEXT NOT NULL DEFAULT '', lease_until TEXT,
 created_at TEXT NOT NULL, last_accessed_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 error_message TEXT NOT NULL DEFAULT ''
);
CREATE TABLE artifact_cache_metrics (
 operation TEXT PRIMARY KEY, hit_count INTEGER NOT NULL DEFAULT 0,
 miss_count INTEGER NOT NULL DEFAULT 0, invalidation_count INTEGER NOT NULL DEFAULT 0,
 avoided_bytes INTEGER NOT NULL DEFAULT 0, avoided_work_ms INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);`

func newTestCache(t *testing.T) (*Cache, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:artifact-cache-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "cas")
	stager, err := artifacts.NewLocalStore(artifacts.Config{Workspace: filepath.Join(root, ".staging"), MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cas.NewStore(cas.Config{Root: root, Stager: stager})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := New(db, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cache, db
}

func testKey() artifactcache.Key {
	return artifactcache.Key{SourceSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Operation: "thumbnail", ParametersJSON: `{"timestamp_seconds":1.5}`, ProcessorVersion: "thumbnail/v1"}
}

func TestCacheMissStoreHitAndMetrics(t *testing.T) {
	cache, db := newTestCache(t)
	ctx := context.Background()
	key := testKey()
	if _, hit, err := cache.Lookup(ctx, key, 0); err != nil || hit {
		t.Fatalf("initial lookup = hit=%v err=%v, want miss", hit, err)
	}
	entry, err := cache.Store(ctx, key, bytes.NewReader([]byte("cached-jpeg")), "image/jpeg", 0)
	if err != nil {
		t.Fatal(err)
	}
	got, hit, err := cache.Lookup(ctx, key, 321)
	if err != nil || !hit || got == nil {
		t.Fatalf("second lookup = entry=%+v hit=%v err=%v", got, hit, err)
	}
	if got.ArtifactSHA256 != entry.ArtifactSHA256 || got.SizeBytes != int64(len("cached-jpeg")) {
		t.Fatalf("entry mismatch: got=%+v stored=%+v", got, entry)
	}
	reader, err := cache.Open(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(body) != "cached-jpeg" {
		t.Fatalf("materialized body=%q err=%v", body, err)
	}
	metrics, err := cache.Metrics(ctx, "thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	if metrics.MissCount != 1 || metrics.HitCount != 1 || metrics.AvoidedBytes != int64(len("cached-jpeg")) || metrics.AvoidedWorkMS != 321 {
		t.Fatalf("metrics=%+v, want one miss/hit and avoided bytes/work", metrics)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artifact_cache_entries`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("cache rows=%d err=%v, want one", rows, err)
	}
}

func TestCacheClaimSerializesBuildAndReusesCASBytes(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()
	key := testKey()
	first, err := cache.Claim(ctx, key, 5*time.Minute, 250)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Acquired || first.LeaseID == "" {
		t.Fatalf("first claim=%+v, want acquired lease", first)
	}
	if _, err := cache.StoreWithLease(ctx, key, first.LeaseID, bytes.NewReader([]byte("same-bytes")), "image/jpeg", 250); err != nil {
		t.Fatal(err)
	}
	second, err := cache.Claim(ctx, key, 5*time.Minute, 250)
	if err != nil {
		t.Fatal(err)
	}
	if second.Acquired || second.Entry == nil {
		t.Fatalf("second claim=%+v, want READY entry without new lease", second)
	}
	if second.Entry.ArtifactSHA256 == "" {
		t.Fatal("second claim returned empty artifact digest")
	}
}

func TestCacheStoreWithLeaseRejectsStaleOwner(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()
	key := testKey()
	first, err := cache.Claim(ctx, key, time.Millisecond, 250)
	if err != nil || !first.Acquired {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := cache.Claim(ctx, key, 5*time.Minute, 250)
	if err != nil || !second.Acquired {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	_, err = cache.StoreWithLease(ctx, key, first.LeaseID, bytes.NewReader([]byte("stale")), "application/octet-stream", 250)
	if !errors.Is(err, artifactcache.ErrLeaseLost) {
		t.Fatalf("stale lease store err=%v, want ErrLeaseLost", err)
	}
	if _, err := cache.StoreWithLease(ctx, key, second.LeaseID, bytes.NewReader([]byte("current")), "application/octet-stream", 250); err != nil {
		t.Fatalf("current lease store: %v", err)
	}
}

func TestCacheInvalidationDoesNotCreateMissMetric(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()
	key := testKey()
	if _, err := cache.Store(ctx, key, bytes.NewReader([]byte("x")), "application/octet-stream", 0); err != nil {
		t.Fatal(err)
	}
	if err := cache.Invalidate(ctx, key); err != nil {
		t.Fatal(err)
	}
	metrics, err := cache.Metrics(ctx, key.Operation)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.InvalidationCount != 1 || metrics.MissCount != 0 {
		t.Fatalf("metrics after invalidation=%+v, invalidation must not be a miss", metrics)
	}
}
