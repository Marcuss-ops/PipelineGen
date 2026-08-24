package httpserver

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWireRegistry_AllNotMountedByDefault verifies that an empty route
// set reports every known capability as NOT_MOUNTED. This is the
// canonical "binary that lost the wire field" failure mode.
func TestWireRegistry_AllNotMountedByDefault(t *testing.T) {
	reg := NewWireRegistry(nil)
	all := reg.All()
	require.NotNil(t, all)
	for _, cap := range knownCapabilities {
		assert.Equal(t, WireNotMounted, all[cap.name], "capability %q should be NOT_MOUNTED with empty routes", cap.name)
		assert.False(t, reg.IsMounted(cap.name), "IsMounted(%q) should be false with empty routes", cap.name)
	}
}

// TestWireRegistry_NilReceiverAllNotMounted verifies the nil-safe All()
// contract — a nil registry returns all-NOT_MOUNTED so the /ready JSON
// shape is stable even when the wire field is uninitialised.
func TestWireRegistry_NilReceiverAllNotMounted(t *testing.T) {
	var reg *WireRegistry
	all := reg.All()
	require.NotNil(t, all)
	for _, cap := range knownCapabilities {
		assert.Equal(t, WireNotMounted, all[cap.name])
	}
	assert.False(t, reg.IsMounted("stock"), "nil IsMounted should return false")
}

// TestWireRegistry_StockMounted verifies the canonical case: the
// stock-pipeline route is registered → wire: stock: MOUNTED.
func TestWireRegistry_StockMounted(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
		{Method: "POST", Path: "/api/stock-pipeline/search-and-run"},
	})
	all := reg.All()
	assert.Equal(t, WireMounted, all["stock"], "stock should be MOUNTED when /api/stock-pipeline/* routes are registered")
	assert.Equal(t, WireNotMounted, all["artlist"], "artlist should be NOT_MOUNTED when no /api/artlist routes are registered")
	assert.Equal(t, WireNotMounted, all["voiceover"], "voiceover should be NOT_MOUNTED when no /api/voiceover routes are registered")
}

// TestWireRegistry_AllCapabilitiesMounted verifies the happy path
// where every known capability is registered. The wire map shows all
// MOUNTED.
func TestWireRegistry_AllCapabilitiesMounted(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
		{Method: "POST", Path: "/api/artlist/sync"},
		{Method: "POST", Path: "/api/media/voiceover/generate"},
		{Method: "POST", Path: "/api/script/generate"},
		{Method: "POST", Path: "/api/youtube/clip-extract"},
		{Method: "POST", Path: "/api/register/from-youtube"},
		{Method: "POST", Path: "/api/storage/sync"},
		{Method: "POST", Path: "/api/drive/admin"},
		{Method: "POST", Path: "/api/media/clips/upload"},
		{Method: "POST", Path: "/api/clips/process"},
		{Method: "POST", Path: "/internal/v1/media/search"},
		{Method: "GET", Path: "/qdrant/live"},
	})
	all := reg.All()
	for _, cap := range knownCapabilities {
		assert.Equal(t, WireMounted, all[cap.name], "capability %q should be MOUNTED", cap.name)
	}
}

// TestWireRegistry_PrefixMatching verifies the HasPrefix semantics:
// /api/stock matches /api/stock-pipeline/run (longer sub-paths) AND
// /api/stock/anything-else. A future sub-route variant doesn't
// accidentally map to a different capability.
func TestWireRegistry_PrefixMatching(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
		{Method: "POST", Path: "/api/stock/anything-else"},
		{Method: "POST", Path: "/api/artlist/sync"},
		{Method: "GET", Path: "/api/storage/sync"},
	})
	assert.True(t, reg.IsMounted("stock"))
	assert.True(t, reg.IsMounted("artlist"))
	assert.True(t, reg.IsMounted("storage"))
	assert.False(t, reg.IsMounted("voiceover"))
	assert.False(t, reg.IsMounted("register"))
}

// TestWireRegistry_UnknownCapabilityIsAlwaysNotMounted verifies that
// IsMounted returns false for a capability NOT in the known list.
// The wire map All() also does NOT include unknown capabilities
// (the keys are bounded by knownCapabilities).
func TestWireRegistry_UnknownCapabilityIsAlwaysNotMounted(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
	})
	assert.False(t, reg.IsMounted("totally_made_up_capability"))
	all := reg.All()
	assert.NotContains(t, all, "totally_made_up_capability", "All() should only return known capability keys")
}

