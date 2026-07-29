// Package artlist — search_chain_prefer_remote_test.go
// PR-P2-SEARCH-LIVE (July 2026) — chain-order contract tests.
//
// godlike/06 SSOT: these tests pin the canonical chain-order
// behavior of SearchService.buildSearcherChain across the two modes
// gated by preferRemote:
//
//   preferRemote=false → [DBSearcher, CachedSearcher(scraper), ...pixabay/pexels per strategy]
//   preferRemote=true  → [ScraperSearcher(RAW), ...pixabay/pexels per strategy]
//     — DBSearcher is DROPPED (no stale local-cache hits)
//     — CachedSearcher wrapper is DROPPED (no in-memory TTL bypass)
//
// Without these tests, the chain-order invariant is only observable
// via end-to-end HTTP tests that also exercise the scraper. The
// service-layer tests below give operators a fast, hermetic check
// that the resolver flips exactly as the user spec requires.
//
// The tests use a countingSearcher which records its own Search
// call count — the canonical observable for "was this provider
// invoked?" (godlike/07). All assertions are on counts and outputs,
// never on internal slice structure (godlike/06: do not leak
// resolver internals through the assertion surface).

package artlist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// countingSearcher is a Searcher double that records its own call
// count and returns canned candidates. The count is the canonical
// observable for "was the chain actually invoke this provider?".
type countingSearcher struct {
	name    string
	count   int
	clips   []Candidate
	wantErr error
}

func (c *countingSearcher) Search(_ context.Context, _ SearchRequest) ([]Candidate, error) {
	c.count++
	if c.wantErr != nil {
		return nil, c.wantErr
	}
	return c.clips, nil
}

// godlike/06 SSOT compile-time pin: countingSearcher satisfies the
// canonical artlist.Searcher port. Drift in the port surface
// surfaces here as a build failure rather than as a runtime
// assertion failure that hides behind other tests.
var _ Searcher = (*countingSearcher)(nil)

// buildSearcherChainWithMocks is a test helper that builds a
// SearchService wired with the minimal dependencies used by
// buildSearcherChain (cfg, assetStore=nil since the DB tests
// require a real sqlite fixture, scraperSearcher, cache). Strategy
// is pinned to StrategyArtlistOnly so only the scraper is in the
// infra chain (the test focus is scraper/DB/cache, not public
// providers).
func buildSearcherChainWithMocks(t *testing.T, preferRemote bool) (*SearchService, *countingSearcher, *liveSearchCache) {
	t.Helper()

	scraper := &countingSearcher{
		name: "scraper",
		clips: []Candidate{
			{ID: "boxing-1", Title: "Boxing Highlight A", SourceRef: "https://cdn.artlist.io/clip/boxing-1.m3u8"},
		},
	}
	cache := newLiveSearchCache()
	ss := &SearchService{
		service: &Service{
			cfg:             &config.Config{},
			log:             zap.NewNop(),
			assetStore:      nil, // DB side tested separately via writeFakeArtlistScraper pattern
			scraperSearcher: scraper,
			liveCache:       cache,
			searchStrategy:  StrategyArtlistOnly,
		},
	}
	chain := ss.buildSearcherChain(preferRemote)
	require.NotNilf(t, chain, "chain must be non-nil for preferRemote=%t (scraper wired, strategy=artlist_only)", preferRemote)
	return ss, scraper, cache
}

// TestBuildSearcherChain_PreferRemoteTrue_InvokesRawScraper pins
// the central PR-P2-SEARCH-LIVE contract:
// "prefer_remote=true → scraper is PRIMARY, NO DB, NO cached wrapper."
//
// When prefer_remote=true and a CachedSearcher wrapper would
// otherwise be inserted between the user and scraper, the wrapper
// is DROPPED entirely per user spec. The raw scraper is invoked
// directly. Pin: scraper.count == 1, returned candidates come from
// the raw scraper (no stale cache hits).
func TestBuildSearcherChain_PreferRemoteTrue_InvokesRawScraper(t *testing.T) {
	ctx := context.Background()
	_, scraper, _ := buildSearcherChainWithMocks(t, true)

	// Construction does NOT invoke Search (resolver only assembles
	// the chain slice). Reset the counter to assert the Search() call.
	scraper.count = 0

	// We can't reach the chain from the helper (only returned the
	// Service triple). Reconstruct: buildSearcherChain returns the
	// chain but our helper discards it. Rebuild inline to keep the
	// helper tidy and avoid leaking the chain struct through the
	// test public surface.
	scraperScratch := &countingSearcher{
		name:  "scraper",
		clips: scraper.clips,
	}
	cache := newLiveSearchCache()
	ss := &SearchService{
		service: &Service{
			cfg:             &config.Config{},
			log:             zap.NewNop(),
			assetStore:      nil,
			scraperSearcher: scraperScratch,
			liveCache:       cache,
			searchStrategy:  StrategyArtlistOnly,
		},
	}
	chain := ss.buildSearcherChain(true)
	require.NotNil(t, chain)

	candidates, err := chain.Search(ctx, SearchRequest{Term: "boxing", Limit: 20, PreferRemote: true})
	require.NoError(t, err)
	require.Len(t, candidates, 1, "chain must return scraper's single candidate")
	require.Equal(t, "boxing-1", candidates[0].ID, "returned candidate must come from RAW scraper (no cache wrapping)")

	// PIN: scraper was invoked exactly ONCE (CachedSearcher wrapper
	// is dropped so there's no cache layer between user and scraper).
	require.Equal(
		t,
		1,
		scraperScratch.count,
		"scraper must be invoked exactly once with prefer_remote=true (cached wrapper DROPPED)",
	)
}

