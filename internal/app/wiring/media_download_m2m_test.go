package wiring

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	mw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	apimw "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// downloadTestSecurity is the minimal M2MSecurityPort for the unified-download
// end-to-end test: it hashes deterministically and resolves one client whose
// scopes are supplied per case.
type downloadTestSecurity struct {
	scopes []string
}

func (s *downloadTestSecurity) EnableM2M() bool { return true }
func (s *downloadTestSecurity) HashClientSecret(plaintext string) string {
	return "sha:" + plaintext
}
func (s *downloadTestSecurity) LookupClient(_ context.Context, hash string) (*mw.M2MClient, error) {
	if hash != "sha:remote-secret" {
		return nil, nil
	}
	return &mw.M2MClient{ClientID: "remote-01", Scopes: s.scopes, Enabled: true}, nil
}

func newDownloadM2MRouter(t *testing.T, details *asset.Details, scopes []string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	module := operator.NewM2MModule(nil, zap.NewNop(), func() bool { return true })
	module.SetAssetDownloadHandler(newMediaAssetDownloadHandler(&fakeContentAssets{details: details}, nil, zap.NewNop()))

	engine := gin.New()
	group := engine.Group("/api/v1/media")
	group.Use(apimw.JobClientAuthMiddleware(&downloadTestSecurity{scopes: scopes}, zap.NewNop()))
	module.RegisterRoutes(group)
	return engine
}

func authenticatedDownloadRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer remote-secret")
	return req
}

// TestM2MUnifiedDownloadThroughAuthStack is the end-to-end half the direct
// handler tests cannot cover: the unified download route is reached through
// the real M2M auth middleware, the real scope gate, and the real concurrency
// limiter, and still serves a local image with a verified digest and Range.
func TestM2MUnifiedDownloadThroughAuthStack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plate.png")
	content := []byte("unified-download-body")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _, err := digest.SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	engine := newDownloadM2MRouter(t, localAssetDetails(t, path, hash), []string{operator.ScopeMediaRead})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, authenticatedDownloadRequest("/api/v1/media/assets/asset-local/download"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w.Body.String() != string(content) {
		t.Fatalf("body = %q", w.Body.String())
	}
	if got := w.Header().Get("X-Content-SHA256"); got != hash {
		t.Fatalf("X-Content-SHA256 = %q, want %q", got, hash)
	}
	if got := w.Header().Get("X-Content-Source"); got != "local" {
		t.Fatalf("X-Content-Source = %q", got)
	}

	req := authenticatedDownloadRequest("/api/v1/media/assets/asset-local/download")
	req.Header.Set("Range", "bytes=8-15")
	wr := httptest.NewRecorder()
	engine.ServeHTTP(wr, req)
	if wr.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206 (body=%s)", wr.Code, wr.Body.String())
	}
	if wr.Body.String() != "download" {
		t.Fatalf("range body = %q", wr.Body.String())
	}
}

func TestM2MUnifiedDownloadRequiresMediaReadScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plate.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := newDownloadM2MRouter(t, localAssetDetails(t, path, ""), []string{"jobs.read"})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, authenticatedDownloadRequest("/api/v1/media/assets/asset-local/download"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

func TestM2MUnifiedDownloadRejectsMissingBearer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plate.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := newDownloadM2MRouter(t, localAssetDetails(t, path, ""), []string{operator.ScopeMediaRead})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/media/assets/asset-local/download", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
	}
}
