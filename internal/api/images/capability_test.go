package images

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutes_ExposesOnlyCanonicalImageGenerationPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	(&ImagesHandler{}).RegisterRoutes(engine.Group("/api/images"))

	canonicalPath := "/api/images/generated/generate"
	removedPath := "/api/images" + "/generate"
	var canonicalFound bool
	for _, route := range engine.Routes() {
		if route.Method != http.MethodPost {
			continue
		}

		switch route.Path {
		case canonicalPath:
			canonicalFound = true
		case removedPath:
			t.Fatal("removed image generation route must not be registered")
		}
	}

	if !canonicalFound {
		t.Fatalf("canonical POST %s is not registered", canonicalPath)
	}
}
