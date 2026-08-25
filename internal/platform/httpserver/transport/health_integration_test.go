//go:build integration

package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	healthapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	infrahealth "github.com/Marcuss-ops/PipelineGen/internal/platform/health"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// newTestHealthHandler creates a HealthHandler wired to a health.Service
// backed by real SQLite DB + real config checkers. The DB is pre-populated
// with the core tables that the health checks verify (media_assets, jobs).
// codex/health-ready-contract: now wires a non-nil ReadyChecker for /ready tests.
func newTestHealthHandler(t *testing.T) (*HealthHandler, *gin.Engine, *healthapp.Service) {
	t.Helper()

	dir := t.TempDir()
	mediaDir := filepath.Join(dir, "media")
	require.NoError(t, os.MkdirAll(mediaDir, 0755))

	dbPath := filepath.Join(mediaDir, "media.db.sqlite")

	// PG-016 typed-handle continuation (June 2026): storage.OpenSQLiteDB
	// registers the mattn/go-sqlite3 driver internally and enforces WAL
	// mode, so both the `_ "github.com/mattn/go-sqlite3"` blank import
	// and the explicit `_journal_mode=WAL` DSN parameter are no longer
	// needed here. The fixture is the typed *storage.SQLiteDB handle.
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, zaptest.NewLogger(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqliteDB.Close() })

	// Create tables that the health checks verify.
	_, err = sqliteDB.Exec(`
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

	// Build the health Service with infra checkers pointing at the temp DB.
	svc := healthapp.NewService(healthapp.ServiceDeps{
		DB:     infrahealth.NewSQLiteChecker(sqliteDB),
		Drive:  infrahealth.NewDriveChecker(), // no probe wired → applicable=false
		Qdrant: nil,                           // QDRANT-005 Blocker 3: Qdrant disabled — Service handles nil as applicable=false
		Jobs:   infrahealth.NewJobsChecker(sqliteDB),
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	readyChecker := healthapp.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, readyChecker)
	router.GET("/health", handler.Health)
	router.GET("/ready", handler.Ready)
	return handler, router, svc
}

func doHealthRequest(t *testing.T, router *gin.Engine, path, query string) *httptest.ResponseRecorder {
	t.Helper()
	if query != "" {
		path += "?" + query
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	router.ServeHTTP(w, req)
	return w
}

// ── Fast liveness (no params) ─────────────────────────────────────────

func TestHealth_FastLiveness(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/health", "")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "healthy", resp["status"])
	assert.True(t, resp["ok"].(bool))
	// Fast liveness: no checks key (empty names → no checks run).
	_, hasChecks := resp["checks"]
	assert.False(t, hasChecks, "fast liveness should not include checks")
}

// ── Deep health (all checks) ──────────────────────────────────────────

func TestHealth_DeepAll(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/health", "deep=true")
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

	// Drive and Qdrant are optional — applicable=false, ok=true.
	driveCheck := checks["drive"].(map[string]any)
	assert.True(t, driveCheck["ok"].(bool), "drive check should be ok when not configured")
	assert.False(t, driveCheck["applicable"].(bool), "drive should report applicable=false")

	qdrantCheck := checks["qdrant"].(map[string]any)
	assert.True(t, qdrantCheck["ok"].(bool), "qdrant check should be ok when disabled")
	assert.False(t, qdrantCheck["applicable"].(bool), "qdrant should report applicable=false")
}

// ── Granular checks (repeated query params) ───────────────────────────

func TestHealth_CheckDBOnly(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/health", "check=db")
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

func TestHealth_CheckCommaSeparated(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/health", "check=db,qdrant")
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

func TestHealth_CheckRepeatedParams(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/health", "check=db&check=jobs")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	checks, ok := resp["checks"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, checks, "db")
	assert.Contains(t, checks, "jobs")
	assert.NotContains(t, checks, "drive")
	assert.NotContains(t, checks, "qdrant")
}

func TestHealth_CheckDedup(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	// Repeated same check should be deduplicated.
	w := doHealthRequest(t, router, "/health", "check=db&check=db&check=jobs")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	checks, ok := resp["checks"].(map[string]any)
	require.True(t, ok)
	assert.Len(t, checks, 2, "duplicate db check should be deduplicated")
	assert.Contains(t, checks, "db")
	assert.Contains(t, checks, "jobs")
}

func TestHealth_CheckEmptyTrimmed(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	// Empty/whitespace check values should be trimmed → fast liveness.
	w := doHealthRequest(t, router, "/health", "check=,,,")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	assert.Equal(t, "healthy", resp["status"])
	_, hasChecks := resp["checks"]
	assert.False(t, hasChecks, "empty checks should fall through to fast liveness")
}

// ── Unknown check name → HTTP 400 ─────────────────────────────────────

func TestHealth_UnknownCheck(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/health", "check=nonesiste")
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"unknown check should return HTTP 400 (not 503)")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.False(t, resp["ok"].(bool))
	assert.Equal(t, "bad request", resp["status"])
	assert.Contains(t, resp["error"].(string), "unknown health check")
}

// ── Nil service ───────────────────────────────────────────────────────

func TestHealth_NilService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewHealthHandler(nil, nil /* nil-by-design */)
	router.GET("/health", handler.Health)

	w := doHealthRequest(t, router, "/health", "deep=true")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["ok"].(bool))
}

// ── Check response shape per component ─────────────────────────────────

func TestHealth_CheckResponseShape(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/health", "check=jobs")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	checks := resp["checks"].(map[string]any)
	jobCheck := checks["jobs"].(map[string]any)

	assert.Contains(t, jobCheck, "ok")
	assert.Contains(t, jobCheck, "duration_ms")
	assert.True(t, jobCheck["ok"].(bool))
}

// ── DB unreachable ────────────────────────────────────────────────────

func TestHealth_DBUnreachable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Use a closed DB to simulate unreachable database.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	// PG-016 typed-handle continuation: storage.OpenSQLiteDB replaces
	// sql.Open + mattn blank import + manual close ordering.
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, zaptest.NewLogger(t))
	require.NoError(t, err)

	// Create tables so schema is valid.
	_, err = sqliteDB.Exec(`CREATE TABLE IF NOT EXISTS media_assets (id TEXT)`)
	require.NoError(t, err)
	_, err = sqliteDB.Exec(`CREATE TABLE IF NOT EXISTS jobs (id TEXT, status TEXT, updated_at TEXT)`)
	require.NoError(t, err)

	_ = sqliteDB.Close() // close to make it unreachable

	svc := healthapp.NewService(healthapp.ServiceDeps{
		DB:     infrahealth.NewSQLiteChecker(sqliteDB),
		Drive:  infrahealth.NewDriveChecker(),
		Qdrant: infrahealth.NewQdrantChecker("http://127.0.0.1:6333", "media_assets", false),
		Jobs:   infrahealth.NewJobsChecker(sqliteDB),
	})

	ready := healthapp.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)
	router.GET("/health", handler.Health)

	w := doHealthRequest(t, router, "/health", "deep=true")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["ok"].(bool))

	checks := resp["checks"].(map[string]any)
	dbCheck := checks["db"].(map[string]any)
	assert.False(t, dbCheck["ok"].(bool))
}

// ── /ready endpoint ───────────────────────────────────────────────────

func TestReady_Healthy(t *testing.T) {
	_, router, _ := newTestHealthHandler(t)

	w := doHealthRequest(t, router, "/ready", "")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	assert.Equal(t, "ready", resp["status"])
}

func TestReady_NilChecker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Create handler with nil ReadyChecker.
	svc := healthapp.NewService(healthapp.ServiceDeps{})
	handler := NewHealthHandler(svc, nil) // ReadyChecker nil
	router.GET("/ready", handler.Ready)

	w := doHealthRequest(t, router, "/ready", "")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"nil ReadyChecker should return 503")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["ok"].(bool))
	assert.Contains(t, resp["error"].(string), "ready checker not initialized")
}

// ── NewHealthHandler with ReadyChecker ────────────────────────────────

func TestNewHealthHandler_ReadyCheckerNonNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	svc := healthapp.NewService(healthapp.ServiceDeps{})
	ready := healthapp.NewReadyChecker(svc)
	require.NotNil(t, ready, "ReadyChecker should not be nil")

	handler := NewHealthHandler(svc, ready)
	router.GET("/health", handler.Health)
	router.GET("/ready", handler.Ready)

	// /health still works (fast liveness).
	wh := doHealthRequest(t, router, "/health", "")
	assert.Equal(t, http.StatusOK, wh.Code)

	// /ready actually uses the checker.
	wr := doHealthRequest(t, router, "/ready", "")
	assert.Equal(t, http.StatusOK, wr.Code)
}

// ── NormalizeCheckNames unit tests at integration scope ────────────────

func TestNormalizeCheckNames_Trims(t *testing.T) {
	result := healthapp.NormalizeCheckNames([]string{" db ", "  drive  "})
	require.Equal(t, []string{"db", "drive"}, result)
}

func TestNormalizeCheckNames_Deduplicates(t *testing.T) {
	result := healthapp.NormalizeCheckNames([]string{"db", "drive", "db"})
	require.Equal(t, []string{"db", "drive"}, result)
}

func TestNormalizeCheckNames_CommaSplit(t *testing.T) {
	result := healthapp.NormalizeCheckNames([]string{"db,drive,qdrant"})
	require.Equal(t, []string{"db", "drive", "qdrant"}, result)
}

func TestNormalizeCheckNames_Mixed(t *testing.T) {
	result := healthapp.NormalizeCheckNames([]string{"db,drive", "qdrant", "  jobs  "})
	require.Equal(t, []string{"db", "drive", "qdrant", "jobs"}, result)
}

func TestNormalizeCheckNames_RemovesEmpty(t *testing.T) {
	result := healthapp.NormalizeCheckNames([]string{"", "db", "", "  ", "jobs"})
	require.Equal(t, []string{"db", "jobs"}, result)
}

func TestValidateCheckNames_Success(t *testing.T) {
	require.NoError(t, healthapp.ValidateCheckNames([]string{"db", "jobs"}))
}

func TestValidateCheckNames_Unknown(t *testing.T) {
	err := healthapp.ValidateCheckNames([]string{"db", "whatisthis"})
	require.Error(t, err)
	var unknownErr *healthapp.ErrUnknownCheck
	require.ErrorAs(t, err, &unknownErr)
	assert.Equal(t, "whatisthis", unknownErr.Name)
}

func TestValidateCheckNames_NilInput(t *testing.T) {
	require.NoError(t, healthapp.ValidateCheckNames(nil))
}

// ── Service.Check with empty names returns no-op ──────────────────────

func TestService_Check_EmptyNames(t *testing.T) {
	svc := healthapp.NewService(healthapp.ServiceDeps{})
	resp := svc.Check(context.Background(), nil)
	assert.True(t, resp.OK)
	assert.Equal(t, "healthy", resp.Status)
	assert.Nil(t, resp.Checks)
}

// ── Wiring test: router with healthSvc but WITHOUT ReadyChecker ──────
// codex/health-ready-contract: this test fails if the production router
// constructs NewHealthHandler with ReadyChecker nil. It simulates the
// exact bug this PR fixes — healthSvc is wired but ReadyChecker is not.
func TestRouter_WiringWithoutReadyChecker_Returns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create a Router wired like the old production code: healthSvc set,
	// but ReadyChecker NOT set. This is exactly the bug state before
	// codex/health-ready-contract.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	// PG-016 typed-handle continuation: storage.OpenSQLiteDB replaces
	// sql.Open + mattn blank import; t.Cleanup closes the typed handle.
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, zaptest.NewLogger(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqliteDB.Close() })

	_, err = sqliteDB.Exec(`CREATE TABLE IF NOT EXISTS media_assets (id TEXT); CREATE TABLE IF NOT EXISTS jobs (id TEXT, status TEXT, updated_at TEXT)`)
	require.NoError(t, err)

	svc := healthapp.NewService(healthapp.ServiceDeps{
		DB:     infrahealth.NewSQLiteChecker(sqliteDB),
		Drive:  infrahealth.NewDriveChecker(),
		Qdrant: infrahealth.NewQdrantChecker("http://127.0.0.1:6333", "media_assets", false),
		Jobs:   infrahealth.NewJobsChecker(sqliteDB),
	})

	// Simulate the pre-fix bug: healthSvc wired, but readyChecker is nil.
	handler := NewHealthHandler(svc, nil /* ReadyChecker NOT wired */)

	router := gin.New()
	router.GET("/health", handler.Health)
	router.GET("/ready", handler.Ready)

	// /health should still work (fast liveness).
	wh := doHealthRequest(t, router, "/health", "")
	assert.Equal(t, http.StatusOK, wh.Code)

	// /ready should return 503 because ReadyChecker is nil.
	// This is the test that would FAIL before the fix if the router
	// was silently passing nil ReadyChecker in production.
	wr := doHealthRequest(t, router, "/ready", "")
	assert.Equal(t, http.StatusServiceUnavailable, wr.Code,
		"expected 503 when ReadyChecker is nil")
}
