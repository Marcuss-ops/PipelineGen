package google_accounting

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/config"
)

func TestSyncProjectCallsDownloadEndpoint(t *testing.T) {
	t.Helper()

	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"abc","status":"pending"}`))
	}))
	defer srv.Close()

	h := NewHandler(&config.Config{
		GoogleAccounting: config.GoogleAccountingConfig{ServerURL: srv.URL},
	}, zap.NewNop(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/google-accounting/sync?video_id=vid-123&file_type=image&headless=false&account=user", nil)
	c.Request = req

	h.SyncProject(c)

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/download" {
		t.Fatalf("expected /download, got %s", gotPath)
	}
	if gotBody["video_id"] != "vid-123" {
		t.Fatalf("expected video_id vid-123, got %#v", gotBody["video_id"])
	}
	if gotBody["file_type"] != "image" {
		t.Fatalf("expected file_type image, got %#v", gotBody["file_type"])
	}
	if gotBody["headless"] != false {
		t.Fatalf("expected headless false, got %#v", gotBody["headless"])
	}
	if gotBody["account"] != "user" {
		t.Fatalf("expected account user, got %#v", gotBody["account"])
	}
}

func TestListProjectsCallsListEndpoint(t *testing.T) {
	t.Helper()

	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[{"id":"1","name":"Demo"}],"count":1}`))
	}))
	defer srv.Close()

	h := NewHandler(&config.Config{
		GoogleAccounting: config.GoogleAccountingConfig{ServerURL: srv.URL},
	}, zap.NewNop(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/google-accounting/list", nil)
	c.Request = req

	h.ListProjects(c)

	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET, got %s", gotMethod)
	}
	if gotPath != "/list" {
		t.Fatalf("expected /list, got %s", gotPath)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"count":1`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestGenerateVideo_NoService_ReturnsError(t *testing.T) {
	h := NewHandler(&config.Config{}, zap.NewNop(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/google-accounting/generate-video", strings.NewReader(`{"prompt":"test prompt"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.GenerateVideo(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "image service not available") {
		t.Fatalf("expected image service error, got: %s", w.Body.String())
	}
}

func TestJobStatusCallsStatusEndpoint(t *testing.T) {
	t.Helper()

	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"done","file_path":"./out.mp4"}`))
	}))
	defer srv.Close()

	h := NewHandler(&config.Config{
		GoogleAccounting: config.GoogleAccountingConfig{ServerURL: srv.URL},
	}, zap.NewNop(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/google-accounting/status/job-123", nil)
	c.Request = req
	c.Params = gin.Params{{Key: "job_id", Value: "job-123"}}

	h.JobStatus(c)

	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET, got %s", gotMethod)
	}
	if gotPath != "/status/job-123" {
		t.Fatalf("expected /status/job-123, got %s", gotPath)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"done"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestGenerateFlowImagesCallsGenerateEndpoint(t *testing.T) {
	t.Helper()

	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"flow-123","status":"pending"}`))
	}))
	defer srv.Close()

	h := NewHandler(&config.Config{
		GoogleAccounting: config.GoogleAccountingConfig{ServerURL: srv.URL},
	}, zap.NewNop(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/google-accounting/generate-flow-images", strings.NewReader(`{"prompt":"forest tree","project_id":"proj-1","style":"cinematic","headless":true,"account":"labs"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.GenerateFlowImages(c)

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/generate-flow-images" {
		t.Fatalf("expected /generate-flow-images, got %s", gotPath)
	}
	if gotBody["prompt"] != "forest tree" {
		t.Fatalf("expected prompt, got %#v", gotBody["prompt"])
	}
	if gotBody["project_id"] != "proj-1" {
		t.Fatalf("expected project_id proj-1, got %#v", gotBody["project_id"])
	}
	if gotBody["style"] != "cinematic" {
		t.Fatalf("expected style cinematic, got %#v", gotBody["style"])
	}
}

func TestListMediaBuildsUrls(t *testing.T) {
	t.Helper()

	tmpDir := t.TempDir()
	imagesDir := filepath.Join(tmpDir, "images", "proj-1")
	videosDir := filepath.Join(tmpDir, "videos", "proj-1")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(videosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imagesDir, "one.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(videosDir, "one.mp4"), []byte("vid"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(&config.Config{
		GoogleAccounting: config.GoogleAccountingConfig{DownloadDir: tmpDir},
	}, zap.NewNop(), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/google-accounting/media", nil)
	c.Request = req

	h.ListMedia(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `/media/google-accounting/images/proj-1/one.jpg`) {
		t.Fatalf("missing image URL in body: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `/media/google-accounting/videos/proj-1/one.mp4`) {
		t.Fatalf("missing video URL in body: %s", w.Body.String())
	}
}
