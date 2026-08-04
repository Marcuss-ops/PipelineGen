package adminconsoleapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	adminapp "github.com/Marcuss-ops/PipelineGen/internal/application/adminconsole"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRegisterRoutes_DoesNotExposeGlobalEventsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := adminapp.NewService(adminapp.NewRegistry(), adminapp.NoOpAuditLogger{}, nil)
	handler := NewHandler(service, zap.NewNop())
	engine := gin.New()
	handler.RegisterRoutes(engine.Group("/api/admin"))

	for _, route := range engine.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/admin/events" {
			t.Fatalf("removed placeholder route is still registered: %s %s", route.Method, route.Path)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/events", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
