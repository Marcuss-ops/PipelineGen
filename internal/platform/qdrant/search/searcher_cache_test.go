package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Alias cache unit tests (QDRANT-ALIAS-CACHE, July 2026) ──────────

// aliasCacheTestServer returns an httptest.Server that serves a GetAliasTarget
// response. callCount is incremented on every GET to the global
// `/aliases` endpoint (per PR-ALIAS-RESOLVE-FIX, 2026-07-04 — transport
// no longer reads `/collections/{alias}/aliases` for alias resolution).
// The response uses the canonical Qdrant envelope
// `{"result": {"aliases": [{alias_name, collection_name}]}}` so the
// primary decoder exercises the live path; relying on the legacy
// flat-shape fallback would make these tests silently break the day
// someone removes the migration-window decoder.
func aliasCacheTestServer(t *testing.T, targetCollection string) (*httptest.Server, *int32) {
	t.Helper()
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/aliases" {
			atomic.AddInt32(&callCount, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"aliases": []map[string]interface{}{
						{
							"alias_name":      "media_assets_current",
							"collection_name": targetCollection,
						},
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	return srv, &callCount
}

func TestSearcher_AliasCache_Hit(t *testing.T) {
	t.Parallel()

	srv, callCount := aliasCacheTestServer(t, "media_assets")
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	searcher := NewSearcher(client, schema, zap.NewNop())

	ctx := context.Background()

	// First call: cache miss → HTTP round-trip.
	c1, err := searcher.resolveCollection(ctx)
	require.NoError(t, err)
	assert.Equal(t, "media_assets", c1)

	// Second call: cache hit → NO HTTP round-trip.
	c2, err := searcher.resolveCollection(ctx)
	require.NoError(t, err)
	assert.Equal(t, "media_assets", c2)

	// Exactly ONE GetAliasTarget call should have been made.
	assert.Equal(t, int32(1), atomic.LoadInt32(callCount),
		"cache hit should avoid second HTTP round-trip")
}

func TestSearcher_AliasCache_Expiry(t *testing.T) {
	// NOT parallel — manipulates the package-level aliasCacheTTL constant-like
	// behaviour by overriding the TTL at the field level. Since aliasCacheTTL
	// is a const, we test expiry by directly manipulating the Searcher's
	// cachedAt field to simulate a time jump.

	srv, callCount := aliasCacheTestServer(t, "media_assets")
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	searcher := NewSearcher(client, schema, zap.NewNop())

	ctx := context.Background()

	// Prime the cache.
	_, err := searcher.resolveCollection(ctx)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(callCount))

	// Simulate TTL expiry by rewinding cachedAt past the TTL.
	searcher.cacheMu.Lock()
	searcher.cachedAt = time.Now().Add(-aliasCacheTTL - time.Second)
	searcher.cacheMu.Unlock()

	// Next call: TTL expired → fresh HTTP round-trip.
	c, err := searcher.resolveCollection(ctx)
	require.NoError(t, err)
	assert.Equal(t, "media_assets", c)
	assert.Equal(t, int32(2), atomic.LoadInt32(callCount),
		"expired cache should trigger a fresh GetAliasTarget call")
}

func TestSearcher_AliasCache_Invalidation(t *testing.T) {
	t.Parallel()

	srv, callCount := aliasCacheTestServer(t, "media_assets")
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	searcher := NewSearcher(client, schema, zap.NewNop())

	ctx := context.Background()

	// Prime the cache.
	_, err := searcher.resolveCollection(ctx)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(callCount))

	// Invalidate.
	searcher.ResetSearchCache()

	// Next call: cache miss → fresh HTTP round-trip.
	_, err = searcher.resolveCollection(ctx)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(callCount),
		"ResetSearchCache should force a fresh GetAliasTarget call")
}

