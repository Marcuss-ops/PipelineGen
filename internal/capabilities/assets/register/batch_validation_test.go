package register

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestBatchRegister_InvalidURL_Returns400BeforeDispatch pins the
// PR-BATCH-URL-PREFLIGHT contract: an invalid URL must be a synchronous 400,
// not an accepted job that fails later on the worker. The handler has svc=nil
// on purpose — the URL gate must fire before the svc-not-wired 503.
func TestBatchRegister_InvalidURL_Returns400BeforeDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(nil, nil, nil, nil)
	r := gin.New()
	h.RegisterRoutes(r.Group("/api/media"))

	body := `{"clips":[{"url":"https://example.com/not-a-youtube-url"}]}`
	req := httptest.NewRequest("POST", "/api/media/register-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid YouTube URL")
	require.NotContains(t, w.Body.String(), "not wired")
}
