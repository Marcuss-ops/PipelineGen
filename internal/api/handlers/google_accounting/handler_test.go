package google_accounting

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	}, zap.NewNop())

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
	}, zap.NewNop())

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

func TestGenerateVideoCallsGenerateEndpoint(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"job_id":"gen-123","status":"pending"}`))
	}))
	defer srv.Close()

	h := NewHandler(&config.Config{
		GoogleAccounting: config.GoogleAccountingConfig{ServerURL: srv.URL},
	}, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/google-accounting/generate-video", strings.NewReader(`{"video_id":"vid-123","prompt":"make a cinematic shot","headless":true,"account":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.GenerateVideo(c)

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/generate-vids-video" {
		t.Fatalf("expected /generate-vids-video, got %s", gotPath)
	}
	if gotBody["video_id"] != "vid-123" {
		t.Fatalf("expected video_id vid-123, got %#v", gotBody["video_id"])
	}
	if gotBody["prompt"] != "make a cinematic shot" {
		t.Fatalf("expected prompt, got %#v", gotBody["prompt"])
	}
	if gotBody["headless"] != true {
		t.Fatalf("expected headless true, got %#v", gotBody["headless"])
	}
	if gotBody["account"] != "user" {
		t.Fatalf("expected account user, got %#v", gotBody["account"])
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
	}, zap.NewNop())

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
