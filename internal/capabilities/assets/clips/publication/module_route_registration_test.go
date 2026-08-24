package assets

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type routeTestPublication struct{}

func (routeTestPublication) DownloadClip(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

func (routeTestPublication) ReuploadClip(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

func TestBuild_RegistersCanonicalSourceScopedDownloadRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	descriptor, err := Build(Dependencies{
		Publication: routeTestPublication{},
		EnabledFunc: func() bool { return true },
	})
	require.NoError(t, err)

	engine := gin.New()
	descriptor.RegisterRoutes(engine.Group("/api/media/clips"))

	have := make(map[string]bool)
	for _, route := range engine.Routes() {
		have[route.Method+" "+route.Path] = true
	}

	require.True(t, have["POST /api/media/clips/:source/clips/:id/download"],
		"publication must mount the canonical public download route under /api/media/clips")
	require.True(t, have["POST /api/media/clips/:source/clips/:id/reupload"],
		"publication must keep the canonical source-scoped reupload route alongside download")
}