func TestSearcher_AliasCache_ConcurrentReads(t *testing.T) {
	t.Parallel()

	srv, callCount := aliasCacheTestServer(t, "media_assets")
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	searcher := NewSearcher(client, schema, zap.NewNop())

	ctx := context.Background()

	// Prime the cache with one warm-up call.
	_, err := searcher.resolveCollection(ctx)
	require.NoError(t, err)

	// Spawn 50 concurrent goroutines that all call resolveCollection.
	// They should all hit the cache and make zero additional HTTP calls.
	var wg sync.WaitGroup
	const goroutines = 50
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := searcher.resolveCollection(ctx)
			if err != nil {
				errs <- err
				return
			}
			if c != "media_assets" {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent resolveCollection failed: %v", err)
	}

	// Still exactly ONE GetAliasTarget call (the warm-up).
	assert.Equal(t, int32(1), atomic.LoadInt32(callCount),
		"concurrent cache reads must not trigger additional HTTP calls")
}

func TestSearcher_AliasCache_ConcurrentCacheFill(t *testing.T) {
	// NOT parallel — this test spawns goroutines that race to fill the cache.

	srv, callCount := aliasCacheTestServer(t, "media_assets")
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	searcher := NewSearcher(client, schema, zap.NewNop())

	ctx := context.Background()

	// Spawn 20 goroutines that all race to fill the cache from cold.
	// The first goroutine does the HTTP call; the rest hit the
	// double-check lock and return the cached value.
	var wg sync.WaitGroup
	const goroutines = 20
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := searcher.resolveCollection(ctx)
			if err != nil {
				errs <- err
				return
			}
			if c != "media_assets" {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent cache fill failed: %v", err)
	}

	// singleflight dedup guarantees single-write semantics: exactly
	// ONE HTTP call even under N-way concurrent cache-fill race.
	// The double-check inside the singleflight callback catches the
	// case where another goroutine populated the cache between RUnlock
	// and singleflight execution.
	// If this ever flakes on overloaded CI (unlikely — singleflight
	// is lock-free on the fast path), loosen to ≤3, but start strict.
	assert.Equal(t, int32(1), atomic.LoadInt32(callCount),
		"singleflight dedup must guarantee exactly one GetAliasTarget call")
}

// ── PromoteCandidate → cache invalidation integration test ────────────

