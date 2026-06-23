// Package common provides shared HTTP handlers. The HealthHandler is the
// single consolidated health-check endpoint (PR7, June 2026): it aggregates
// DB, Drive, Qdrant, and JobBroker checks behind GET /health.
//
// Query parameters:
//
//	?deep=true           run all component checks (default: fast ping only)
//	?check=db,drive,...  run only the named checks (implies deep)
//
// Response shape:
//
//	{
//	  "ok": true,
//	  "status": "healthy",
//	  "checks": {
//	    "db":     {"ok": true, "duration_ms": 2},
//	    "drive":  {"ok": true, "duration_ms": 145},
//	    "qdrant": {"ok": true, "duration_ms": 12, "points_count": 1500},
//	    "jobs":   {"ok": true, "duration_ms": 1}
//	  }
//	}
//
// When no deep parameter is supplied the response is the legacy short form:
//
//	{"ok": true, "status": "healthy"}
package common

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// HealthHandler handles health check requests.
type HealthHandler struct {
	cfg *config.Config
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(cfg *config.Config) *HealthHandler {
	return &HealthHandler{cfg: cfg}
}

// Ready godoc
// @Summary Readiness check
// @Description Verifies critical dependencies: database accessibility, migrations applied, config validity.
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /ready [get]
func (h *HealthHandler) Ready(c *gin.Context) {
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"ok":     false,
			"reason": "configuration not initialized",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	checks := gin.H{}
	allReady := true

	// 1. Database accessibility
	dbCheck := h.checkDB(ctx)
	checks["database"] = dbCheck
	if ok, _ := dbCheck["ok"].(bool); !ok {
		allReady = false
	}

	// 2. Config validity (basic)
	if h.cfg.Storage.DataDir == "" {
		checks["config"] = gin.H{"ready": false, "error": "data directory not configured"}
		allReady = false
	} else {
		checks["config"] = gin.H{"ready": true}
	}

	if allReady {
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
			"ok":     true,
			"checks": checks,
		})
	} else {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"ok":     false,
			"checks": checks,
		})
	}
}

// ── Health (GET /health) ──────────────────────────────────────────────

// Health godoc
// @Summary Unified health check
// @Description Single modular health endpoint aggregating DB+Drive+Qdrant+JobBroker.
// @Description Use ?deep=true for full component checks; ?check=db,drive,... for granular.
// @Tags health
// @Accept json
// @Produce json
// @Param deep query bool false "Run all component checks"
// @Param check query string false "Comma-separated list: db,drive,qdrant,jobs"
// @Success 200 {object} map[string]any
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"ok":     false,
			"reason": "configuration not initialized",
		})
		return
	}

	// Determine check depth.
	deep := c.Query("deep") == "true"
	checkParam := strings.TrimSpace(c.Query("check"))
	checksRequested := checkParam != ""

	// Fast path: lightweight ping only.
	if !deep && !checksRequested {
		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
			"ok":     true,
		})
		return
	}

	// Build the allowlist of checks to run.
	allow := map[string]bool{}
	if checksRequested {
		for _, name := range strings.Split(checkParam, ",") {
			allow[strings.TrimSpace(name)] = true
		}
	} else {
		// deep=true without check= → run all.
		allow = map[string]bool{"db": true, "drive": true, "qdrant": true, "jobs": true}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	checks := gin.H{}
	allOK := true

	// ── 1. Database ─────────────────────────────────────────────────
	if allow["db"] {
		checks["db"] = h.checkDB(ctx)
		if ok, _ := checks["db"].(gin.H)["ok"].(bool); !ok {
			allOK = false
		}
	}

	// ── 2. Google Drive ─────────────────────────────────────────────
	if allow["drive"] {
		checks["drive"] = h.checkDrive(ctx)
		if ok, _ := checks["drive"].(gin.H)["ok"].(bool); !ok {
			allOK = false
		}
	}

	// ── 3. Qdrant ───────────────────────────────────────────────────
	if allow["qdrant"] {
		checks["qdrant"] = h.checkQdrant(ctx)
		if ok, _ := checks["qdrant"].(gin.H)["ok"].(bool); !ok {
			allOK = false
		}
	}

	// ── 4. JobBroker ────────────────────────────────────────────────
	if allow["jobs"] {
		checks["jobs"] = h.checkJobBroker(ctx)
		if ok, _ := checks["jobs"].(gin.H)["ok"].(bool); !ok {
			allOK = false
		}
	}

	status := "healthy"
	if !allOK {
		status = "unhealthy"
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     allOK,
		"status": status,
		"checks": checks,
	})
}

