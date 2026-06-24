// Package storage — handler_test.go tests the HTTP handler with
// a fake application service. No real Drive, DB, CatalogSync, or job service.
package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
)

// ── Fake DrivePort for thin-transport integration test ──────────────

type appFakeDrive struct {
	files     []appstorage.DriveFile
	listErr   error
	moveErr   error
	createID  string
	createErr error
	renameErr error
}

func (f *appFakeDrive) ListFiles(ctx context.Context, folderID string) ([]appstorage.DriveFile, error) {
	return f.files, f.listErr
}
func (f *appFakeDrive) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return f.moveErr
}
func (f *appFakeDrive) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	return f.createID, f.createErr
}
func (f *appFakeDrive) RenameFile(ctx context.Context, fileID, newName string) error {
	return f.renameErr
}

type appFakeLogger struct{}

func (l *appFakeLogger) Info(msg string, keysAndValues ...any)  {}
func (l *appFakeLogger) Warn(msg string, keysAndValues ...any)  {}
func (l *appFakeLogger) Error(msg string, keysAndValues ...any) {}
func (l *appFakeLogger) Debug(msg string, keysAndValues ...any) {}

// Compile-time: appFakeDrive implements DrivePort.
var _ appstorage.DrivePort = (*appFakeDrive)(nil)

// ── Helper: build test router with wired handler ────────────────────

func newWiredStorageHandler() (*Handler, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	fakeDrive := &appFakeDrive{
		files:    []appstorage.DriveFile{{ID: "1", Name: "test.mp4"}},
		createID: "new-id",
	}
	svc := appstorage.NewService(fakeDrive, &appFakeLogger{})
	handler := NewHandler(svc, nil, nil, zap.NewNop())
	router := gin.New()
	rg := router.Group("/")
	handler.RegisterRoutes(rg)
	return handler, router
}

func newNilServiceHandler() (*Handler, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(nil, nil, nil, zap.NewNop())
	router := gin.New()
	rg := router.Group("/")
	handler.RegisterRoutes(rg)
	return handler, router
}

// ── Route tests ────────────────────────────────────────────────────

func TestStorageRoutes_Compatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// All 4 production routes registered in handler.go RegisterRoutes.
	// With nil service they all return 503 (nil-svc guard fires first).
	tests := []struct {
		method     string
		path       string
		wantStatus int
		reason     string
	}{
		{http.MethodGet, "/drive/files?folder_id=x", http.StatusServiceUnavailable, "nil svc guard fires before query bind"},
		{http.MethodPost, "/drive/move", http.StatusServiceUnavailable, "nil svc guard fires before bind"},
		{http.MethodPost, "/drive/create-folder", http.StatusServiceUnavailable, "nil svc guard fires before bind"},
		{http.MethodPost, "/drive/rename", http.StatusServiceUnavailable, "nil svc guard fires before bind"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			_, router := newNilServiceHandler()

			rec := httptest.NewRecorder()
			req, _ := http.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("expected %d (%s), got %d", tc.wantStatus, tc.reason, rec.Code)
			}
		})
	}
}

func TestStorageRoutes_NoDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	_, router := newNilServiceHandler()

	routes := router.Routes()
	seen := make(map[string]bool)
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if seen[key] {
			t.Errorf("duplicate route: %s", key)
		}
		seen[key] = true
	}
}

// ── Handler thin-transport tests ────────────────────────────────────

func TestHandler_NilServiceReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type ep struct {
		method string
		path   string
	}
	endpoints := []ep{
		{http.MethodGet, "/drive/files?folder_id=x"},
		{http.MethodPost, "/drive/move"},
		{http.MethodPost, "/drive/create-folder"},
		{http.MethodPost, "/drive/rename"},
	}
	for _, e := range endpoints {
		t.Run(e.method+" "+e.path, func(t *testing.T) {
			_, router := newNilServiceHandler()

			rec := httptest.NewRecorder()
			req, _ := http.NewRequest(e.method, e.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("%s %s: expected 503, got %d", e.method, e.path, rec.Code)
			}
		})
	}
}

func TestListFiles_WithQueryParam(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/drive/files?folder_id=root", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	files, ok := resp["files"].([]any)
	if !ok {
		t.Fatalf("expected 'files' array in response, got: %v", resp)
	}
	if len(files) != 1 {
		t.Errorf("files len=%d want 1", len(files))
	}
}

func TestListFiles_MissingFolderID(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/drive/files", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing folder_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMoveFile_ValidJSON(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	body := `{"file_id":"f1","from_folder_id":"src","to_folder_id":"dst"}`
	req, _ := http.NewRequest(http.MethodPost, "/drive/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "moved" {
		t.Errorf("status=%v want moved", resp["status"])
	}
}

func TestCreateFolder_ValidJSON(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	body := `{"name":"my-folder","parent_id":"root"}`
	req, _ := http.NewRequest(http.MethodPost, "/drive/create-folder", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["folder_id"] != "new-id" {
		t.Errorf("folder_id=%v want new-id", resp["folder_id"])
	}
}

func TestRenameFile_ValidJSON(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	body := `{"file_id":"f1","new_name":"renamed.mp4"}`
	req, _ := http.NewRequest(http.MethodPost, "/drive/rename", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "renamed" {
		t.Errorf("status=%v want renamed", resp["status"])
	}
}

func TestHandler_WiredService_NilJobsAndCatalogSync(t *testing.T) {
	// Verify the handler works even with nil jobsSvc and catalogSync.
	fakeDrive := &appFakeDrive{
		files:    []appstorage.DriveFile{{ID: "1", Name: "x"}},
		createID: "id1",
	}
	svc := appstorage.NewService(fakeDrive, &appFakeLogger{})

	t.Run("nil jobs and catalog sync are tolerated", func(t *testing.T) {
		h := NewHandler(svc, nil, nil, zap.NewNop())
		router := gin.New()
		rg := router.Group("/")
		h.RegisterRoutes(rg)

		rec := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/drive/files?folder_id=r", nil)
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
