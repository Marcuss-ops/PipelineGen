package cliprender

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// render_cache_postgres_test.go pins the staleness contract of the
// deterministic render cache against a live PostgreSQL.
//
// The bug this file exists to make impossible: `Get()` used to return the
// clip_render_cache row without ever reading media_assets, so a fingerprint
// whose asset had been deleted or overwritten still reported a HIT — and the
// batch handler trusts a hit immediately (fingerprint -> asset_id -> CACHED),
// so the product advertised a certified artifact that no longer existed.
//
// Gated behind TEST_POSTGRES_DSN, like the rest of the live PostgreSQL
// surfaces, so `go test ./...` stays hermetic:
//
//	docker compose -f docker-compose.test-postgres.yml up -d --wait
//	TEST_POSTGRES_DSN=postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable \
//	  go test ./internal/capabilities/cliprender/ -run TestRenderCache -count=1

// requireRenderCachePostgres opens the live media database and applies the
// canonical migrations plus the cache DDL. It skips (never fakes) when
// TEST_POSTGRES_DSN is unset.
func requireRenderCachePostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping live PostgreSQL render-cache tests (see docker-compose.test-postgres.yml)")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres (is the test container up? make test-postgres): %v", err)
	}
	for i, ddl := range []string{
		pgmigration.MediaSchemaDDL,
		pgmigration.MediaVectorSurfacesDDL,
		pgmigration.MediaHNSWIndexesDDL,
		pgmigration.MediaTimestampsTimestamptzDDL,
		pgmigration.MediaAssetVersionsDDL,
		pgmigration.MediaAssetProcessingDDL,
		pgmigration.MediaDropAssetFacesDDL,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("apply media migration %d: %v", i+1, err)
		}
	}
	if err := EnsureRenderCacheTable(ctx, db); err != nil {
		t.Fatalf("ensure clip_render_cache: %v", err)
	}
	return db
}

// insertRenderCacheAsset writes the minimal media_assets row a cache hit must
// be able to point at. Test-only direct SQL is deliberate: the canonical
// committer is exercised by its own suite, and this test is about what the
// cache does when the row underneath it changes.
func insertRenderCacheAsset(t *testing.T, ctx context.Context, db *sql.DB, id, sha, lifecycle string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_assets (id, content_sha256, lifecycle_state, deleted_at)
		VALUES ($1, $2, $3, '')
		ON CONFLICT (id) DO UPDATE SET
			content_sha256 = EXCLUDED.content_sha256,
			lifecycle_state = EXCLUDED.lifecycle_state,
			deleted_at = ''`, id, sha, lifecycle); err != nil {
		t.Fatalf("insert media asset %s: %v", id, err)
	}
}

func TestRenderCacheHitRequiresTheReferencedAssetToStillMatch(t *testing.T) {
	db := requireRenderCachePostgres(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM clip_render_cache WHERE fingerprint = $1`, strings.Repeat("f", 64))
		_, _ = db.ExecContext(ctx, `DELETE FROM media_assets WHERE id = $1`, "cache-stale-asset")
	})

	const (
		fingerprint = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		assetID     = "cache-stale-asset"
		goodSHA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		otherSHA    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	insertRenderCacheAsset(t, ctx, db, assetID, goodSHA, "ACTIVE")

	cache := NewPostgresRenderCache(db)
	if err := cache.Put(ctx, &RenderCacheRecord{
		Fingerprint: fingerprint, AssetID: assetID, StorageKey: "clip/key.mp4",
		ArtifactURL: "https://example.test/key.mp4", ContentType: "video/mp4",
		SHA256: goodSHA, SizeBytes: 1024, DurationSec: 60.007,
		Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Backend: BackendChrononVulkan,
	}); err != nil {
		t.Fatalf("put cache record: %v", err)
	}

	// 1. Asset present with the same content address -> HIT.
	hit, err := cache.Get(ctx, fingerprint)
	if err != nil {
		t.Fatalf("a live, matching asset must be a hit: %v", err)
	}
	if hit.AssetID != assetID || hit.SHA256 != goodSHA || hit.Backend != BackendChrononVulkan {
		t.Fatalf("hit = %+v, want the certified locator", hit)
	}

	// 2. The asset was overwritten (same id, different bytes) -> MISS.
	//    Serving this would hand back the locator of a different artifact.
	insertRenderCacheAsset(t, ctx, db, assetID, otherSHA, "ACTIVE")
	if _, err := cache.Get(ctx, fingerprint); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("an overwritten asset must miss (got %v)", err)
	}

	// 3. The asset was retired but the row still exists -> MISS.
	insertRenderCacheAsset(t, ctx, db, assetID, goodSHA, "DELETED")
	if _, err := cache.Get(ctx, fingerprint); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("a retired asset must miss (got %v)", err)
	}

	// 4. The asset row is gone entirely -> MISS. This is the case the old
	//    implementation reported as CACHED.
	if _, err := db.ExecContext(ctx, `DELETE FROM media_assets WHERE id = $1`, assetID); err != nil {
		t.Fatalf("delete media asset: %v", err)
	}
	if _, err := cache.Get(ctx, fingerprint); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("a deleted asset must miss, not report a phantom CACHED (got %v)", err)
	}

	// 5. The cache row survived all of the above: the MISS came from the
	//    validation, not from the entry having been removed.
	var entries int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clip_render_cache WHERE fingerprint = $1`, fingerprint).Scan(&entries); err != nil {
		t.Fatalf("count cache rows: %v", err)
	}
	if entries != 1 {
		t.Fatalf("cache rows = %d, want the stale entry to remain (the JOIN, not a cleanup, must produce the miss)", entries)
	}
}

func TestRenderCacheIgnoresEntriesWhoseAssetWasNeverCommitted(t *testing.T) {
	db := requireRenderCachePostgres(t)
	ctx := context.Background()
	const fingerprint = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM clip_render_cache WHERE fingerprint = $1`, fingerprint)
	})

	cache := NewPostgresRenderCache(db)
	if err := cache.Put(ctx, &RenderCacheRecord{
		Fingerprint: fingerprint, AssetID: "cache-asset-never-committed",
		SHA256: strings.Repeat("c", 64), SizeBytes: 2048, Backend: BackendChrononVulkan,
	}); err != nil {
		t.Fatalf("put cache record: %v", err)
	}
	if _, err := cache.Get(ctx, fingerprint); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("a cache row with no committed asset must miss (got %v)", err)
	}
}
