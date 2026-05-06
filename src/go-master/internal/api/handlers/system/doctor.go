package system

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3"

	"velox/go-master/pkg/config"
)

// Handler handles system diagnostic endpoints
type Handler struct {
	cfg *config.Config
	log *zap.Logger
}

// NewHandler creates a new system handler
func NewHandler(cfg *config.Config, log *zap.Logger) *Handler {
	return &Handler{
		cfg: cfg,
		log: log,
	}
}

// DoctorResponse represents the response from the doctor endpoint
type DoctorResponse struct {
	OK     bool              `json:"ok"`
	Checks map[string]string `json:"checks"`
	Fixes  []string         `json:"fixes,omitempty"`
}

// Doctor godoc
// @Summary System health check
// @Description Check all system prerequisites and dependencies
// @Tags system
// @Accept json
// @Produce json
// @Success 200 {object} DoctorResponse
// @Router /system/doctor [get]
func (h *Handler) Doctor(c *gin.Context) {
	resp := &DoctorResponse{
		OK:     true,
		Checks: make(map[string]string),
		Fixes:  []string{},
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Check data directory
	h.checkDataDir(ctx, resp)

	// Check Ollama
	h.checkOllama(ctx, resp)

	// Check yt-dlp
	h.checkYtDlp(ctx, resp)

	// Check Google token
	h.checkGoogleToken(ctx, resp)

	// Check databases
	h.checkDatabases(ctx, resp)

	// Check Artlist DB
	h.checkArtlistDB(ctx, resp)

	// Determine overall status
	for _, status := range resp.Checks {
		if status != "ok" {
			resp.OK = false
			break
		}
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) checkDataDir(ctx context.Context, resp *DoctorResponse) {
	dataDir := h.cfg.Storage.DataDir
	if dataDir == "" {
		resp.Checks["data_dir"] = "not_configured"
		resp.Fixes = append(resp.Fixes, "Set storage.data_dir in config")
		return
	}

	// Check if directory exists
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		resp.Checks["data_dir"] = "missing"
		resp.Fixes = append(resp.Fixes, fmt.Sprintf("mkdir -p %s", dataDir))
		return
	}

	// Check if writable
	testFile := filepath.Join(dataDir, ".write_test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		resp.Checks["data_dir"] = "not_writable"
		resp.Fixes = append(resp.Fixes, fmt.Sprintf("chmod 755 %s", dataDir))
		return
	}
	os.Remove(testFile)

	resp.Checks["data_dir"] = "ok"
}

func (h *Handler) checkOllama(ctx context.Context, resp *DoctorResponse) {
	// Check if ollama is installed
	_, err := exec.LookPath("ollama")
	if err != nil {
		resp.Checks["ollama"] = "not_installed"
		resp.Fixes = append(resp.Fixes, "Install ollama from https://ollama.com")
		return
	}

	// Check if ollama service is running and has the required model
	cmd := exec.CommandContext(ctx, "ollama", "list")
	output, err := cmd.Output()
	if err != nil {
		resp.Checks["ollama"] = "not_running"
		resp.Fixes = append(resp.Fixes, "Start ollama service")
		return
	}

	// Check for required model
	requiredModel := h.cfg.External.OllamaModel
	if requiredModel == "" {
		requiredModel = "gemma3:4b"
	}

	hasModel := false
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, requiredModel) {
			hasModel = true
			break
		}
	}

	if !hasModel {
		resp.Checks["ollama"] = "missing_model"
		resp.Fixes = append(resp.Fixes, fmt.Sprintf("Run: ollama pull %s", requiredModel))
		return
	}

	resp.Checks["ollama"] = "ok"
}

func (h *Handler) checkYtDlp(ctx context.Context, resp *DoctorResponse) {
	// Check if yt-dlp is installed
	_, err := exec.LookPath("yt-dlp")
	if err != nil {
		resp.Checks["yt_dlp"] = "not_installed"
		resp.Fixes = append(resp.Fixes, "pip install yt-dlp")
		return
	}

	// Check version
	cmd := exec.CommandContext(ctx, "yt-dlp", "--version")
	if err := cmd.Run(); err != nil {
		resp.Checks["yt_dlp"] = "broken"
		resp.Fixes = append(resp.Fixes, "Reinstall yt-dlp: pip install --upgrade yt-dlp")
		return
	}

	resp.Checks["yt_dlp"] = "ok"
}

func (h *Handler) checkGoogleToken(ctx context.Context, resp *DoctorResponse) {
	// Check if Google OAuth token exists and is valid
	tokenPath := filepath.Join(h.cfg.Storage.DataDir, "token.json")
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		resp.Checks["google_token"] = "missing"
		resp.Fixes = append(resp.Fixes, "Run Google OAuth flow to generate token.json")
		return
	}

	// TODO: Add actual token validation by trying to refresh or make a simple API call
	resp.Checks["google_token"] = "ok"
}

func (h *Handler) checkDatabases(ctx context.Context, resp *DoctorResponse) {
	// Check main database
	mainDBPath := filepath.Join(h.cfg.Storage.DataDir, "velox.db.sqlite")
	if _, err := os.Stat(mainDBPath); os.IsNotExist(err) {
		resp.Checks["main_db"] = "missing"
		resp.Fixes = append(resp.Fixes, "Database will be created on first run")
		return
	}

	resp.Checks["main_db"] = "ok"
}

func (h *Handler) checkArtlistDB(ctx context.Context, resp *DoctorResponse) {
	artlistDBPath := filepath.Join(h.cfg.Storage.DataDir, "artlist.db.sqlite")
	if _, err := os.Stat(artlistDBPath); os.IsNotExist(err) {
		resp.Checks["artlist_db"] = "missing"
		resp.Fixes = append(resp.Fixes, "Artlist DB will be created on first run")
		return
	}

	// Try to open the database
	db, err := sql.Open("sqlite3", artlistDBPath)
	if err != nil {
		resp.Checks["artlist_db"] = "corrupted"
		resp.Fixes = append(resp.Fixes, "Check artlist.db.sqlite file")
		return
	}
	defer db.Close()

	// Try a simple query to verify the database is valid
	if err := db.Ping(); err != nil {
		resp.Checks["artlist_db"] = "corrupted"
		resp.Fixes = append(resp.Fixes, "Check artlist.db.sqlite file")
		return
	}

	resp.Checks["artlist_db"] = "ok"
}