// TestBuildSearcherChain_PreferRemoteFalse_PreservesLegacyChain
// pins the BACKWARD-COMPAT contract: prefer_remote=false preserves
// the pre-PR chain order (DBSearcher first, CachedSearcher wraps
// scraper, CACHE HITS short-circuit the scraper call).
//
// Specifically: when the live cache has a fresh entry for "boxing"
// AND the scraper is configured AND preferRemote=false, the chain
// returns the cached entry without invoking the scraper. This is
// the operator-facing "cache-first" behavior the user spec says
// MUST be preserved when prefer_remote=false.
func TestBuildSearcherChain_PreferRemoteFalse_PreservesLegacyChain(t *testing.T) {
	ctx := context.Background()

	scraper := &countingSearcher{
		name:  "scraper",
		clips: []Candidate{{ID: "scraper-fresh", Title: "Scraper Fresh"}},
	}
	cache := newLiveSearchCache()
	// Pre-populate the cache with a fresh entry for "boxing".
	cache.set("boxing", []Candidate{{ID: "boxing-cached-hit", Title: "Boxing Cached"}})

	ss := &SearchService{
		service: &Service{
			cfg:             &config.Config{},
			log:             zap.NewNop(),
			assetStore:      nil,
			scraperSearcher: scraper,
			liveCache:       cache,
			searchStrategy:  StrategyArtlistOnly,
		},
	}
	chain := ss.buildSearcherChain(false)
	require.NotNil(t, chain)

	scraper.count = 0

	candidates, err := chain.Search(ctx, SearchRequest{Term: "boxing", Limit: 20, PreferRemote: false})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(
		t,
		"boxing-cached-hit",
		candidates[0].ID,
		"prefer_remote=false: CachedSearcher wrapper is ON — cache hit short-circuits scraper",
	)
	require.Equal(
		t,
		0,
		scraper.count,
		"prefer_remote=false: scraper must NOT be invoked when cache is fresh (legacy semantic)",
	)
}

// TestBuildSearcherChain_PreferRemoteFalse_CacheMissInvokesScraper
// pins the second branch of the legacy chain: when the cache is
// empty and prefer_remote=false, the CachedSearcher wrapper still
// invokes the scraper (cache miss path). The chain order is
// preserved; only the cache is bypassed when empty.
func TestBuildSearcherChain_PreferRemoteFalse_CacheMissInvokesScraper(t *testing.T) {
	ctx := context.Background()

	scraper := &countingSearcher{
		name:  "scraper",
		clips: []Candidate{{ID: "scraper-fresh", Title: "Scraper Fresh Hit"}},
	}
	cache := newLiveSearchCache() // empty

	ss := &SearchService{
		service: &Service{
			cfg:             &config.Config{},
			log:             zap.NewNop(),
			assetStore:      nil,
			scraperSearcher: scraper,
			liveCache:       cache,
			searchStrategy:  StrategyArtlistOnly,
		},
	}
	chain := ss.buildSearcherChain(false)
	require.NotNil(t, chain)

	scraper.count = 0

	candidates, err := chain.Search(ctx, SearchRequest{Term: "boxing", Limit: 20, PreferRemote: false})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, "scraper-fresh", candidates[0].ID)
	require.Equal(
		t,
		1,
		scraper.count,
		"prefer_remote=false with empty cache: scraper is invoked on cache miss (CachedSearcher falls through)",
	)
}

