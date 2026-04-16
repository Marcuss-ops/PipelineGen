package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ScraperHandler manages the Node.js scraper integration
type ScraperHandler struct {
	scraperDir string
	nodeBin    string
}

// NewScraperHandler creates a new scraper handler
func NewScraperHandler(scraperDir, nodeBin string) *ScraperHandler {
	return &ScraperHandler{
		scraperDir: scraperDir,
		nodeBin:    nodeBin,
	}
}

// ScraperRequest represents a request to search/clip videos
type ScraperRequest struct {
	SearchTerm string `json:"search_term" binding:"required"`
	MaxPages   int    `json:"max_pages"`
	Source     string `json:"source"` // "artlist", "pixabay", "pexels"
	Category   string `json:"category"`
}

// ScraperStatsResponse represents scraper statistics
type ScraperStatsResponse struct {
	Categories    []CategoryStats `json:"categories"`
	TotalVideos   int             `json:"total_videos"`
	TotalDownloaded int           `json:"total_downloaded"`
	TotalSizeGB   float64         `json:"total_size_gb"`
}

// CategoryStats represents stats for a single category
type CategoryStats struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SearchTerms int    `json:"search_terms"`
	Scraped     int    `json:"scraped"`
	Pending     int    `json:"pending"`
	TotalVideos int    `json:"total_videos"`
}

// VideoClip represents a found video
type VideoClip struct {
	Source     string  `json:"source"`
	ID         int     `json:"id"`
	Name       string  `json:"name"`
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	URL        string  `json:"url"`
	Thumbnail  string  `json:"thumbnail,omitempty"`
}

// sanitizeInput removes potentially dangerous characters from user input
func sanitizeInput(input string) string {
	// Remove path traversal sequences
	input = strings.ReplaceAll(input, "../", "")
	input = strings.ReplaceAll(input, "..\\", "")
	// Remove shell special characters that could enable injection
	input = strings.ReplaceAll(input, ";", "")
	input = strings.ReplaceAll(input, "&", "")
	input = strings.ReplaceAll(input, "|", "")
	input = strings.ReplaceAll(input, "$", "")
	input = strings.ReplaceAll(input, "`", "")
	input = strings.ReplaceAll(input, "(", "")
	input = strings.ReplaceAll(input, ")", "")
	input = strings.ReplaceAll(input, "{", "")
	input = strings.ReplaceAll(input, "}", "")
	input = strings.ReplaceAll(input, "[", "")
	input = strings.ReplaceAll(input, "]", "")
	input = strings.ReplaceAll(input, "<", "")
	input = strings.ReplaceAll(input, ">", "")
	input = strings.ReplaceAll(input, "!", "")
	input = strings.ReplaceAll(input, "#", "")
	// Remove spaces and replace with underscores
	input = strings.ReplaceAll(input, " ", "_")
	return input
}

// validateSafePath ensures the path is within the intended directory
func validateSafePath(baseDir, fullPath string) error {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("invalid base directory: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if !strings.HasPrefix(absPath, absBase) {
		return fmt.Errorf("path traversal detected: %s is outside %s", absPath, absBase)
	}
	return nil
}

// SearchVideos searches for videos using Artlist GraphQL
// POST /api/scraper/search
func (h *ScraperHandler) SearchVideos(c *gin.Context) {
	var req ScraperRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Sanitize user input to prevent path traversal and command injection
	req.SearchTerm = sanitizeInput(req.SearchTerm)
	if req.SearchTerm == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid search term"})
		return
	}

	if req.MaxPages == 0 {
		req.MaxPages = 5
	}
	if req.Source == "" {
		req.Source = "artlist"
	}

	// Determine the script to run based on source
	scriptName := fmt.Sprintf("map_%s.js", req.Source)
	scriptPath := filepath.Join(h.scraperDir, "scripts", scriptName)

	// Validate the script exists before attempting to run
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("Scraper script not found for source '%s': %s", req.Source, scriptPath),
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build command args based on source
	args := []string{scriptPath, req.SearchTerm, strconv.Itoa(req.MaxPages), "--no-download"}

	cmd := exec.CommandContext(ctx, h.nodeBin, args...)
	cmd.Dir = h.scraperDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":     false,
			"error":  fmt.Sprintf("Failed to run scraper: %v", err),
			"stderr": stderr.String(),
		})
		return
	}

	// Read the output JSON file
	outputFile := fmt.Sprintf("%s_%s_mapping.json", req.Source, req.SearchTerm)
	outputPath := filepath.Join(h.scraperDir, "Output", outputFile)

	// Validate the output path is within the intended directory
	if err := validateSafePath(h.scraperDir, outputPath); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid output path"})
		return
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": fmt.Sprintf("Failed to read output: %v", err)})
		return
	}

	var clips []VideoClip
	if err := json.Unmarshal(data, &clips); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to parse output: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"search_term": req.SearchTerm,
		"source":      req.Source,
		"max_pages":   req.MaxPages,
		"clips_found": len(clips),
		"clips":       clips,
	})
}

// GetScraperStats returns statistics about the scraper database
// GET /api/scraper/stats
func (h *ScraperHandler) GetScraperStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.nodeBin, "scripts/cli.js", "help")
	cmd.Dir = h.scraperDir

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": fmt.Sprintf("Failed to get stats: %v", err)})
		return
	}

	// Return basic info - in production, you'd query the SQLite DB directly
	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"scraper":    "node-scraper",
		"status":     "ready",
		"output_dir": "src/node-scraper/Output",
		"database":   "src/node-scraper/artlist_videos.db",
	})
}

// SeedDatabase seeds the scraper database with initial data
// POST /api/scraper/seed
func (h *ScraperHandler) SeedDatabase(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.nodeBin, "scripts/cli.js", "seed")
	cmd.Dir = h.scraperDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":     false,
			"error":  fmt.Sprintf("Failed to seed database: %v", err),
			"stderr": stderr.String(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"status":  "success",
		"message": "Database seeded successfully",
		"output":  stdout.String(),
	})
}

// RegisterRoutes registers all scraper-related routes
func (h *ScraperHandler) RegisterRoutes(router *gin.RouterGroup) {
	scraperGroup := router.Group("/scraper")
	{
		scraperGroup.POST("/search", h.SearchVideos)
		scraperGroup.GET("/stats", h.GetScraperStats)
		scraperGroup.POST("/seed", h.SeedDatabase)
		scraperGroup.GET("/categories", h.ListCategories)
		scraperGroup.POST("/download", h.DownloadClips)
	}
}

// ListCategories lists all categories in the scraper database
// GET /api/scraper/categories
func (h *ScraperHandler) ListCategories(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.nodeBin, "scripts/cli.js", "categories")
	cmd.Dir = h.scraperDir

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": fmt.Sprintf("Failed to list categories: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"categories": stdout.String(),
	})
}

// DownloadClips downloads pending video clips
// POST /api/scraper/download
func (h *ScraperHandler) DownloadClips(c *gin.Context) {
	var req struct {
		Category string `json:"category"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Sanitize category to prevent command injection
	if req.Category != "" {
		req.Category = sanitizeInput(req.Category)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	args := []string{"scripts/cli.js", "download"}
	if req.Category != "" {
		args = append(args, req.Category)
	}

	cmd := exec.CommandContext(ctx, h.nodeBin, args...)
	cmd.Dir = h.scraperDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  fmt.Sprintf("Failed to download: %v", err),
			"stderr": stderr.String(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"output": stdout.String(),
	})
}
