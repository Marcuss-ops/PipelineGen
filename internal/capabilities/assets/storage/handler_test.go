// Package storage — handler_test.go tests the HTTP handler with fake
// catalog sync and job services. No real Drive, DB, or job service.
//
// Blocco A1 consolidation (June 2026): the old /drive/* routes
// (files, move-file, create-folder, rename) were moved to the unified
// /api/drive/* admin surface in api/system/handler_drive.go. This file
// now only tests the remaining /sync route.
package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Fake job service for thin-transport test ────────────────────────

type fakeJobService struct{}

func (f *fakeJobService) Enqueue(_ context.Context, req *jobs.EnqueueRequest) (*jobs.Job, error) {
	return &jobs.Job{ID: "test-job"}, nil
}
func (f *fakeJobService) Get(_ context.Context, _ string) (*jobs.Job, error) { return nil, nil }
func (f *fakeJobService) Cancel(_ context.Context, _ string) error           { return nil }
func (f *fakeJobService) List(_ context.Context, _ jobs.Filter) ([]jobs.Job, error) {
	return nil, nil
}
func (f *fakeJobService) IsTerminal(_ jobs.Status) bool         { return false }
func (f *fakeJobService) RegisterHandler(_ string, _ any) error { return nil }
func (f *fakeJobService) ListEvents(_ context.Context, _ string) ([]jobs.Event, error) {
	return nil, nil
}
func (f *fakeJobService) Retry(_ context.Context, _ string) (*jobs.Job, error) {
	return nil, nil
}

// Compile-time: fakeJobService satisfies the full jobs.Service interface.
var _ jobs.Service = (*fakeJobService)(nil)

// ── Helper: build test router with wired handler ────────────────────

func newStorageHandler() (*Handler, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	var jobsSvc jobs.Service = &fakeJobService{}
	var catalogSync *catalogsync.Service // nil — sync route returns 500
	handler := NewHandler(jobsSvc, catalogSync, zap.NewNop())
	router := gin.New()
	rg := router.Group("/")
	handler.RegisterRoutes(rg)
	return handler, router
}

// ── Route tests ────────────────────────────────────────────────────

func TestStorageRoutes_OnlySync(t *testing.T) {
	gin.SetMode(gin.TestMode)

	_, router := newStorageHandler()
	routes := router.Routes()

	// After Blocco A1 consolidation, only POST /sync remains.
	if len(routes) != 1 {
		t.Errorf("expected 1 route (POST /sync), got %d: %+v", len(routes), routes)
	}
	if routes[0].Method != "POST" || routes[0].Path != "/sync" {
		t.Errorf("expected POST /sync, got %s %s", routes[0].Method, routes[0].Path)
	}
}

func TestStorageRoutes_NoDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	_, router := newStorageHandler()
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

func TestSyncRoute_NilCatalogSync(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHandler(&fakeJobService{}, nil, zap.NewNop())
	router := gin.New()
	rg := router.Group("/")
	h.RegisterRoutes(rg)

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/sync", strings.NewReader(`{"drive_folder_id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	// catalogSync is nil → handler returns 500
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 with nil catalogSync, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSyncRoute_MissingFolderID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHandler(&fakeJobService{}, nil, zap.NewNop())
	router := gin.New()
	rg := router.Group("/")
	h.RegisterRoutes(rg)

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/sync", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	// drive_folder_id is required → 400 Bad Request
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing drive_folder_id, got %d: %s", rec.Code, rec.Body.String())
	}
}
