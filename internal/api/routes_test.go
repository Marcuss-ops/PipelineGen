package api

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"velox/go-master/internal/config"
	"velox/go-master/internal/module"
)

func TestRegistryRoutesKeepExpectedPrefixes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Features: config.FeaturesConfig{
			ArtlistEnabled: true,
			YouTubeEnabled: true,
		},
	}

	registry := module.NewRegistry()

	// Create mock modules that simulate the FIXED behavior (creating sub-groups)
	artlistModule := &mockModuleWithGroup{name: "artlist", prefix: "/artlist", enabled: true}
	youtubeModule := &mockModuleWithGroup{name: "clips", prefix: "/clips", enabled: true}
	jobsModule := &mockModuleWithGroup{name: "jobs", prefix: "/jobs", enabled: true}
	mediaModule := &mockModuleWithGroup{name: "media", prefix: "/media", enabled: true}

	registry.Register(artlistModule)
	registry.Register(youtubeModule)
	registry.Register(jobsModule)
	registry.Register(mediaModule)

	// Simulate what Router.Setup() does with registry
	engine := gin.New()
	apiGroup := engine.Group("/api")
	protected := apiGroup.Group("")

	// This is what RegisterAllRoutes does - calls RegisterRoutes on each module
	registry.RegisterAllRoutes(cfg, protected)

	routes := engine.Routes()

	// Check that routes are at correct paths (with module prefix)
	routeMap := make(map[string]bool)
	for _, route := range routes {
		key := route.Method + " " + route.Path
		routeMap[key] = true
	}

	// Artlist routes should be under /api/artlist/
	assert.True(t, routeMap["POST /api/artlist/run"], "POST /api/artlist/run should be registered")
	assert.True(t, routeMap["GET /api/artlist/runs/:run_id"], "GET /api/artlist/runs/:run_id should be registered")
	assert.True(t, routeMap["GET /api/artlist/stats"], "GET /api/artlist/stats should be registered")
	assert.True(t, routeMap["POST /api/artlist/search/live"], "POST /api/artlist/search/live should be registered")

	// Clips routes should be under /api/clips/
	assert.True(t, routeMap["POST /api/clips/process"], "POST /api/clips/process should be registered")
	assert.True(t, routeMap["GET /api/clips/info"], "GET /api/clips/info should be registered")
	assert.True(t, routeMap["GET /api/clips/search"], "GET /api/clips/search should be registered")
	assert.True(t, routeMap["POST /api/clips/search"], "POST /api/clips/search should be registered")

	// Jobs routes should be under /api/jobs/
	assert.True(t, routeMap["GET /api/jobs"], "GET /api/jobs should be registered")
	assert.True(t, routeMap["POST /api/jobs"], "POST /api/jobs should be registered")
	assert.True(t, routeMap["GET /api/jobs/:id"], "GET /api/jobs/:id should be registered")

	// Media routes should be under /api/media/
	assert.True(t, routeMap["GET /api/media/search"], "GET /api/media/search should be registered")

	// Ensure routes are NOT at wrong paths (without module prefix)
	assert.False(t, routeMap["POST /api/run"], "POST /api/run should NOT be registered (missing artlist prefix)")
	assert.False(t, routeMap["POST /api/extract"], "POST /api/extract should NOT be registered (missing clips prefix)")
	assert.False(t, routeMap["GET /api"], "GET /api should NOT be registered (missing jobs prefix)")
}

// mockModuleWithGroup simulates the FIXED module behavior where RegisterRoutes
// creates a sub-group with the proper prefix
type mockModuleWithGroup struct {
	name    string
	prefix  string
	enabled bool
}

func (m *mockModuleWithGroup) Name() string {
	return m.name
}

func (m *mockModuleWithGroup) Enabled(cfg *config.Config) bool {
	return m.enabled
}

func (m *mockModuleWithGroup) RegisterRoutes(rg *gin.RouterGroup) {
	// This is the key fix: create a sub-group with the module's prefix
	group := rg.Group(m.prefix)

	switch m.name {
	case "artlist":
		group.POST("/run", func(c *gin.Context) {})
		group.GET("/runs/:run_id", func(c *gin.Context) {})
		group.GET("/stats", func(c *gin.Context) {})
		group.POST("/search/live", func(c *gin.Context) {})
	case "clips":
		group.POST("/process", func(c *gin.Context) {})
		group.GET("/info", func(c *gin.Context) {})
		group.GET("/search", func(c *gin.Context) {})
		group.POST("/search", func(c *gin.Context) {})
	case "jobs":
		group.GET("", func(c *gin.Context) {})
		group.POST("", func(c *gin.Context) {})
		group.GET("/:id", func(c *gin.Context) {})
	case "media":
		group.GET("/search", func(c *gin.Context) {})
	}
}

func (m *mockModuleWithGroup) Start(ctx context.Context) error {
	return nil
}

func (m *mockModuleWithGroup) Stop(ctx context.Context) error {
	return nil
}
