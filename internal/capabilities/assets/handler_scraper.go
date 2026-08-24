package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ScraperHandler exposes the Node-based Artlist search endpoint.
// It is a sub-handler of the consolidated assets/ package; routes
// remain mounted under /api/scraper/* for backward compatibility.
type ScraperHandler struct {
	nodeScraperDir string
	processRunner  ProcessRunner
}

// NewScraperHandler creates a new scraper handler.
func NewScraperHandler(nodeScraperDir string, processRunner ProcessRunner) *ScraperHandler {
	return &ScraperHandler{nodeScraperDir: nodeScraperDir, processRunner: processRunner}
}

// RegisterRoutes registers /api/scraper routes.
func (h *ScraperHandler) RegisterRoutes(r *gin.RouterGroup) {
	// POST /search removed — Blocco A2 consolidation (June 2026).
	// Unified search is now at POST /api/media/search.
}

type searchRequest struct {
	SearchTerm string `json:"search_term"`
	Term       string `json:"term"`
	Limit      int    `json:"limit"`
}

type clipResult struct {
	Title       string   `json:"title"`
	ClipPageURL string   `json:"clip_page_url"`
	StreamURLs  []string `json:"stream_urls"`
	PrimaryURL  string   `json:"primary_url"`
	ClipID      string   `json:"clip_id"`
}

type searchResponse struct {
	OK        bool         `json:"ok"`
	Term      string       `json:"term"`
	SearchURL string       `json:"search_url"`
	Saved     int          `json:"saved"`
	Clips     []clipResult `json:"clips"`
	Error     string       `json:"error,omitempty"`
	RawStderr string       `json:"raw_stderr,omitempty"`
}

func (h *ScraperHandler) Search(c *gin.Context) {
	if strings.TrimSpace(h.nodeScraperDir) == "" {
		apiutil.Error(c, http.StatusServiceUnavailable, "node scraper directory is not configured")
		return
	}

	var req searchRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		apiutil.BadRequest(c, "invalid JSON payload: "+err.Error())
		return
	}

	term := strings.TrimSpace(req.SearchTerm)
	if term == "" {
		term = strings.TrimSpace(req.Term)
	}
	if term == "" {
		term = strings.TrimSpace(c.Query("search_term"))
	}
	if term == "" {
		term = strings.TrimSpace(c.Query("term"))
	}
	if term == "" {
		apiutil.BadRequest(c, "missing search_term")
		return
	}

	limit := apiutil.ClampLimit(req.Limit, 8, 20)
	if q := strings.TrimSpace(c.Query("limit")); q != "" {
		if parsed, err := strconv.Atoi(q); err == nil && parsed > 0 {
			limit = apiutil.ClampLimit(parsed, 8, 20)
		}
	}

	scraperDir := h.nodeScraperDir
	if absDir, err := filepath.Abs(scraperDir); err == nil {
		scraperDir = absDir
	}
	scriptPath := filepath.Join(scraperDir, "artlist_search.js")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Minute)
	defer cancel()

	args := []string{
		scriptPath,
		"--term", term,
		"--limit", strconv.Itoa(limit),
	}

	result, err := h.processRunner.Run(ctx, "node", args, ProcessOptions{
		WorkDir:        scraperDir,
		CombinedOutput: false,
	})
	if err != nil {
		resp := searchResponse{
			OK:        false,
			Term:      term,
			Error:     err.Error(),
			RawStderr: strings.TrimSpace(result.Stderr),
		}
		c.JSON(http.StatusInternalServerError, resp)
		return
	}

	var payload searchResponse
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("failed to decode scraper response: %v", err),
			"raw":   result.Stdout,
		})
		return
	}

	payload.OK = true
	c.JSON(http.StatusOK, payload)
}
