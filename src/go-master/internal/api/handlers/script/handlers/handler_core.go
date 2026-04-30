package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/scripts"
	artlistSvc "velox/go-master/internal/service/artlist"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/upload/drive"
)

// ScriptDocsHandler generates modular script docs with Ollama and optionally uploads them to Google Docs.
type ScriptDocsHandler struct {
	generator        *ollama.Generator
	docClient        drive.DocClient
	voService        *voiceover.Service
	imgService       *imgservice.Service
	dataDir          string
	clipTextDir      string
	pythonScriptsDir string
	nodeScraperDir   string
	scriptsRepo      *scripts.ScriptRepository
	StockDriveRepo   *clips.Repository
	ArtlistRepo      *clips.Repository
	clipsOnlyRepo    *clips.Repository
	stockRootFolder  string
	artlistService   *artlistSvc.Service
}

// NewScriptDocsHandler creates a modular script-docs handler.
func NewScriptDocsHandler(gen *ollama.Generator, docClient drive.DocClient, voService *voiceover.Service, imgService *imgservice.Service, dataDir, clipTextDir, pythonScriptsDir, nodeScraperDir string, scriptsRepo *scripts.ScriptRepository, StockDriveRepo, ArtlistRepo, clipsOnlyRepo *clips.Repository, stockRootFolder string, artlistService *artlistSvc.Service) *ScriptDocsHandler {
	return &ScriptDocsHandler{
		generator:        gen,
		docClient:        docClient,
		voService:        voService,
		imgService:       imgService,
		dataDir:          dataDir,
		clipTextDir:      clipTextDir,
		pythonScriptsDir: pythonScriptsDir,
		nodeScraperDir:   nodeScraperDir,
		scriptsRepo:      scriptsRepo,
		StockDriveRepo:   StockDriveRepo,
		ArtlistRepo:      ArtlistRepo,
		clipsOnlyRepo:    clipsOnlyRepo,
		stockRootFolder:  stockRootFolder,
		artlistService:   artlistService,
	}
}

// RegisterRoutes registers the script-docs routes.
func (h *ScriptDocsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
	r.POST("/preview", h.GeneratePreview)
	r.GET("/modes", h.Modes)
}

// Modes returns the available output modes.
func (h *ScriptDocsHandler) Modes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"modes": []string{
			"default",
			"preview",
		},
	})
}

// Generate produces the full document and uploads it to Google Docs when available.
func (h *ScriptDocsHandler) Generate(c *gin.Context) {
	h.generate(c, false)
}

// GeneratePreview always writes a local preview file instead of uploading to Docs.
func (h *ScriptDocsHandler) GeneratePreview(c *gin.Context) {
	h.generate(c, true)
}
