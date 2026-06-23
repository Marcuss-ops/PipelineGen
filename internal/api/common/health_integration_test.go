//go:build integration

package common

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// newTestHealthHandler creates a HealthHandler wired to a real SQLite DB
// in a temp directory. The DB is pre-populated with the core tables that
// the health checks verify (media_assets, jobs).
func newTestHealthHandler(t *testing.T) (*HealthHandler, *gin.Engine) {
	t.Helper()

	dir := t.TempDir()
	mediaDir := filepath.Join(dir, "media")
	require.NoError(t, os.MkdirAll(mediaDir, 0755))

	dbPath := filepath.Join(mediaDir, "media.db.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	require.NoError(t, err)
	defer db.Close()

	// Create tables that the health checks verify.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			name TEXT,
			source TEXT,
			drive_file_id TEXT,
			drive_link TEXT,
			created_at TEXT
		);
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			job_type TEXT,
			status TEXT,
			payload TEXT,
			created_at TEXT,
			updated_at TEXT
		);
	`)
	require.NoError(t, err)

	cfg := &config.Config{}
	cfg.Storage.DataDir = dir
	cfg.VectorSearch.Enabled = false

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewHealthHandler(cfg)
	router.GET("/health", handler.Health)
	return handler, router
}

func doHealthRequest(t *testing.T, router *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/health"
	if query != "" {
		path += "?" + query
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	router.ServeHTTP(w, req)
	return w
}

// ── Basic health (no deep) ────────────────────────────────────────────

func TestHealth_Basic(t *testing.T) {
	_, router := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "healthy", resp["status"])
	assert.True(t, resp["ok"].(bool))
	// Basic mode: no checks key.
	_, hasChecks := resp["checks"]
	assert.False(t, hasChecks, "basic health should not include checks")
}

// ── Deep health (all checks) ──────────────────────────────────────────

func TestHealth_DeepAll(t *testing.T) {
	_, router := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "deep=true")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	checks, ok := resp["checks"].(map[string]any)
	require.True(t, ok, "deep health should include checks map")

	// All four checks should be present.
	for _, name := range []string{"db", "drive", "qdrant", "jobs"} {
		assert.Contains(t, checks, name, "deep health missing check: %s", name)
	}

	// DB and jobs checks should be ok (real DB was created).
	dbCheck := checks["db"].(map[string]any)
	assert.True(t, dbCheck["ok"].(bool), "db check should be ok with real database")

	jobsCheck := checks["jobs"].(map[string]any)
	assert.True(t, jobsCheck["ok"].(bool), "jobs check should be ok with real database")
}

// ── Granular checks ───────────────────────────────────────────────────

func TestHealth_CheckDBOnly(t *testing.T) {
	_, router := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "check=db")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	checks, ok := resp["checks"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, checks, "db")
	assert.NotContains(t, checks, "drive")
	assert.NotContains(t, checks, "qdrant")
	assert.NotContains(t, checks, "jobs")

	dbCheck := checks["db"].(map[string]any)
	assert.True(t, dbCheck["ok"].(bool))
}

func TestHealth_CheckMultiple(t *testing.T) {
	_, router := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "check=db,qdrant")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	checks, ok := resp["checks"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, checks, "db")
	assert.Contains(t, checks, "qdrant")
	assert.NotContains(t, checks, "drive")
	assert.NotContains(t, checks, "jobs")
}

// ── Nil config ────────────────────────────────────────────────────────

func TestHealth_NilConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewHealthHandler(nil)
	router.GET("/health", handler.Health)

	w := doHealthRequest(t, router, "deep=true")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["ok"].(bool))
	assert.Equal(t, "unhealthy", resp["status"])
}

// ── Check response shape per component ─────────────────────────────────

func TestHealth_CheckResponseShape(t *testing.T) {
	_, router := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "check=jobs")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	checks := resp["checks"].(map[string]any)
	jobCheck := checks["jobs"].(map[string]any)

	assert.Contains(t, jobCheck, "ok")
	assert.Contains(t, jobCheck, "duration_ms")
	assert.True(t, jobCheck["ok"].(bool))
}
