package script

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/upload/drive"
)

// ScriptDocsHandler generates modular script docs with Ollama and optionally uploads them to Google Docs.
type ScriptDocsHandler struct {
	generator       *ollama.Generator
	docClient       *drive.DocClient
	dataDir         string
	scriptsRepo     *scripts.ScriptRepository
	stockRootFolder string
}

// NewScriptDocsHandler creates a modular script-docs handler.
func NewScriptDocsHandler(gen *ollama.Generator, docClient *drive.DocClient, dataDir string, scriptsRepo *scripts.ScriptRepository, stockRootFolder string) *ScriptDocsHandler {
	return &ScriptDocsHandler{
		generator:       gen,
		docClient:       docClient,
		dataDir:         dataDir,
		scriptsRepo:     scriptsRepo,
		stockRootFolder: stockRootFolder,
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

func (h *ScriptDocsHandler) generate(c *gin.Context, forcePreview bool) {
	if h.generator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "script generator not initialized"})
		return
	}

	var req ScriptDocsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	req.normalize()

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	document, err := BuildScriptDocument(ctx, h.generator, req, h.dataDir)
	if err != nil {
		zap.L().Error("script document generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Save script to database if repository is available
	if h.scriptsRepo != nil {
		h.saveScriptToDB(ctx, req, document)
	}

	var docID, docURL string
	if h.docClient != nil {
		doc, err := h.docClient.CreateDoc(ctx, document.Title, document.Content, h.stockRootFolder)
		if err != nil {
			zap.L().Warn("doc creation failed during generation", zap.Error(err))
		} else {
			docID = doc.ID
			docURL = doc.URL
		}
	} else if !forcePreview {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "Google Docs client is not initialized. Cannot create document."})
		return
	}

	if forcePreview {
		path, err := h.savePreview(document.Title, document.Content)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":           true,
			"preview_only": true,
			"title":        document.Title,
			"full_content": document.Content,
			"preview_path": path,
			"timeline":     document.Timeline,
			"doc_id":       docID,
			"doc_url":      docURL,
		})
		return
	}

	if docID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Failed to create Google Doc"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"doc_id":       docID,
		"doc_url":      docURL,
		"title":        document.Title,
		"full_content": document.Content,
		"timeline":     document.Timeline,
	})
}

// saveScriptToDB saves the generated script to the database
func (h *ScriptDocsHandler) saveScriptToDB(ctx context.Context, req ScriptDocsRequest, document *ScriptDocument) {
	// Convert sections to ScriptSectionRecord
	sections := make([]scripts.ScriptSectionRecord, 0, len(document.Sections))
	for i, sec := range document.Sections {
		if sec.Title == "🧾 Metadata" {
			continue // Skip metadata section
		}
		sections = append(sections, scripts.ScriptSectionRecord{
			SectionType:  sec.Title,
			SectionTitle: sec.Title,
			Content:      sec.Body,
			SortOrder:    i,
		})
	}

	// Create script record
	script := &scripts.ScriptRecord{
		Topic:          req.Topic,
		Duration:       req.Duration,
		Language:       req.Language,
		Template:       req.Template,
		Mode:           "modular",
		NarrativeText:  document.Content,
		TimelineJSON:   "",
		EntitiesJSON:   "",
		MetadataJSON:   "",
		FullDocument:   document.Content,
		ModelUsed:      "gemma3:12b", // TODO: get from generator
		OllamaBaseURL:  "",
		Version:        1,
		ParentScriptID: nil,
		IsDeleted:      false,
	}

	// Save script with sections
	scriptID, err := h.scriptsRepo.SaveScript(script, sections, nil)
	if err != nil {
		zap.L().Error("Failed to save script to database", zap.Error(err))
		return
	}

	zap.L().Info("Script saved to database", zap.Int64("script_id", scriptID), zap.String("topic", req.Topic))
}

func (h *ScriptDocsHandler) savePreview(title, content string) (string, error) {
	dir := h.dataDir
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	path := buildPreviewPath(dir, title)
	if err := writePreview(path, title, content); err != nil {
		return "", err
	}
	return path, nil
}
