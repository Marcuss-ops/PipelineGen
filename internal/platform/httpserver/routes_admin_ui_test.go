package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// fakeAuthSecurityPort is a minimal AuthSecurityPort for testing.
type fakeAuthSecurityPort struct {
	enable    bool
	adminTok  string
	workerTok string
}

func (f *fakeAuthSecurityPort) EnableAuth() bool    { return f.enable }
func (f *fakeAuthSecurityPort) AdminToken() string  { return f.adminTok }
func (f *fakeAuthSecurityPort) WorkerToken() string { return f.workerTok }

func newTestRouter() *Router {
	return NewRouter(&RouterConfig{
		Auth:          &fakeAuthSecurityPort{enable: true, adminTok: "admin-secret"},
		Rate:          nil,
		Features:      nil,
		Log:           nil,
		ServerGinMode: gin.TestMode,
		DataDir:       "",
		DownloadDir:   "",
		CORSOrigins:   nil,
	})
}

func TestAdminUI_PublicAssets(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	router := newTestRouter()
	engine := router.Setup()

	// The static SPA is intentionally public so the browser can load
	// the login page; only the API routes require authentication.
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminAuthMe_RequiresToken(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	router := newTestRouter()
	engine := router.Setup()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth/me", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminUI_WithToken_ServesIndex(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	router := newTestRouter()
	engine := router.Setup()

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.Header.Set("X-Velox-Admin-Token", "admin-secret")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "<html")
}

func TestAdminUI_SPARoute_WithToken_FallbackToIndex(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	router := newTestRouter()
	engine := router.Setup()

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.Header.Set("X-Velox-Admin-Token", "admin-secret")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<html")
}

func TestAdminUI_AuthDisabled_AllowsRequest(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	router := NewRouter(&RouterConfig{
		Auth:          &fakeAuthSecurityPort{enable: false},
		ServerGinMode: gin.TestMode,
	})
	engine := router.Setup()

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
