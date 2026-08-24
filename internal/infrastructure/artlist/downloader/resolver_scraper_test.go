package downloader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ----- copyFile tests -----------------------------------------------------

// TestCopyFile_Success is the happy-path regression lock: copyFile must
// faithfully mirror bytes src -> dst with no corruption.
func TestCopyFile_Success(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	content := []byte("hello, copyFile - regression lock for P0-1")
	require.NoError(t, os.WriteFile(src, content, 0o644))

	require.NoError(t, copyFile(src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

// TestCopyFile_SourceNotFound pins the failure-is-fail-closed contract:
// a missing source MUST surface an error AND MUST NOT leave a half-
// written destination behind.
func TestCopyFile_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.txt")
	dst := filepath.Join(dir, "dst.txt")

	err := copyFile(src, dst)
	require.Error(t, err, "opening a missing source MUST surface an error")
	_, statErr := os.Stat(dst)
	assert.True(t, os.IsNotExist(statErr), "dst MUST NOT exist when source is absent (no half-written file)")
}

// ----- downloadViaScraper LocalPath validation tests ---------------------

type fakeScraperResp struct {
	OK        bool   `json:"ok"`
	LocalPath string `json:"local_path"`
	Error     string `json:"error,omitempty"`
}

// newFakeScraper returns an httptest.Server that responds to POST /download
// with the supplied body. Any other path or method returns 404/405 so we
// can spot routing mistakes in test setup.
func newFakeScraper(t *testing.T, body fakeScraperResp) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(req.URL.Path, "/download") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// scraperTestResolver builds a Resolver wired to sc with a sane
// ScraperTimeout (NewResolver zero would expire context.WithTimeout
// immediately). Logger is nop; metrics nil disables emission.
func scraperTestResolver(t *testing.T, sc string) *Resolver {
	t.Helper()
	return NewResolver(&config.Config{}, ResolverConfig{
		ScraperURL:     sc,
		ScraperTimeout: 5 * time.Second,
	}, zap.NewNop(), nil)
}

// TestDownloadViaScraper_RejectsRelativeLocalPath pins the
// filepath.IsAbs fast-reject path. A relative response would otherwise
// be interpreted relative to our CWD by filepath.Clean, which is
// unacceptable for a fetch whose target is supposed to live under the
// operator-configured DestinationID.
func TestDownloadViaScraper_RejectsRelativeLocalPath(t *testing.T) {
	dir := t.TempDir()
	server := newFakeScraper(t, fakeScraperResp{OK: true, LocalPath: "clip.mp4"})
	defer server.Close()

	r := scraperTestResolver(t, server.URL)
	outPath := filepath.Join(dir, "clip.mp4")

	err := r.downloadViaScraper(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://artlist.io/clip/123",
		ClipID:        "123",
		DestinationID: dir,
	}, outPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, artapp.ErrInvalidResponse,
		"non-absolute local_path MUST be rejected as ErrInvalidResponse (path-traversal guard)")
	assert.Contains(t, err.Error(), "non-absolute")
}

// TestDownloadViaScraper_RejectsLocalPathOutsideDestination pins the
// filepath.Rel containment check. A scraper response advertising a
// path outside the configured output directory MUST be rejected before
// any rename/copy touches the filesystem.
func TestDownloadViaScraper_RejectsLocalPathOutsideDestination(t *testing.T) {
	dir := t.TempDir()
	server := newFakeScraper(t, fakeScraperResp{OK: true, LocalPath: "/tmp/outside.mp4"})
	defer server.Close()

	r := scraperTestResolver(t, server.URL)
	outPath := filepath.Join(dir, "clip.mp4")

	err := r.downloadViaScraper(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://artlist.io/clip/123",
		ClipID:        "123",
		DestinationID: dir,
	}, outPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, artapp.ErrInvalidResponse,
		"local_path outside DestinationID MUST be rejected as ErrInvalidResponse")
	assert.Contains(t, err.Error(), "outside of configured output directory")
}

// TestDownloadViaScraper_HappyPathAcceptsLocalPathInsideDestination
// confirms the validation does not regress the legitimate case: when
// the scraper advertises a path that lives INSIDE our output dir,
// downloadViaScraper proceeds with the rename.
func TestDownloadViaScraper_HappyPathAcceptsLocalPathInsideDestination(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "clip.mp4")
	scraperOut := outPath + ".mp4" // matches the scraper's savePath convention
	require.NoError(t, os.WriteFile(scraperOut, []byte("fake clip bytes"), 0o644))

	server := newFakeScraper(t, fakeScraperResp{OK: true, LocalPath: scraperOut})
	defer server.Close()

	r := scraperTestResolver(t, server.URL)
	err := r.downloadViaScraper(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://artlist.io/clip/123",
		ClipID:        "123",
		DestinationID: dir,
	}, outPath)
	require.NoError(t, err)

	got, readErr := os.ReadFile(outPath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("fake clip bytes"), got)
}