// TestWireRegistry_FromEngineNilReturnsAllNotMounted verifies the
// gin adapter is nil-safe (used by routes.go when the engine is nil
// during composition failure paths).
func TestWireRegistry_FromEngineNilReturnsAllNotMounted(t *testing.T) {
	reg := NewWireRegistryFromEngine(nil)
	require.NotNil(t, reg)
	for _, cap := range knownCapabilities {
		assert.Equal(t, WireNotMounted, reg.All()[cap.name])
	}
}

// TestWireRegistry_FromEngineWithRoutes verifies the gin adapter
// extracts the minimal RouteInfo projection correctly. Uses a real
// gin engine so the test exercises the actual engine.Routes() path.
func TestWireRegistry_FromEngineWithRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/stock-pipeline/run", func(c *gin.Context) {})
	engine.POST("/api/artlist/sync", func(c *gin.Context) {})

	reg := NewWireRegistryFromEngine(engine)
	all := reg.All()
	assert.Equal(t, WireMounted, all["stock"])
	assert.Equal(t, WireMounted, all["artlist"])
	assert.Equal(t, WireNotMounted, all["voiceover"])
}

// TestWireRegistry_AllReturnsMapWithAllKnownCapabilities is a
// defensive test that pins the All() output to len(knownCapabilities)
// keys. If a future agent adds a known capability without extending
// the test, this catches the discrepancy.
func TestWireRegistry_AllReturnsMapWithAllKnownCapabilities(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
	})
	all := reg.All()
	assert.Len(t, all, len(knownCapabilities), "All() must return one entry per known capability")
	for _, cap := range knownCapabilities {
		assert.Contains(t, all, cap.name, "All() must include capability %q", cap.name)
	}
}

// TestWireRegistry_PrefixBoundaryDoesNotMatch verifies the `/`-boundary
// enforcement on prefix matching. Sibling capabilities that share a
// leading substring (e.g. /api/stock vs /api/storage vs /api/register)
// must NOT misclassify each other.
//
// Regression guard for code-reviewer 2026-07-08 — the original
// `strings.HasPrefix(route.Path, cap.prefix)` implementation matched
// /api/stockpiler as stock (false positive). The fix wraps matching
// in matchesCapabilityPrefix which enforces a `/` boundary.
func TestWireRegistry_PrefixBoundaryDoesNotMatch(t *testing.T) {
	// Sibling-prefix routes that must NOT classify as any known capability.
	// These are hypothetical future routes that share a leading
	// substring with a known capability prefix.
	cases := []struct {
		path     string
		notThis  string // capability that must NOT be classified as mounted
		describe string
	}{
		{path: "/api/stockpiler", notThis: "stock", describe: "stockpiler must not misclassify as stock"},
		{path: "/api/storage-admin", notThis: "storage", describe: "storage-admin must not misclassify as storage"},
		{path: "/api/register-anything", notThis: "register", describe: "register-anything must not misclassify as register"},
		{path: "/api/voiceoverX", notThis: "voiceover", describe: "voiceoverX must not misclassify as voiceover"},
		{path: "/api/youtube-archive", notThis: "youtube", describe: "youtube-archive must not misclassify as youtube"},
		{path: "/qdrant-admin", notThis: "qdrant_health", describe: "qdrant-admin must not misclassify as qdrant_health"},
	}
	for _, c := range cases {
		t.Run(c.describe, func(t *testing.T) {
			reg := NewWireRegistry([]RouteInfo{{Method: "POST", Path: c.path}})
			assert.False(t, reg.IsMounted(c.notThis),
				"path %q must NOT classify as %q (sibling-prefix boundary violation)", c.path, c.notThis)
		})
	}
}

// TestWireRegistry_PrefixBoundaryExactMatch verifies the boundary
// enforcement also accepts exact prefix matches (e.g. /api/stock-pipeline
// with no trailing slash if such a route were ever registered).
func TestWireRegistry_PrefixBoundaryExactMatch(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline"},
		{Method: "POST", Path: "/api/artlist"},
		{Method: "GET", Path: "/qdrant/"},
	})
	assert.True(t, reg.IsMounted("stock"), "exact-prefix route /api/stock-pipeline must classify as stock")
	assert.True(t, reg.IsMounted("artlist"), "exact-prefix route /api/artlist must classify as artlist")
	assert.True(t, reg.IsMounted("qdrant_health"), "trailing-slash route /qdrant/ must classify as qdrant_health")
	assert.False(t, reg.IsMounted("voiceover"), "no voiceover route in this test")
}

