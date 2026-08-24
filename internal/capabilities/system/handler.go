// Package system (api/system) — SystemHandler handles the `/system/doctor`
// diagnostic endpoint. Wave 14 PR4 cleanup (June 24, 2026): the handler
// no longer imports `internal/platform/config`. The previous `*config.Config`
// dependency was replaced with a typed `DoctorConfig` struct populated at the
// composition root by `internal/app/system_adapters.go::doctorConfigFrom(*config.Config)`,
// keeping api/system a thin transport per AGENTS.md Pattern 8.
package system

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// DoctorConfig is the typed snapshot of configuration fields the
// `/system/doctor` endpoint reads. The composition root populates it
// from `*config.Config` and passes it by value — the handler has no
// other reason to know about the canonical config struct.
//
// Each field is computed eagerly at composition time (paths are
// pre-resolved strings, not method receivers) so the handler is a
// pure data consumer and easy to test.
type DoctorConfig struct {
	DataDir                   string
	AssetsPath                string
	ImagesPath                string
	TempPath                  string
	AnimationsPath            string
	YoutubeClipsPath          string
	PythonScriptsDir          string
	GoogleAccountingEnabled   bool
	GoogleAccountingServerURL string
}

// SystemHandler handles system diagnostic endpoints.
type SystemHandler struct {
	cfg             DoctorConfig
	log             *zap.Logger
	toolChecker     appassets.ToolChecker
	processRunner   appassets.ProcessRunner
	dbHealthChecker appassets.DBHealthChecker
}

// NewSystemHandler creates a new system handler.
func NewSystemHandler(cfg DoctorConfig, log *zap.Logger, toolChecker appassets.ToolChecker, processRunner appassets.ProcessRunner, dbHealthChecker appassets.DBHealthChecker) *SystemHandler {
	return &SystemHandler{
		cfg:             cfg,
		log:             log,
		toolChecker:     toolChecker,
		processRunner:   processRunner,
		dbHealthChecker: dbHealthChecker,
	}
}

// DoctorResponse represents the response from the doctor endpoint.
type DoctorResponse struct {
	OK      bool                     `json:"ok"`
	Checks  map[string]string        `json:"checks"`
	Storage map[string]StorageStatus `json:"storage,omitempty"`
	Fixes   []string                 `json:"fixes,omitempty"`
}

// StorageStatus describes the writability/existence of one storage path.
type StorageStatus struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Writable bool   `json:"writable"`
	Error    string `json:"error,omitempty"`
}

