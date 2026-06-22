package common

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// HealthHandler handles health check requests
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
	dbPath := filepath.Join(h.cfg.Storage.DataDir, "media/media.db.sqlite")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=2000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		checks["database"] = gin.H{"ready": false, "error": "cannot open database"}
		allReady = false
	} else {
		defer db.Close()
		if err := db.PingContext(ctx); err != nil {
			checks["database"] = gin.H{"ready": false, "error": "database unreachable"}
			allReady = false
		} else {
			// Verify migrations table exists
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media_assets'").Scan(&count); err != nil || count == 0 {
				checks["database"] = gin.H{"ready": false, "error": "migrations may not be applied"}
				allReady = false
			} else {
				checks["database"] = gin.H{"ready": true}
			}
		}
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
			"status":  "ready",
			"ok":      true,
			"checks":  checks,
		})
	} else {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"ok":     false,
			"checks": checks,
		})
	}
}

// Health godoc
// @Summary Health check
// @Description Check if the server is healthy (fast lightweight check)
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"ok":     true,
	})
}

// Status godoc
// @Summary Server status
// @Description Get detailed server status
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Router /status [get]
func (h *HealthHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"status": "running",
		"mode":   "minimal",
	})
}

// OllamaTimeout godoc
// @Summary Ollama timeout configuration
// @Description Returns the current Ollama timeout configuration for diagnostics. Protected by auth when enabled.
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Router /api/health/ollama-timeout [get]
func (h *HealthHandler) OllamaTimeout(c *gin.Context) {
	if h.cfg == nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":    true,
			"error": "configuration not initialized",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"ollama": gin.H{
			"timeout_seconds": h.cfg.External.OllamaTimeoutSeconds,
			"model":           h.cfg.External.OllamaModel,
		},
		"server_time": time.Now().UTC(),
	})
}

// DeepHealth godoc
// @Summary Deep health check
// @Description Query external dependencies like SQLite and Ollama to verify deep integration status.
// @Description This endpoint returns infrastructure details and is protected by a bearer token.
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Router /api/health/deep [get]
func (h *HealthHandler) DeepHealth(c *gin.Context) {
	if h.cfg == nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "warning",
			"ok":     true,
			"error":  "configuration not initialized",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// 1. Check SQLite Database — use generic status, never expose file path
	dbStart := time.Now()
	var dbStatus = "healthy"
	var dbError string

	dbPath := filepath.Join(h.cfg.Storage.DataDir, "media/media.db.sqlite")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=2000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		dbStatus = "unhealthy"
		dbError = "failed to open database"
	} else {
		defer db.Close()
		if err := db.PingContext(ctx); err != nil {
			dbStatus = "unhealthy"
			dbError = "failed to ping database"
		}
	}
	dbDuration := time.Since(dbStart).Milliseconds()

	// 2. Check Ollama — redact URL in response
	ollamaStart := time.Now()
	var ollamaStatus = "healthy"
	var ollamaError string

	ollamaURL := h.cfg.External.OllamaURL
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}
	pingURL := fmt.Sprintf("%s/api/tags", ollamaURL)
	req, err := http.NewRequestWithContext(ctx, "GET", pingURL, nil)
	if err != nil {
		ollamaStatus = "unhealthy"
		ollamaError = "failed to create request"
	} else {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			ollamaStatus = "unhealthy"
			ollamaError = "failed to connect to Ollama"
		} else {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				ollamaStatus = "unhealthy"
				ollamaError = "Ollama returned non-200 status"
			}
		}
	}
	ollamaDuration := time.Since(ollamaStart).Milliseconds()

	// 3. Check Qdrant — probe /readyz and collection info
	var qdrantInfo gin.H
	if h.cfg.VectorSearch.Enabled {
		qdrantInfo = h.probeQdrant(ctx)
	}

	// Overall status
	status := "healthy"
	if dbStatus == "unhealthy" || ollamaStatus == "unhealthy" {
		status = "unhealthy"
	} else if dbStatus == "warning" || ollamaStatus == "warning" {
		status = "warning"
	}
	if qdrantInfo != nil {
		if qdrantHealthy, _ := qdrantInfo["healthy"].(bool); !qdrantHealthy {
			status = "unhealthy"
		}
	}

	resp := gin.H{
		"ok":     status == "healthy",
		"status": status,
		"database": gin.H{
			"status":      dbStatus,
			"duration_ms": dbDuration,
			"error":       dbError,
		},
		"ollama": gin.H{
			"status":      ollamaStatus,
			"duration_ms": ollamaDuration,
			"error":       ollamaError,
		},
	}
	if qdrantInfo != nil {
		resp["qdrant"] = qdrantInfo
	}

	c.JSON(http.StatusOK, resp)
}

// probeQdrant checks Qdrant health and collection info via HTTP.
func (h *HealthHandler) probeQdrant(ctx context.Context) gin.H {
	start := time.Now()
	qdrantURL := h.cfg.VectorSearch.URL
	if qdrantURL == "" {
		qdrantURL = "http://127.0.0.1:6333"
	}
	collection := h.cfg.VectorSearch.Collection

	client := &http.Client{Timeout: 3 * time.Second}

	// Probe /readyz for liveness
	healthy := true
	var healthErr string

	readyzURL := fmt.Sprintf("%s/readyz", qdrantURL)
	req, err := http.NewRequestWithContext(ctx, "GET", readyzURL, nil)
	if err != nil {
		healthy = false
		healthErr = "failed to create request"
	} else {
		resp, err := client.Do(req)
		if err != nil {
			healthy = false
			healthErr = "failed to connect to Qdrant"
		} else {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				healthy = false
				healthErr = fmt.Sprintf("Qdrant returned HTTP %d", resp.StatusCode)
			}
		}
	}

	// Get collection info (points count)
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

	durationMs := time.Since(start).Milliseconds()

	result := gin.H{
		"healthy":     healthy,
		"enabled":     h.cfg.VectorSearch.Enabled,
		"collection":  collection,
		"duration_ms": durationMs,
	}
	if !healthy {
		result["error"] = healthErr
	}
	if pointsCount >= 0 {
		result["points_count"] = pointsCount
	}

	return result
}
