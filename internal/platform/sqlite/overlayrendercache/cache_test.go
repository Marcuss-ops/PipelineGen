package overlayrendercache

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	migration "github.com/Marcuss-ops/PipelineGen/migrations/sqlite"
)

const artifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:overlay-render-cache-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(migration.OverlayRenderCacheDDL); err != nil {
		t.Fatalf("apply canonical overlay render cache DDL: %v", err)
	}
	cache, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func testKey() scriptgen.OverlayRenderCacheKey {
	return scriptgen.OverlayRenderCacheKey{
		RenderKey:   strings.Repeat("a", 64),
		PlanContext: strings.Repeat("b", 64),
		Epoch:       "test-epoch",
	}
}

func testArtifact() scriptgen.RenderArtifact {
	return scriptgen.RenderArtifact{
		ID: "job-1", URL: "https://store.example/objects/" + artifactSHA,
		SHA256: artifactSHA, SizeBytes: 4096, MimeType: "video/mp4", RenderMS: 900,
		Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
	}
}

func TestStoreLookupRoundTripAndHitCount(t *testing.T) {
	cache := newTestCache(t)
	ctx := context.Background()
	key := testKey()

	if _, hit, err := cache.Lookup(ctx, key); err != nil || hit {
		t.Fatalf("initial lookup = hit=%v err=%v, want a clean miss", hit, err)
	}
	if err := cache.Store(ctx, key, testArtifact()); err != nil {
		t.Fatal(err)
	}
	got, hit, err := cache.Lookup(ctx, key)
	if err != nil || !hit {
		t.Fatalf("lookup after store = hit=%v err=%v", hit, err)
	}
	if got.SHA256 != artifactSHA || got.SizeBytes != 4096 || got.URL != testArtifact().URL {
		t.Fatalf("round-tripped artifact = %+v", got)
	}
	// Worker phase timings survive in the row: they are evidence of the
	// producing render, and the enqueuer sanitizes them on reuse.
	if got.RenderMS != 900 {
		t.Fatalf("stored artifact lost its measurements: %+v", got)
	}

	var hits int
	if err := cache.db.QueryRow(`SELECT hit_count FROM overlay_render_cache_entries WHERE cache_key = ?`, key.Digest()).Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("hit_count = %d, want 1", hits)
	}
}

func TestLookupUnknownKeyIsACleanMiss(t *testing.T) {
	cache := newTestCache(t)
	artifact, hit, err := cache.Lookup(context.Background(), testKey())
	if err != nil || hit || artifact != nil {
		t.Fatalf("unknown key = artifact=%+v hit=%v err=%v, want a clean miss", artifact, hit, err)
	}
}

func TestInvalidateHidesTheRowAndStoreRepairsIt(t *testing.T) {
	cache := newTestCache(t)
	ctx := context.Background()
	key := testKey()
	if err := cache.Store(ctx, key, testArtifact()); err != nil {
		t.Fatal(err)
	}
	if err := cache.Invalidate(ctx, key, "object store rejected the locator"); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := cache.Lookup(ctx, key); err != nil || hit {
		t.Fatalf("invalidated lookup = hit=%v err=%v, want a miss", hit, err)
	}
	var status, reason string
	if err := cache.db.QueryRow(`SELECT status, invalidated_reason FROM overlay_render_cache_entries WHERE cache_key = ?`, key.Digest()).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "INVALID" || !strings.Contains(reason, "rejected") {
		t.Fatalf("row after invalidation = status %q reason %q", status, reason)
	}
	// A fresh render is the repair.
	if err := cache.Store(ctx, key, testArtifact()); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := cache.Lookup(ctx, key); err != nil || !hit {
		t.Fatalf("store after invalidation = hit=%v err=%v, want a hit", hit, err)
	}
}

func TestInvalidateUnknownKeyIsNotAnError(t *testing.T) {
	cache := newTestCache(t)
	if err := cache.Invalidate(context.Background(), testKey(), "never stored"); err != nil {
		t.Fatalf("invalidating an absent key must be a no-op: %v", err)
	}
}

func TestCorruptArtifactJSONIsInvalidatedAndMissed(t *testing.T) {
	cache := newTestCache(t)
	ctx := context.Background()
	key := testKey()
	if err := cache.Store(ctx, key, testArtifact()); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.db.Exec(`UPDATE overlay_render_cache_entries SET artifact_json = '{not-json' WHERE cache_key = ?`, key.Digest()); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := cache.Lookup(ctx, key); err != nil || hit {
		t.Fatalf("corrupt row lookup = hit=%v err=%v, want a self-healing miss", hit, err)
	}
	var status string
	if err := cache.db.QueryRow(`SELECT status FROM overlay_render_cache_entries WHERE cache_key = ?`, key.Digest()).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "INVALID" {
		t.Fatalf("corrupt row status = %q, want INVALID", status)
	}
}

func TestArtifactDigestDriftIsInvalidatedAndMissed(t *testing.T) {
	cache := newTestCache(t)
	ctx := context.Background()
	key := testKey()
	if err := cache.Store(ctx, key, testArtifact()); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.db.Exec(`
		UPDATE overlay_render_cache_entries
		SET artifact_json = json_set(artifact_json, '$.sha256', 'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc')
		WHERE cache_key = ?`, key.Digest()); err != nil {
		t.Skipf("json1 extension unavailable: %v", err)
	}
	if _, hit, err := cache.Lookup(ctx, key); err != nil || hit {
		t.Fatalf("digest-drift lookup = hit=%v err=%v, want a self-healing miss", hit, err)
	}
}

func TestStoreRejectsAnUncertifiableDigest(t *testing.T) {
	cache := newTestCache(t)
	artifact := testArtifact()
	artifact.SHA256 = "not-a-digest"
	if err := cache.Store(context.Background(), testKey(), artifact); err == nil {
		t.Fatal("store accepted an artifact without a content address")
	}
}

func TestInvalidKeysAreRejectedBeforeTouchingTheDatabase(t *testing.T) {
	cache := newTestCache(t)
	if _, _, err := cache.Lookup(context.Background(), scriptgen.OverlayRenderCacheKey{}); err == nil {
		t.Fatal("lookup accepted an empty key")
	}
	if err := cache.Store(context.Background(), scriptgen.OverlayRenderCacheKey{}, testArtifact()); err == nil {
		t.Fatal("store accepted an empty key")
	}
	if err := cache.Invalidate(context.Background(), scriptgen.OverlayRenderCacheKey{}, "x"); err == nil {
		t.Fatal("invalidate accepted an empty key")
	}
}

func TestUnwiredAdapterFailsClosed(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNotWired) {
		t.Fatalf("New(nil) = %v, want ErrNotWired", err)
	}
	var cache *Cache
	if _, _, err := cache.Lookup(context.Background(), testKey()); err == nil {
		t.Fatal("nil adapter lookup = nil error")
	}
}
