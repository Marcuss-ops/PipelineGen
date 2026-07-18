package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/web"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestNewHandler_NilLog_DefaultsToNop(t *testing.T) {
	t.Parallel()
	h := NewHandler(web.DistFS(), nil)
	assert.NotNil(t, h)
	assert.NotNil(t, h.StaticFS())
	assert.NotNil(t, h.log)
}

func TestNewHandler_NilFS_IsNil(t *testing.T) {
	t.Parallel()
	h := NewHandler(nil, nil)
	assert.NotNil(t, h)
	assert.Nil(t, h.StaticFS())
	assert.NotNil(t, h.log)
}

func TestHandler_Health(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	h := NewHandler(nil, nil)
	router := gin.New()
	rg := router.Group("/api/admin/ui")
	h.RegisterRoutes(rg)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/ui/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ok":true`)
	assert.Contains(t, rec.Body.String(), `"ui":"admin-ui"`)
}