func TestCollectionManager_PromoteCandidate_InvalidatesSearchCache(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var aliasActions []map[string]interface{}
	collectionCreated := false
	var aliasTargetCallCount int32

	// Build a schema with a single dense vector to keep the mock simple.
	schema := &qdrantSchema.IndexSchema{
		Version:      "v3-minimal",
		PhysicalName: "media_assets",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}
	require.NoError(t, schema.Validate())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		// Alias target check (PR-ALIAS-RESOLVE-FIX, 2026-07-04): transport
		// resolves aliases via the GLOBAL `/aliases` endpoint, NOT the
		// collection-prefixed `/collections/{alias}/aliases`. We intercept
		// the live endpoint and emit the canonical Qdrant envelope
		// `{"result": {"aliases": [...]}}`. Track call count so the
		// test can verify cache-invalidation via observable behaviour.
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			atomic.AddInt32(&aliasTargetCallCount, 1)
			if !collectionCreated {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"aliases": []map[string]interface{}{
						{
							"alias_name":      "media_assets_current",
							"collection_name": "media_assets",
						},
					},
				},
			})
		// Physical collection check.
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets":
			if !collectionCreated {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"status":       "green",
					"points_count": 42.0,
					"config": map[string]interface{}{
						"params": map[string]interface{}{
							"vectors": map[string]interface{}{
								"text": map[string]interface{}{"size": float64(768), "distance": "Cosine"},
							},
						},
					},
					"payload_schema": map[string]interface{}{},
				},
			})
		// Create collection.
		case r.Method == http.MethodPut && r.URL.Path == "/collections/media_assets":
			collectionCreated = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": true, "status": "ok"})
		// Create alias (PromoteCandidate).
		case r.Method == http.MethodPost && r.URL.Path == "/collections/aliases":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			actions, _ := body["actions"].([]interface{})
			for _, a := range actions {
				aliasActions = append(aliasActions, a.(map[string]interface{}))
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": true, "status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	searcher := NewSearcher(client, schema, zap.NewNop())
	cm := collections.NewCollectionManager(client, schema, zap.NewNop())

	// Wire cache invalidation (as runtime.go does).
	cm.OnAliasSwitch = searcher.ResetSearchCache

	// ── Phase 1: prepare and promote a registered projection. ───────
	// This fixture intentionally exercises the explicit lifecycle path so
	// it does not bypass the mandatory SQLite-authoritative verifier gate
	// through EnsureSchema.
	ctx := context.Background()
	require.NoError(t, cm.BeginProjection(ctx, "build-cache", "media_assets", 0))
	require.NoError(t, cm.PrepareCandidate(ctx, "media_assets"))
	require.NoError(t, cm.TransitionProjection(ctx, "build-cache", capregistry.ProjectionValidating))
	require.NoError(t, cm.TransitionProjection(ctx, "build-cache", capregistry.ProjectionReady))
	cm.MarkVerified("media_assets")
	require.NoError(t, cm.PromoteCandidate(ctx, "media_assets"))

	// After PromoteCandidate, the alias switch callback should have reset
	// the searcher's cache. Verify via direct cache inspection:
	// the cachedTarget must be empty (cache was invalidated).
	searcher.cacheMu.RLock()
	assert.Empty(t, searcher.cachedTarget, "PromoteCandidate→OnAliasSwitch must clear cachedTarget")
	assert.True(t, searcher.cachedAt.IsZero(), "PromoteCandidate→OnAliasSwitch must zero cachedAt")
	searcher.cacheMu.RUnlock()

	// First resolveCollection after cache invalidation: must go to the wire.
	c, err := searcher.resolveCollection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "media_assets", c)

	// Cache must now be populated.
	searcher.cacheMu.RLock()
	assert.Equal(t, "media_assets", searcher.cachedTarget)
	assert.False(t, searcher.cachedAt.IsZero())
	searcher.cacheMu.RUnlock()

	// ── Phase 2: cache hit verification.
	// Second resolveCollection: should hit the cache (no extra wire call).
	beforeHit := atomic.LoadInt32(&aliasTargetCallCount)
	c2, err := searcher.resolveCollection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "media_assets", c2)
	assert.Equal(t, beforeHit, atomic.LoadInt32(&aliasTargetCallCount),
		"second resolveCollection must hit cache, not call GetAliasTarget")

	// ── Phase 3: PromoteCandidate again → cache invalidated. Verify via
	// direct cache inspection: cachedTarget must be empty after the second promote.
	cm.MarkVerified("media_assets")
	err = cm.PromoteCandidate(context.Background(), "media_assets")
	require.NoError(t, err)

	// Cache must be empty after second PromoteCandidate.
	searcher.cacheMu.RLock()
	assert.Empty(t, searcher.cachedTarget, "second PromoteCandidate must clear cachedTarget")
	searcher.cacheMu.RUnlock()

	// After PromoteCandidate, resolveCollection must go to the wire.
	c3, err := searcher.resolveCollection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "media_assets", c3)
}

// ── ResetSearchCache is safe when Searcher is zero-valued ─────────────

func TestSearcher_ResetSearchCache_NilSafe(t *testing.T) {
	t.Parallel()

	searcher := &Searcher{}
	// Must not panic.
	searcher.ResetSearchCache()

	searcher.cacheMu.RLock()
	assert.Empty(t, searcher.cachedTarget)
	assert.True(t, searcher.cachedAt.IsZero())
	searcher.cacheMu.RUnlock()
}

// ── resolveCollection error path does not pollute cache ──────────────

func TestSearcher_ResolveCollection_ErrorDoesNotCache(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var callCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		// Always return 404 — alias never resolves.
		http.NotFound(w, r)
	}))
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	searcher := NewSearcher(client, schema, zap.NewNop())
	ctx := context.Background()

	// First call: error, cache NOT populated.
	_, err := searcher.resolveCollection(ctx)
	require.Error(t, err)

	// Cache should still be empty.
	searcher.cacheMu.RLock()
	assert.Empty(t, searcher.cachedTarget)
	searcher.cacheMu.RUnlock()

	// Second call: re-attempts GetAliasTarget because cache was not set.
	_, err = searcher.resolveCollection(ctx)
	require.Error(t, err)

	assert.Equal(t, 2, callCount,
		"error path must not populate cache; each call must re-attempt GetAliasTarget")
}