// TestBuildSearcherChain_PreferRemoteTrue_NoCacheWriteAfterScrape
// pins the SSOT contract: with preferRemote=true, the
// CachedSearcher wrapper is REMOVED from the chain — which means
// neither the cache READ nor the cache WRITE paths fire. After a
// successful scraper hit, the live cache MUST remain empty
// (cache.set is only called inside CachedSearcher.Search which
// isn't in the chain).
func TestBuildSearcherChain_PreferRemoteTrue_NoCacheWriteAfterScrape(t *testing.T) {
	ctx := context.Background()

	scraper := &countingSearcher{
		name: "scraper",
		clips: []Candidate{
			{ID: "boxing-1", Title: "Boxing Highlight A"},
		},
	}
	cache := newLiveSearchCache()

	ss := &SearchService{
		service: &Service{
			cfg:             &config.Config{},
			log:             zap.NewNop(),
			assetStore:      nil,
			scraperSearcher: scraper,
			liveCache:       cache,
			searchStrategy:  StrategyArtlistOnly,
		},
	}
	chain := ss.buildSearcherChain(true)
	require.NotNil(t, chain)

	_, err := chain.Search(ctx, SearchRequest{Term: "boxing", Limit: 20, PreferRemote: true})
	require.NoError(t, err)

	// PIN: cache MUST remain empty after prefer_remote=true scrape.
	// CachedSearcher.set is the only writer; with the wrapper
	// dropped, the cache must not be populated.
	_, hit := cache.get("boxing")
	require.False(
		t,
		hit,
		"prefer_remote=true must NOT populate the in-memory cache (CachedSearcher wrapper is removed from chain)",
	)
}

