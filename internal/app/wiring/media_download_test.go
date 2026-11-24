package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring/mediahttp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func localAssetDetails(t *testing.T, path, expectedHash string) *asset.Details {
	t.Helper()
	return &asset.Details{
		Asset: &asset.Asset{ID: "asset-local", LifecycleState: asset.StateActive},
		Locations: []*asset.Location{{
			AssetID:       "asset-local",
			LocationKind:  asset.LocationKindLocal,
			IsPrimary:     true,
			URI:           path,
			MimeType:      "application/octet-stream",
			ContentSHA256: expectedHash,
		}},
	}
}

func newDownloadTestRouter(details *asset.Details) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/assets/:id/download", mediahttp.NewMediaAssetDownloadHandler(&fakeContentAssets{details: details}, nil, zap.NewNop()))
	return r
}

func TestMediaAssetDownloadServesLocalWithHashAndRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plate.png")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _, err := digest.SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}

	router := newDownloadTestRouter(localAssetDetails(t, path, hash))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/asset-local/download", nil))
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

	req := httptest.NewRequest(http.MethodGet, "/assets/asset-local/download", nil)
	req.Header.Set("Range", "bytes=6-10")
	wr := httptest.NewRecorder()
	router.ServeHTTP(wr, req)
	if wr.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", wr.Code)
	}
	if wr.Body.String() != "world" {
		t.Fatalf("range body = %q", wr.Body.String())
	}
}

func TestMediaAssetDownloadRejectsHashMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plate.png")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	router := newDownloadTestRouter(localAssetDetails(t, path, strings.Repeat("0", 64)))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/asset-local/download", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

func TestMediaAssetDownloadRequiresActiveAsset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plate.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	details := localAssetDetails(t, path, "")
	details.Asset.LifecycleState = asset.StateStaging
	router := newDownloadTestRouter(details)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/asset-local/download", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

// fakeDriveArtifact is a Drive reader with Range support, shaped like the
// production ClipsDriveAdapter. It deliberately reports a PNG mime type: the
// legacy clip download rejected everything that was not video/audio, which is
// exactly the regression the unified route exists to prevent.
type fakeDriveArtifact struct {
	name    string
	mime    string
	content string
}

func (f *fakeDriveArtifact) GetFileMeta(context.Context, string) (*appclips.ClipDriveFileMetaDTO, error) {
	return &appclips.ClipDriveFileMetaDTO{Name: f.name, MimeType: f.mime, Size: int64(len(f.content))}, nil
}

func (f *fakeDriveArtifact) DownloadFile(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(f.content)), f.mime, nil
}

func (f *fakeDriveArtifact) DownloadFileRange(_ context.Context, _ string, start, end int64) (io.ReadCloser, string, error) {
	if start < 0 || end < start || end >= int64(len(f.content)) {
		return nil, "", io.ErrUnexpectedEOF
	}
	return io.NopCloser(strings.NewReader(f.content[start : end+1])), f.mime, nil
}

func newDriveDownloadRouter(details *asset.Details, driver mediahttp.DriveArtifactReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/assets/:id/download", mediahttp.NewMediaAssetDownloadHandler(&fakeContentAssets{details: details}, driver, zap.NewNop()))
	return r
}

func TestMediaAssetDownloadServesDriveImageWithRange(t *testing.T) {
	content := "0123456789"
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	details := &asset.Details{
		Asset: &asset.Asset{ID: "asset-drive", LifecycleState: asset.StateActive},
		Locations: []*asset.Location{{
			AssetID:       "asset-drive",
			LocationKind:  asset.LocationKindDrive,
			IsPrimary:     true,
			ExternalID:    "drive-file-1",
			MimeType:      "image/png",
			ContentSHA256: hash,
		}},
	}
	router := newDriveDownloadRouter(details, &fakeDriveArtifact{name: "plate.png", mime: "image/png", content: content})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/asset-drive/download", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w.Body.String() != content {
		t.Fatalf("body = %q", w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := w.Header().Get("X-Content-Source"); got != "drive" {
		t.Fatalf("X-Content-Source = %q", got)
	}
	if got := w.Header().Get("X-Content-SHA256"); got != hash {
		t.Fatalf("X-Content-SHA256 = %q, want %q", got, hash)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/asset-drive/download", nil)
	req.Header.Set("Range", "bytes=2-5")
	wr := httptest.NewRecorder()
	router.ServeHTTP(wr, req)
	if wr.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206 (body=%s)", wr.Code, wr.Body.String())
	}
	if wr.Body.String() != "2345" {
		t.Fatalf("range body = %q", wr.Body.String())
	}
	if got := wr.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", got)
	}
}

func TestMediaAssetDownloadFallsBackToObjectStorageForImage(t *testing.T) {
	content := "object-store-image"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		http.ServeContent(w, r, "plate.png", time.Time{}, strings.NewReader(content))
	}))
	defer upstream.Close()

	details := &asset.Details{
		Asset: &asset.Asset{ID: "asset-object", LifecycleState: asset.StateActive},
		Locations: []*asset.Location{{
			AssetID:       "asset-object",
			LocationKind:  asset.LocationKindObjectStorage,
			IsPrimary:     true,
			URI:           upstream.URL + "/plate.png",
			MimeType:      "image/png",
			ContentSHA256: strings.Repeat("b", 64),
		}},
	}
	router := newDriveDownloadRouter(details, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/asset-object/download", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w.Body.String() != content {
		t.Fatalf("body = %q", w.Body.String())
	}
	if got := w.Header().Get("X-Content-Source"); got != "object_storage" {
		t.Fatalf("X-Content-Source = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/asset-object/download", nil)
	req.Header.Set("Range", "bytes=0-5")
	wr := httptest.NewRecorder()
	router.ServeHTTP(wr, req)
	if wr.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206 (body=%s)", wr.Code, wr.Body.String())
	}
	if wr.Body.String() != "object" {
		t.Fatalf("range body = %q", wr.Body.String())
	}
}

// TestMediaAssetDownloadSkipsEmptyObjectStorageLocation pins the priority
// contract: a row can carry an object_storage location whose URL was never
// populated (a partially-retired delivery row). That row must not shadow a
// usable Drive copy — "first location that actually has bytes" means the
// empty one is skipped.
func TestMediaAssetDownloadSkipsEmptyObjectStorageLocation(t *testing.T) {
	content := "drive-wins"
	details := &asset.Details{
		Asset: &asset.Asset{ID: "asset-mixed", LifecycleState: asset.StateActive},
		Locations: []*asset.Location{
			{AssetID: "asset-mixed", LocationKind: asset.LocationKindObjectStorage, IsPrimary: true, URI: ""},
			{AssetID: "asset-mixed", LocationKind: asset.LocationKindDrive, ExternalID: "drive-file-2"},
		},
	}
	router := newDriveDownloadRouter(details, &fakeDriveArtifact{name: "plate.png", mime: "image/png", content: content})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/asset-mixed/download", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w.Body.String() != content {
		t.Fatalf("body = %q", w.Body.String())
	}
	if got := w.Header().Get("X-Content-Source"); got != "drive" {
		t.Fatalf("X-Content-Source = %q, want drive", got)
	}
}
