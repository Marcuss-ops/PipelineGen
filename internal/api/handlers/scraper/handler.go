package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	nodeScraperDir string
}

func NewHandler(nodeScraperDir string) *Handler {
	return &Handler{nodeScraperDir: nodeScraperDir}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/search", h.Search)
}

type searchRequest struct {
	SearchTerm string `json:"search_term"`
	Term       string `json:"term"`
	Limit      int    `json:"limit"`
	SaveDB     bool   `json:"save_db"`
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

func (h *Handler) Search(c *gin.Context) {
	if strings.TrimSpace(h.nodeScraperDir) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "node scraper directory is not configured"})
		return
	}

	var req searchRequest
	_ = c.ShouldBindJSON(&req)

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
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "missing search_term"})
		return
	}

	limit := req.Limit
	if limit <= 0 {
		if q := strings.TrimSpace(c.Query("limit")); q != "" {
			if parsed, err := strconv.Atoi(q); err == nil && parsed > 0 {
				limit = parsed
			}
		}
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}

	saveDB := req.SaveDB
	if !saveDB {
		saveDB = strings.EqualFold(strings.TrimSpace(c.Query("save_db")), "true") || strings.EqualFold(strings.TrimSpace(c.Query("save")), "true")
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
	if saveDB {
		args = append(args, "--save-db")
	}

	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = scraperDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		resp := searchResponse{
			OK:        false,
			Term:      term,
			Error:     err.Error(),
			RawStderr: strings.TrimSpace(stderr.String()),
		}
		c.JSON(http.StatusInternalServerError, resp)
		return
	}

	var payload searchResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("failed to decode scraper response: %v", err),
			"raw":   stdout.String(),
		})
		return
	}

	payload.OK = true
	c.JSON(http.StatusOK, payload)
}
