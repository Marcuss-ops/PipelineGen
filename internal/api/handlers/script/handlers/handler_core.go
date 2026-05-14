package handlers

import (
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/scripts"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/association"
	clipresolver "velox/go-master/internal/service/clipresolver"
	imgservice "velox/go-master/internal/service/images"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/upload/drive"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/apiutil"
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
	assocService     *association.Service
	jobsService      *jobservice.Service
	clipResolver     *clipresolver.Service
	persistSvc       *scriptdocs.PersistenceService
}

// NewScriptDocsHandler creates a modular script-docs handler.
func NewScriptDocsHandler(gen *ollama.Generator, docClient drive.DocClient, voService *voiceover.Service, imgService *imgservice.Service, dataDir, clipTextDir, pythonScriptsDir, nodeScraperDir string, scriptsRepo *scripts.ScriptRepository, StockDriveRepo, ArtlistRepo, clipsOnlyRepo *clips.Repository, stockRootFolder string, artlistService *artlistSvc.Service, assocService *association.Service, jobsService *jobservice.Service, clipResolver *clipresolver.Service, persistSvc *scriptdocs.PersistenceService) *ScriptDocsHandler {
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
		assocService:     assocService,
		jobsService:      jobsService,
		clipResolver:     clipResolver,
		persistSvc:       persistSvc,
	}
}

// RegisterRoutes registers the script-docs routes.
func (h *ScriptDocsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
	r.POST("/association-candidates", h.AssociationCandidates)
	r.GET("/modes", h.Modes)
}

// Modes returns the available output modes.
func (h *ScriptDocsHandler) Modes(c *gin.Context) {
	apiutil.OK(c, gin.H{
		"ok":    true,
		"modes": []string{"default"},
	})
}

// Generate produces the full document and uploads it to Google Docs when available.
func (h *ScriptDocsHandler) Generate(c *gin.Context) {
	h.generate(c)
}

// SetArtlistService sets the Artlist service for live discovery
func (h *ScriptDocsHandler) SetArtlistService(svc *artlistSvc.Service) {
	h.artlistService = svc
}