// TestSearchLive_PreferRemoteTrue_InvokesScraper_EvenWithRealDBHits
// pins the canonical user-spec integration PROOF using a real
// SQLite-backed DBSearcher.
//
// User spec (Italian, verbatim): "Test di integrazione che dimostri
// che SearchLive('boxing', limit=20) chiama davvero lo scraper
// anche con risultati presenti in SQLite."
//
// godlike/06 SSOT: the test uses the canonical production helpers
// (createTestDB, insertTestClip) from service_test.go and the
// canonical assets.NewClipsRepository (the concrete AssetStore
// port implementation). The DBSearcher wraps that AssetStore via
// the canonical NewDBSearcher constructor — the same chain
// production wiring uses at composition time. The scraper is a
// countingSearcher double so the INVOCATION count is the canonical
// observable (godlike/07) and not a stubbed Searcher.Search.
//
// Subtests cover both directions of the contract:
//
//	A) prefer_remote=true + DB has cached hits → scraper.count >= 1
//	   (USER-SPEC literal satisfied — scraper is invoked despite
//	   DBSearcher having a valid hit, because DBSearcher is DROPPED
//	   from the chain under this mode).
//	B) prefer_remote=false + DB has cached hits → scraper.count == 0
//	   (BACKWARD-COMPAT — legacy cache-first semantics preserved:
//	   DBSearcher is at chain index 0 and short-circuits the
//	   scraper call).
func TestSearchLive_PreferRemoteTrue_InvokesScraper_EvenWithRealDBHits(t *testing.T) {
	ctx := context.Background()

	// buildSceneForSearchLive constructs the canonical pre-conditions
	// for the spec contract: real SQLite-backed DBSearcher that has a
	// valid hit for "boxing". Returns the wired SearchService +
	// scraper counter so subtests can assert on the invocation count
	// (godlike/07 no-fake-availability: counter is the canonical
	// observable, not a mock return value).
	buildSceneForSearchLive := func(t *testing.T) (*SearchService, *countingSearcher) {
		t.Helper()
		security.AddAllowedHost("cdn.artlist.io")

		cfg := &config.Config{
			Storage: config.StorageConfig{DataDir: t.TempDir()},
			Video:   config.VideoConfig{Duration: 15},
		}
		db := createTestDB(t)
		t.Cleanup(func() { _ = db.Close() })

		logger := zap.NewNop()
		artlistRepo := assets.NewClipsRepository(db, logger)

		// Pre-populate clip_search_terms + media_assets via the
		// canonical insertTestClip helper so DBSearcher.SearchByTerms
		// has a valid hit for "boxing". The TEST precondition is
		// "SQLite ha risultati" — both must be true.
		clipID := "boxing-cached-db-" + t.Name()
		_, _ = db.Exec(
			`INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)`,
			"boxing", clipID,
		)
		a := &asset.Asset{
			ID:             clipID,
			Name:           "Boxing Cached DB Hit",
			SourceURL:      "https://cdn.artlist.io/video/" + clipID + ".m3u8",
			Source:         "artlist",
			LifecycleState: asset.StateActive,
			MediaType:      "video",
		}
		a.SetDownloadLink("https://cdn.artlist.io/video/" + clipID + ".m3u8")
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)

		scraper := &countingSearcher{
			name: "scraper",
			clips: []Candidate{
				{
					ID:         "boxing-live-1",
					Title:      "Boxing Scraper Live Hit",
					SourceRef:  "https://cdn.artlist.io/video/boxing-live-1.m3u8",
					PageURL:    "https://artlist.io/clip/boxing-live-1",
					SourceName: "artlist",
				},
			},
		}

		svc, err := NewSearchService(
			&Service{
				cfg:             cfg,
				log:             logger,
				assetStore:      artlistRepo,
				scraperSearcher: scraper,
				liveCache:       newLiveSearchCache(),
				searchStrategy:  StrategyArtlistOnly,
			},
			&stubDispatcherForArtlist{repo: artlistRepo},
		)
		require.NoError(t, err)
		return svc, scraper
	}

	t.Run("USER_SPEC_LITERAL_prefer_remote_true_invokes_scraper_despite_db_hits", func(t *testing.T) {
		svc, scraper := buildSceneForSearchLive(t)

		// Construction does NOT invoke Search; reset to baseline
		// so the count delta cleanly tracks the SearchLive call.
		preCount := scraper.count

		candidates, err := svc.SearchLive(ctx, "boxing", 20, true)
		require.NoError(t, err)
		require.NotEmpty(t, candidates, "SearchLive must return at least the scraper's canned candidate")

		// USER-SPEC LITERAL PIN (godlike/07 no-fake-availability):
		// scraper invocation count MUST increase by >= 1 even
		// though the DBSearcher would have found a hit for "boxing".
		// The prefer_remote=true chain DROPS DBSearcher entirely so
		// the scraper is consulted at chain index 0.
		require.GreaterOrEqualf(
			t,
			scraper.count-preCount,
			1,
			"PR-P2-SEARCH-LIVE USER-SPEC VIOLATED: scraper was NOT invoked despite SQLite having a cached hit for %q "+
				"(scraper count delta=%d). User spec literal: 'chiama davvero lo scraper anche con risultati presenti in SQLite'.",
			"boxing",
			scraper.count-preCount,
		)
		require.Equal(
			t,
			"boxing-live-1",
			candidates[0].ID,
			"user-spec: returned candidate MUST come from the live scraper (NOT the stale DBSearcher hit)",
		)

		t.Logf(
			"PR-P2-SEARCH-LIVE user-spec proof PASSED: prefer_remote=true + DB hits ⇒ scraper invoked %d×; "+
				"returned clip %q from scraper (DBSearcher was DROPPED from chain as required by spec).",
			scraper.count-preCount,
			candidates[0].ID,
		)
	})

	t.Run("BACKWARD_COMPAT_prefer_remote_false_with_db_hits_does_NOT_invoke_scraper", func(t *testing.T) {
		svc, scraper := buildSceneForSearchLive(t)

		preCount := scraper.count

		candidates, err := svc.SearchLive(ctx, "boxing", 20, false)
		require.NoError(t, err)
		require.NotEmpty(t, candidates, "DBSearcher must return at least the pre-populated cached hit for 'boxing'")

		// USER-SPEC BACKWARD-COMPAT PIN: prefer_remote=false + DB
		// has the term ⇒ scraper MUST NOT be invoked (DBSearcher
		// at chain index 0 short-circuits the chain loop).
		// "Mantenila la cache locale come fallback solo se prefer_remote=false"
		require.Equalf(
			t,
			preCount,
			scraper.count,
			"PR-P2-SEARCH-LIVE BACKWARD-COMPAT VIOLATED: prefer_remote=false + DB has hits ⇒ "+
				"scraper MUST NOT be invoked (legacy cache-first semantics). scraper count delta=%d (expected 0).",
			scraper.count-preCount,
		)
		// DBSearcher returns its Candidate with SourceName="database"
		// (per adapter_core.go:268). Asserting on SourceName proves
		// the chain actually queried DBSearcher first, not the
		// scraper.
		assert.Equal(
			t,
			"database",
			candidates[0].SourceName,
			"prefer_remote=false: returned candidate MUST come from DBSearcher (SourceName='database')",
		)

		t.Logf(
			"PR-P2-SEARCH-LIVE backward-compat proof PASSED: prefer_remote=false + DB hits ⇒ scraper invoked %d× (expected 0); "+
				"DBSearcher returned cached hit (SourceName=%q).",
			scraper.count-preCount,
			candidates[0].SourceName,
		)
	})
}
