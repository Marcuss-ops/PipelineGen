package wiring

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring/mediahttp"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeContentAssets struct {
	details *asset.Details
}

func (f *fakeContentAssets) Get(context.Context, string) (*asset.Details, error) {
	return f.details, nil
}

func (f *fakeContentAssets) List(context.Context, asset.Filter) ([]*asset.Summary, error) {
	return nil, nil
}

func (f *fakeContentAssets) Count(context.Context, asset.Filter) (int64, error) { return 0, nil }

func newContentTestRouter(details *asset.Details) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/assets/:id/content", mediahttp.NewMediaContentHandler(&fakeContentAssets{details: details}, zap.NewNop()))
	return r
}

func objectStoreAsset(uri string) *asset.Details {
	return &asset.Details{
		Asset: &asset.Asset{ID: "a1", LifecycleState: asset.StateActive},
		Locations: []*asset.Location{{
			AssetID: "a1", LocationKind: asset.LocationKindObjectStorage, IsPrimary: true,
			URI: uri, MimeType: "video/mp4", FileSizeBytes: 4,
		}},
	}
}

// TestMediaContentHandler_ProxiesRangeWithAuth pins CR-19's route contract:
// bytes come from the object store through the proxy, Range is forwarded, and
// the store's bearer token never leaks to the client.
func TestMediaContentHandler_ProxiesRangeWithAuth(t *testing.T) {
	var gotRange, gotAuth string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/10")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("abcd"))
	}))
	defer store.Close()
	t.Setenv("OBJECTSTORE_TOKEN", "secret-token")

	r := newContentTestRouter(objectStoreAsset(store.URL))
	req := httptest.NewRequest(http.MethodGet, "/assets/a1/content", nil)
	req.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body %q)", w.Code, w.Body.String())
	}
	if w.Body.String() != "abcd" {
		t.Fatalf("body = %q, want abcd", w.Body.String())
	}
	if gotRange != "bytes=0-3" {
		t.Errorf("upstream Range = %q, want bytes=0-3", gotRange)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("upstream Authorization = %q", gotAuth)
	}
	if w.Header().Get("Content-Range") != "bytes 0-3/10" || w.Header().Get("Content-Length") != "4" {
		t.Errorf("range headers not preserved: %v", w.Header())
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("Accept-Ranges = %q", w.Header().Get("Accept-Ranges"))
	}
	if w.Header().Get("Authorization") != "" {
		t.Errorf("object-store token leaked to the client")
	}
}

func TestMediaContentHandler_FailsClosedWithoutObjectStoreLocation(t *testing.T) {
	details := &asset.Details{Asset: &asset.Asset{ID: "a1", LifecycleState: asset.StateActive}}
	r := newContentTestRouter(details)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/a1/content", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMediaContentHandler_RejectsNonActiveAsset(t *testing.T) {
	details := objectStoreAsset("http://127.0.0.1:9/never-called")
	details.Asset.LifecycleState = asset.StatePreparing
	r := newContentTestRouter(details)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/a1/content", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}