// TestWireRegistry_ClipsMountedUnderMedia locks the canonical clips
// prefix contract. The clips capability mounts under /api/media/clips/*
// via the assets module (internal/app/wire_assets.go), which includes
// the AI stock ingestion endpoint /api/media/clips/ingest/ai-stock.
func TestWireRegistry_ClipsMountedUnderMedia(t *testing.T) {
	t.Run("happy_path_ai_stock_mounted", func(t *testing.T) {
		reg := NewWireRegistry([]RouteInfo{
			{Method: "POST", Path: "/api/media/clips/ingest/ai-stock"},
		})
		assert.True(t, reg.IsMounted("clips"),
			"clips MUST be MOUNTED when /api/media/clips/ingest/ai-stock is registered")
		all := reg.All()
		assert.Equal(t, WireMounted, all["clips"])
		assert.Equal(t, WireNotMounted, all["youtube"],
			"legacy /api/clips/* routes must NOT classify as clips")
	})

	t.Run("legacy_youtube_clips_still_tracked", func(t *testing.T) {
		reg := NewWireRegistry([]RouteInfo{
			{Method: "POST", Path: "/api/clips/process"},
		})
		assert.True(t, reg.IsMounted("youtube"),
			"youtube MUST be MOUNTED when legacy /api/clips/process is registered")
		assert.Equal(t, WireNotMounted, reg.All()["clips"],
			"legacy /api/clips/* routes must NOT classify as clips")
	})
}

// TestWireRegistry_VoiceoverMountedUnderMedia locks the canonical
// voiceover prefix contract (per internal/app/wire_assets.go::WireAssets
// assetsRouteMod wraps the Assets module under prefix `/media`,
// voiceover module under `/voiceover`, beneath routes.go's
// `api := engine.Group("/api")` — the resulting URL is
// `/api/media/voiceover/*`).
//
// Regression guard for the 2026-07-08 incident: the prior voiceover
// prefix `/api/voiceover` did NOT match the actual mount
// `/api/media/voiceover/*` and the /ready JSON reported
// `wire.voiceover: NOT_MOUNTED` while the route was live. This test
// asserts BOTH directions:
//
//	(a) HappyPath: /api/media/voiceover/generate → voiceover MOUNTED.
//	(b) SiblingBoundary: /api/voiceover/generate (a hypothetical
//	    non-aggregated route) does NOT classify as voiceover when
//	    NEITHER prefix is registered — protects against a future
//	    agent accidentally widening the prefix to also match
//	    /api/voiceover/* while keeping the mount at /api/media.
//
// godlike/06 SSOT (one canonical owner per fact): this test pins the
// canonical prefix contract; the wire.go prefix constant + this test
// form the SSOT lockstep pair — updates to either require the other.
func TestWireRegistry_VoiceoverMountedUnderMedia(t *testing.T) {
	t.Run("happy_path_mounts_under_media", func(t *testing.T) {
		reg := NewWireRegistry([]RouteInfo{
			{Method: "POST", Path: "/api/media/voiceover/generate"},
		})
		assert.True(t, reg.IsMounted("voiceover"),
			"voiceover MUST be MOUNTED when /api/media/voiceover/generate is registered "+
				"(this is the canonical Wire-clipped assets aggregate path)")
		all := reg.All()
		assert.Equal(t, WireMounted, all["voiceover"])
	})

	t.Run("sibling_boundary_does_not_match_api_voiceover", func(t *testing.T) {
		// Sibling-prefix boundary: hypothetical /api/voiceover/generate
		// (without /media/) must NOT classify as voiceover when the
		// canonical mount path is /api/media/voiceover/*.
		reg := NewWireRegistry([]RouteInfo{
			{Method: "POST", Path: "/api/voiceover/generate"},
		})
		assert.False(t, reg.IsMounted("voiceover"),
			"/api/voiceover/generate must NOT classify as voiceover (canonical mount is /api/media/voiceover/*; "+
				"sibling-prefix boundary enforcement prevents voiceoverX-style false positives)")
		assert.False(t, reg.IsMounted("stock"), "stock unaffected by this test")
	})

	t.Run("all_lit_at_canonical_prefix", func(t *testing.T) {
		// Production realistic: stock + artlist registered at /api/*,
		// voiceover registered at the assets aggregate /api/media/voiceover/*
		// (assetsRouteMod wraps all 7 capability descriptors).
		reg := NewWireRegistry([]RouteInfo{
			{Method: "POST", Path: "/api/stock-pipeline/run"},
			{Method: "POST", Path: "/api/artlist/sync"},
			{Method: "POST", Path: "/api/media/voiceover/generate"},
		})
		all := reg.All()
		assert.Equal(t, WireMounted, all["stock"])
		assert.Equal(t, WireMounted, all["artlist"])
		assert.Equal(t, WireMounted, all["voiceover"],
			"with the canonical assets aggregate prefix /api/media/voiceover/generate, "+
				"voiceover MUST report MOUNTED — this is the regression guard")
	})
}