// ── Component checks (private) ──────────────────────────────────────

func (h *HealthHandler) checkDB(ctx context.Context) gin.H {
	start := time.Now()
	dbPath := filepath.Join(h.cfg.Storage.DataDir, "media/media.db.sqlite")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=2000"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "cannot open database"}
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "database unreachable"}
	}

	// Verify core table exists.
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media_assets'").Scan(&count); err != nil || count == 0 {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "migrations may not be applied"}
	}

	return gin.H{"ok": true, "duration_ms": time.Since(start).Milliseconds()}
}

func (h *HealthHandler) checkDrive(ctx context.Context) gin.H {
	start := time.Now()

	credPath := h.cfg.GetCredentialsPath()
	tokenPath := h.cfg.GetTokenPath()

	if credPath == "" || tokenPath == "" {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "Drive credentials not configured"}
	}

	// Fast probe: Google Drive v3 about endpoint with a tight timeout.
	// Returns basic user info; a 200 means the token is valid and the API is reachable.
	client := &http.Client{Timeout: 3 * time.Second}

	// Read token to attach as Bearer (simple file read; avoids importing the full
	// OAuth stack into the health handler just for a ping).
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "token file not readable"}
	}

	var tokenData struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(tokenBytes, &tokenData) != nil || tokenData.AccessToken == "" {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "token file invalid or missing access_token"}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/drive/v3/about?fields=user", nil)
	if err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "failed to create Drive request"}
	}
	req.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)

	resp, err := client.Do(req)
	if err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "Drive API unreachable"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": fmt.Sprintf("Drive API returned HTTP %d", resp.StatusCode)}
	}

	return gin.H{"ok": true, "duration_ms": time.Since(start).Milliseconds(), "configured": true}
}

func (h *HealthHandler) checkQdrant(ctx context.Context) gin.H {
	start := time.Now()

	qdrantURL := h.cfg.VectorSearch.URL
	if qdrantURL == "" {
		qdrantURL = "http://127.0.0.1:6333"
	}
	collection := h.cfg.VectorSearch.Collection

	if !h.cfg.VectorSearch.Enabled {
		return gin.H{"ok": true, "duration_ms": time.Since(start).Milliseconds(), "enabled": false, "note": "vector search disabled"}
	}

	client := &http.Client{Timeout: 3 * time.Second}

	// Probe /readyz.
	readyzURL := fmt.Sprintf("%s/readyz", qdrantURL)
	req, err := http.NewRequestWithContext(ctx, "GET", readyzURL, nil)
	if err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "failed to create request"}
	}

	resp, err := client.Do(req)
	if err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "failed to connect to Qdrant"}
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": fmt.Sprintf("Qdrant returned HTTP %d", resp.StatusCode)}
	}

	// Get collection points count.
	var pointsCount int64 = -1
	collURL := fmt.Sprintf("%s/collections/%s", qdrantURL, collection)
	req2, err := http.NewRequestWithContext(ctx, "GET", collURL, nil)
	if err == nil {
		resp2, err := client.Do(req2)
		if err == nil {
			defer resp2.Body.Close()
			if resp2.StatusCode == http.StatusOK {
				var collResp struct {
					Result struct {
						PointsCount int64 `json:"points_count"`
					} `json:"result"`
				}
				if json.NewDecoder(resp2.Body).Decode(&collResp) == nil {
					pointsCount = collResp.Result.PointsCount
				}
			}
		}
	}

	result := gin.H{
		"ok":          true,
		"enabled":     true,
		"collection":  collection,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	if pointsCount >= 0 {
		result["points_count"] = pointsCount
	}

	return result
}

func (h *HealthHandler) checkJobBroker(ctx context.Context) gin.H {
	start := time.Now()

	// JobBroker health: verify the media DB is reachable and the jobs table
	// exists. This is a lightweight proxy — the real broker liveness is
	// reflected in whether jobs can be enqueued (checked via the DB path).
	dbPath := filepath.Join(h.cfg.Storage.DataDir, "media/media.db.sqlite")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=2000"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "cannot open database"}
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "database unreachable"}
	}

	// Verify jobs table exists.
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'").Scan(&count); err != nil || count == 0 {
		return gin.H{"ok": false, "duration_ms": time.Since(start).Milliseconds(), "error": "jobs table not found"}
	}

	return gin.H{"ok": true, "duration_ms": time.Since(start).Milliseconds()}
}
