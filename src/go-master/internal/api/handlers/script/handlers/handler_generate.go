package handlers

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
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/scripts"
)

func (h *ScriptDocsHandler) generate(c *gin.Context, forcePreview bool) {
	if h.generator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "script generator not initialized"})
		return
	}

	var req script.ScriptDocsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	req.Normalize()

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	document, err := script.BuildScriptDocument(ctx, h.generator, req, h.dataDir, h.pythonScriptsDir, h.nodeScraperDir, h.StockDriveRepo, h.ArtlistRepo, h.artlistService, h.imgService)

	if err != nil {
		zap.L().Error("script document generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	var docID, docURL string
	if h.docClient == nil {
		zap.L().Error("doc creation requested but google docs client is not initialized")
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "google docs client not initialized; cannot publish document",
		})
		return
	}

	doc, err := h.docClient.CreateDoc(ctx, document.Title, document.Content, h.stockRootFolder)
	if err != nil {
		zap.L().Error("doc creation failed during generation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": fmt.Sprintf("failed to create Google Doc: %v", err)})
		return
	}
	docID = doc.ID
	docURL = doc.URL

	// Save script to database if repository is available
	if h.scriptsRepo != nil {
		h.saveScriptToDB(ctx, req, document)
	}

	// Generate voiceover if requested
	var voResult interface{}
	if req.Voiceover && h.voService != nil {
		filename := strings.ReplaceAll(req.Topic, " ", "_") + ".mp3"
		res, err := h.voService.Generate(ctx, narrativeOnly(document.Content), req.Language, filename)
		if err != nil {
			zap.L().Warn("voiceover generation failed", zap.Error(err))
		} else {
			voResult = res
		}
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
			"docs_url":     docURL,
			"voiceover":    voResult,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"doc_id":       docID,
		"doc_url":      docURL,
		"docs_url":     docURL,
		"title":        document.Title,
		"full_content": document.Content,
		"timeline":     document.Timeline,
		"voiceover":    voResult,
	})
}

func narrativeOnly(content string) string {
	marker := types.MarkerNarrator
	if idx := strings.Index(content, marker); idx != -1 {
		part := content[idx+len(marker):]
		if nextIdx := strings.Index(part, types.MarkerTimeline); nextIdx != -1 {
			return strings.TrimSpace(part[:nextIdx])
		}
		return strings.TrimSpace(part)
	}
	return content
}

// saveScriptToDB saves the generated script to the database
func (h *ScriptDocsHandler) saveScriptToDB(ctx context.Context, req script.ScriptDocsRequest, document *script.ScriptDocument) {
	sections := make([]scripts.ScriptSectionRecord, 0, len(document.Sections))
	for i, sec := range document.Sections {
		if sec.Title == "🧾 Metadata" {
			continue
		}
		sections = append(sections, scripts.ScriptSectionRecord{
			SectionType:  sec.Title,
			SectionTitle: sec.Title,
			Content:      sec.Body,
			SortOrder:    i,
		})
	}

	scriptRec := &scripts.ScriptRecord{
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
		ModelUsed:      "",
		OllamaBaseURL:  "",
		Version:        1,
		ParentScriptID: nil,
		IsDeleted:      false,
	}
	if client := h.generator.GetClient(); client != nil {
		scriptRec.ModelUsed = client.Model()
		scriptRec.OllamaBaseURL = client.BaseURL()
	}

	scriptID, err := h.scriptsRepo.SaveScript(scriptRec, sections, nil)
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
	scriptsDir := filepath.Join(dir, "scripts")
	// Ensure the scripts directory exists
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create scripts directory: %w", err)
	}

	path := script.BuildPreviewPath(scriptsDir, title)
	if err := script.WritePreview(path, title, content); err != nil {
		return "", err
	}
	return path, nil
}