// Doctor godoc
// @Summary System health check
// @Description Check all system prerequisites and dependencies
// @Tags system
// @Accept json
// @Produce json
// @Success 200 {object} DoctorResponse
// @Router /system/doctor [get]
func (h *SystemHandler) Doctor(c *gin.Context) {
	resp := &DoctorResponse{
		OK:      true,
		Checks:  make(map[string]string),
		Storage: make(map[string]StorageStatus),
		Fixes:   []string{},
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Check storage directories deeply
	h.checkStorageDeep(ctx, resp)

	// Check external tools
	h.checkExternalTools(ctx, resp)

	// Check Google token
	h.checkGoogleToken(ctx, resp)

	// Check all databases (6-module architecture)
	h.checkDatabases(ctx, resp)

	// Check Voiceover service specifically
	h.checkVoiceover(ctx, resp)

	// Check Google Accounting service
	h.checkGoogleAccounting(ctx, resp)

	// Determine overall status
	for _, status := range resp.Checks {
		if status != "ok" {
			resp.OK = false
			break
		}
	}
	for _, s := range resp.Storage {
		if !s.Exists || !s.Writable {
			resp.OK = false
			break
		}
	}

	c.JSON(http.StatusOK, resp)
}

func (h *SystemHandler) checkStorageDeep(ctx context.Context, resp *DoctorResponse) {
	dirs := map[string]string{
		"data_dir":      h.cfg.DataDir,
		"assets_dir":    h.cfg.AssetsPath,
		"images_dir":    h.cfg.ImagesPath,
		"temp_dir":      h.cfg.TempPath,
		"animations":    h.cfg.AnimationsPath,
		"youtube_clips": h.cfg.YoutubeClipsPath,
	}

	for key, path := range dirs {
		status := StorageStatus{Path: path}

		if _, err := os.Stat(path); err == nil {
			status.Exists = true

			// Check writability by creating a temp file
			tmpFile := filepath.Join(path, ".velox_write_test")
			if err := os.WriteFile(tmpFile, []byte("test"), 0644); err == nil {
				status.Writable = true
				_ = os.Remove(tmpFile)
			} else {
				status.Writable = false
				status.Error = err.Error()
				resp.Fixes = append(resp.Fixes, fmt.Sprintf("chmod +w %s", path))
			}
		} else {
			status.Exists = false
			status.Error = err.Error()
			resp.Fixes = append(resp.Fixes, fmt.Sprintf("mkdir -p %s", path))
		}

		resp.Storage[key] = status
	}
}

func (h *SystemHandler) checkExternalTools(ctx context.Context, resp *DoctorResponse) {
	// Ollama
	if !h.toolChecker.CommandExists("ollama") {
		resp.Checks["ollama"] = "not_installed"
	} else {
		resp.Checks["ollama"] = "ok"
	}

	// yt-dlp
	if !h.toolChecker.CommandExists("yt-dlp") {
		resp.Checks["yt_dlp"] = "not_installed"
	} else {
		resp.Checks["yt_dlp"] = "ok"
	}

	// ffmpeg
	if !h.toolChecker.CommandExists("ffmpeg") {
		resp.Checks["ffmpeg"] = "not_installed"
	} else {
		resp.Checks["ffmpeg"] = "ok"
	}

	// python3
	if !h.toolChecker.CommandExists("python3") {
		resp.Checks["python3"] = "not_installed"
	} else {
		resp.Checks["python3"] = "ok"
	}
}

func (h *SystemHandler) checkGoogleToken(ctx context.Context, resp *DoctorResponse) {
	tokenPath := filepath.Join(h.cfg.DataDir, "token.json")
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

func (h *SystemHandler) checkDatabases(ctx context.Context, resp *DoctorResponse) {
	dbs := h.dbHealthChecker.GetAllDBs()
	for _, dbRelPath := range dbs {
		name := strings.Split(dbRelPath, "/")[0]
		path := h.dbHealthChecker.GetDBPath(h.cfg.DataDir, dbRelPath)

		key := fmt.Sprintf("db_%s", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			resp.Checks[key] = "missing"
			continue
		}

		// Try to open and ping
		result := h.dbHealthChecker.Ping(ctx, path)
		if result.Error != "" {
			resp.Checks[key] = "error"
			resp.Fixes = append(resp.Fixes, fmt.Sprintf("Check database: %s", path))
		} else if !result.OK {
			resp.Checks[key] = "unreachable"
		} else {
			resp.Checks[key] = "ok"
		}
	}
}

func (h *SystemHandler) checkVoiceover(ctx context.Context, resp *DoctorResponse) {
	scriptPath := filepath.Join(h.cfg.PythonScriptsDir, "bridges", "tts_edge.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		resp.Checks["voiceover_script"] = "missing"
		resp.Fixes = append(resp.Fixes, "Restore scripts/bridges/tts_edge.py")
	} else {
		resp.Checks["voiceover_script"] = "ok"
	}

	// Check edge-tts package
	if _, err := h.processRunner.RunSimple(ctx, "python3", "-c", "import edge_tts"); err != nil {
		resp.Checks["voiceover_library"] = "missing_edge_tts"
		resp.Fixes = append(resp.Fixes, "pip install edge-tts")
	} else {
		resp.Checks["voiceover_library"] = "ok"
	}
}

func (h *SystemHandler) checkGoogleAccounting(ctx context.Context, resp *DoctorResponse) {
	if !h.cfg.GoogleAccountingEnabled {
		resp.Checks["google_accounting"] = "disabled"
		return
	}

	url := h.cfg.GoogleAccountingServerURL
	if url == "" {
		resp.Checks["google_accounting"] = "misconfigured"
		resp.Fixes = append(resp.Fixes, "Set google_accounting.server_url in config.yaml")
		return
	}

	// Try to reach uvicorn server
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/health", nil)
	hResp, err := client.Do(req)
	if err != nil {
		resp.Checks["google_accounting"] = "offline"
		resp.Fixes = append(resp.Fixes, "Start Google Accounting worker: cd google-accounting && uvicorn main:app --port 8000")
	} else {
		defer hResp.Body.Close()
		if hResp.StatusCode == http.StatusOK {
			resp.Checks["google_accounting"] = "ok"
		} else {
			resp.Checks["google_accounting"] = fmt.Sprintf("error_%d", hResp.StatusCode)
		}
	}
}

// Slugify handles GET /internal/slug — the canonical slug endpoint absorbed
// from the retired UtilityModule (2026-08-23 Cleanup Day).
func (h *SystemHandler) Slugify(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing query parameter q"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"input": q,
		"slug":  textutil.Slugify(q),
	})
}
