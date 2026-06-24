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

	tests := []struct {
		method      string
		path        string
		wantStatus  int    // Expected status with nil service and empty JSON body.
		reason      string
	}{
		{http.MethodPost, "/storage/files", http.StatusServiceUnavailable, "nil svc guard fires before bind"},
		{http.MethodPost, "/storage/files/move", http.StatusServiceUnavailable, "nil svc guard fires before bind"},
		{http.MethodPost, "/storage/files/rename", http.StatusServiceUnavailable, "nil svc guard fires before bind"},
		{http.MethodPost, "/storage/folders", http.StatusServiceUnavailable, "nil svc guard fires before bind"},
		// local-to-drive and sync-drive-folder do ShouldBindJSON before
		// nil-checking h.jobsSvc, so empty JSON body returns 400.
		{http.MethodPost, "/storage/local-to-drive", http.StatusBadRequest, "bind fires before nil-svc guard"},
		{http.MethodPost, "/storage/sync-drive-folder", http.StatusBadRequest, "bind fires before nil-svc guard"},
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

	endpoints := []string{
		"/storage/files",
		"/storage/files/move",
		"/storage/files/rename",
		"/storage/folders",
	}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			_, router := newNilServiceHandler()

			rec := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, ep, strings.NewReader(`{"folder_id":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("%s: expected 503, got %d", ep, rec.Code)
			}
		})
	}
}

func TestListFiles_ValidJSON(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	body := `{"folder_id":"root"}`
	req, _ := http.NewRequest(http.MethodPost, "/storage/files", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["count"] != float64(1) {
		t.Errorf("count=%v want 1", resp["count"])
	}
}

func TestListFiles_MalformedJSON(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/storage/files", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed JSON, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMoveFiles_MissingRequiredField(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	// Missing "file_ids" and "to_folder_id" (both binding:required).
	body := `{}`
	req, _ := http.NewRequest(http.MethodPost, "/storage/files/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing required fields, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateFolder_ValidJSON(t *testing.T) {
	_, router := newWiredStorageHandler()

	rec := httptest.NewRecorder()
	body := `{"name":"my-folder","parent_id":"root"}`
	req, _ := http.NewRequest(http.MethodPost, "/storage/folders", strings.NewReader(body))
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
	req, _ := http.NewRequest(http.MethodPost, "/storage/files/rename", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["new_name"] != "renamed.mp4" {
		t.Errorf("new_name=%v want renamed.mp4", resp["new_name"])
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
		req, _ := http.NewRequest(http.MethodPost, "/storage/files", strings.NewReader(`{"folder_id":"r"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
