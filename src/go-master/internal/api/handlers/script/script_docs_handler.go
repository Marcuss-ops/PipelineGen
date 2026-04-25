package script

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/upload/drive"
)

// ScriptDocsHandler generates modular script docs with Ollama and optionally uploads them to Google Docs.
type ScriptDocsHandler struct {
	generator *ollama.Generator
	docClient *drive.DocClient
	dataDir   string
}

// NewScriptDocsHandler creates a modular script-docs handler.
func NewScriptDocsHandler(gen *ollama.Generator, docClient *drive.DocClient, dataDir string) *ScriptDocsHandler {
	return &ScriptDocsHandler{
		generator: gen,
		docClient: docClient,
		dataDir:   dataDir,
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

	previewOnly := forcePreview || h.docClient == nil
	if previewOnly {
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
		})
		return
	}

	doc, err := h.docClient.CreateDoc(ctx, document.Title, document.Content, "")
	if err != nil {
		zap.L().Error("doc creation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"doc_id":       doc.ID,
		"doc_url":      doc.URL,
		"title":        document.Title,
		"full_content": document.Content,
		"timeline":     document.Timeline,
	})
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
