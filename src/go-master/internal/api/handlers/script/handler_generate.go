package script

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/scripts"
)

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

	document, err := BuildScriptDocument(ctx, h.generator, req, h.dataDir, h.clipTextDir, h.pythonScriptsDir, h.nodeScraperDir)
	if err != nil {
		zap.L().Error("script document generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

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
			"ok":            true,
			"preview_only":  true,
			"title":         document.Title,
			"full_content":  document.Content,
			"preview_path":  path,
			"timeline":      document.Timeline,
			"doc_id":        docID,
			"doc_url":       docURL,
			"voiceover":     voResult,
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
		"voiceover":    voResult,
	})
}

func narrativeOnly(content string) string {
	// Simple extraction of the narrative part if possible, otherwise return as is
	marker := "🎙️ Narrative Script"
	if idx := strings.Index(content, marker); idx != -1 {
		part := content[idx+len(marker):]
		// Find next section
		if nextIdx := strings.Index(part, "⏱️ Timeline"); nextIdx != -1 {
			return strings.TrimSpace(part[:nextIdx])
		}
		return strings.TrimSpace(part)
	}
	return content
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