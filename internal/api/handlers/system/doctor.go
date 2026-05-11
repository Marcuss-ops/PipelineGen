package system

import (
	"context"
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

	"velox/go-master/internal/storage"
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Check data directory
	h.checkDataDir(ctx, resp)

	// Check external tools
	h.checkExternalTools(ctx, resp)

	// Check Google token
	h.checkGoogleToken(ctx, resp)

	// Check all databases (6-module architecture)
	h.checkDatabases(ctx, resp)

	// Check Voiceover service specifically
	h.checkVoiceover(ctx, resp)

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

	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		resp.Checks["data_dir"] = "missing"
		resp.Fixes = append(resp.Fixes, fmt.Sprintf("mkdir -p %s", dataDir))
		return
	}

	resp.Checks["data_dir"] = "ok"
}

func (h *Handler) checkExternalTools(ctx context.Context, resp *DoctorResponse) {
	// Ollama
	if _, err := exec.LookPath("ollama"); err != nil {
		resp.Checks["ollama"] = "not_installed"
	} else {
		resp.Checks["ollama"] = "ok"
	}

	// yt-dlp
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		resp.Checks["yt_dlp"] = "not_installed"
	} else {
		resp.Checks["yt_dlp"] = "ok"
	}

	// ffmpeg
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		resp.Checks["ffmpeg"] = "not_installed"
	} else {
		resp.Checks["ffmpeg"] = "ok"
	}

	// python3
	if _, err := exec.LookPath("python3"); err != nil {
		resp.Checks["python3"] = "not_installed"
	} else {
		resp.Checks["python3"] = "ok"
	}
}

func (h *Handler) checkGoogleToken(ctx context.Context, resp *DoctorResponse) {
	tokenPath := filepath.Join(h.cfg.Storage.DataDir, "token.json")
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		// Try root directory too
		if _, err := os.Stat("token.json"); os.IsNotExist(err) {
			resp.Checks["google_token"] = "missing"
			resp.Fixes = append(resp.Fixes, "Run Google OAuth flow")
			return
		}
	}
	resp.Checks["google_token"] = "ok"
}

func (h *Handler) checkDatabases(ctx context.Context, resp *DoctorResponse) {
	dbs := storage.GetAllDBs()
	for _, dbRelPath := range dbs {
		name := strings.Split(dbRelPath, "/")[0]
		path := storage.GetDBPath(h.cfg.Storage.DataDir, dbRelPath)
		
		key := fmt.Sprintf("db_%s", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			resp.Checks[key] = "missing"
			continue
		}

		// Try to open and ping
		db, err := storage.OpenSQLiteDB(path, h.log)
		if err != nil {
			resp.Checks[key] = "error"
			resp.Fixes = append(resp.Fixes, fmt.Sprintf("Check database: %s", path))
			continue
		}
		
		if err := db.DB.Ping(); err != nil {
			resp.Checks[key] = "unreachable"
		} else {
			resp.Checks[key] = "ok"
		}
		db.Close()
	}
}

func (h *Handler) checkVoiceover(ctx context.Context, resp *DoctorResponse) {
	scriptPath := filepath.Join(h.cfg.Paths.PythonScriptsDir, "tts_edge.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		resp.Checks["voiceover_script"] = "missing"
		resp.Fixes = append(resp.Fixes, "Restore scripts/tts_edge.py")
	} else {
		resp.Checks["voiceover_script"] = "ok"
	}

	// Check edge-tts package
	cmd := exec.CommandContext(ctx, "python3", "-c", "import edge_tts")
	if err := cmd.Run(); err != nil {
		resp.Checks["voiceover_library"] = "missing_edge_tts"
		resp.Fixes = append(resp.Fixes, "pip install edge-tts")
	} else {
		resp.Checks["voiceover_library"] = "ok"
	}
}
