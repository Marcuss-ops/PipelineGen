package api

import (
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeInternalMediaHandler struct{}

func (fakeInternalMediaHandler) RegisterInternalMediaRoutes(r *gin.RouterGroup) {
	media := r.Group("/media")
	media.POST("/sync-drive-folder", func(c *gin.Context) {})
}

func TestNewServerWithHealth_RegistersInternalMediaRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := NewServerWithHealth(
		nil,
		nil,
		nil,
		fakeInternalMediaHandler{},
		nil,
		nil,
		nil,
	)

	routes := server.GetRouter().Routes()
	for _, route := range routes {
		if route.Method == "POST" && route.Path == "/internal/v1/media/sync-drive-folder" {
			return
		}
	}

	t.Fatalf("expected POST /internal/v1/media/sync-drive-folder to be registered")
}
