package script

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
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/upload/drive"
)

// ScriptDocsHandler generates a script with Ollama and optionally uploads it to Google Docs.
type ScriptDocsHandler struct {
	generator *ollama.Generator
	docClient *drive.DocClient
	dataDir   string
}

type ScriptDocsRequest struct {
	Topic       string `json:"topic" binding:"required"`
	Duration    int    `json:"duration"`
	Language    string `json:"language"`
	Template    string `json:"template"`
	PreviewOnly bool   `json:"preview_only"`
}

// NewScriptDocsHandler creates a minimal script-docs handler.
func NewScriptDocsHandler(gen *ollama.Generator, docClient *drive.DocClient, dataDir string) *ScriptDocsHandler {
	return &ScriptDocsHandler{
		generator: gen,
		docClient: docClient,
		dataDir:   dataDir,
	}
}

// RegisterRoutes registers the minimal script-docs routes.
func (h *ScriptDocsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
	r.POST("/preview", h.GeneratePreview)
	r.GET("/modes", h.Modes)
}

func (h *ScriptDocsHandler) Modes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"modes": []string{
			"default",
			"preview",
		},
	})
}

func (h *ScriptDocsHandler) Generate(c *gin.Context) {
	h.generate(c, false)
}

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

	if req.Duration <= 0 {
		req.Duration = 60
	}
	if strings.TrimSpace(req.Language) == "" {
		req.Language = "it"
	}

	prompt := buildPrompt(req.Topic, req.Duration, req.Language, req.Template)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()

	text, err := h.generator.Generate(ctx, prompt)
	if err != nil {
		zap.L().Error("script generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	title := fmt.Sprintf("Script: %s", req.Topic)
	previewOnly := forcePreview || req.PreviewOnly || h.docClient == nil

	if previewOnly {
		path, err := h.savePreview(title, text)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":           true,
			"preview_only": true,
			"title":        title,
			"full_content": text,
			"preview_path": path,
		})
		return
	}

	doc, err := h.docClient.CreateDoc(ctx, title, text, "")
	if err != nil {
		zap.L().Error("doc creation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"doc_id":       doc.ID,
		"doc_url":      doc.URL,
		"title":        title,
		"full_content": text,
	})
}

func (h *ScriptDocsHandler) savePreview(title, content string) (string, error) {
	dir := h.dataDir
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	safe := sanitizeFilename(title)
	path := filepath.Join(dir, safe+".txt")
	data := []byte(title + "\n\n" + content)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func buildPrompt(topic string, duration int, language, template string) string {
	wordCount := duration * 3
	style := "documentary"
	switch strings.ToLower(strings.TrimSpace(template)) {
	case "storytelling":
		style = "storytelling"
	case "top10":
		style = "top 10"
	case "biography":
		style = "biography"
	}

	return fmt.Sprintf(
		"Genera un testo %s su %s in lingua %s. Lunghezza circa %d parole. Scrivi solo il testo finale, senza introduzioni, titoli o note tecniche.",
		style, topic, language, wordCount,
	)
}

func sanitizeFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == ' ' || r == '-' || r == '_' {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "script_preview"
	}
	return out
}
