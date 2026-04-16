package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ScriptDocsHandler handles script-to-Google-Docs requests
type ScriptDocsHandler struct {
	service *scriptdocs.ScriptDocService
}

// NewScriptDocsHandler creates a new handler
func NewScriptDocsHandler(svc *scriptdocs.ScriptDocService) *ScriptDocsHandler {
	return &ScriptDocsHandler{service: svc}
}

// RegisterRoutes registers the handler routes
func (h *ScriptDocsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
}

// Generate generates a script and creates a Google Doc with entity extraction and clip associations
// @Summary Generate script and create Google Doc
// @Description Generates a script via Ollama, extracts entities, associates clips (Stock + Artlist), and creates a Google Doc
// @Tags script-docs
// @Accept json
// @Produce json
// @Param request body scriptdocs.ScriptDocRequest true "Generate script doc request"
// @Success 200 {object} map[string]interface{}
// @Router /script-docs/generate [post]
func (h *ScriptDocsHandler) Generate(c *gin.Context) {
	var req scriptdocs.ScriptDocRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	result, err := h.service.GenerateScriptDoc(c.Request.Context(), req)
	if err != nil {
		// Log full error internally, return sanitized message to client
		logger.Error("Script doc generation failed", zap.Error(err))
		sanitizedErr := sanitizeErrorMessage(err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": sanitizedErr})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"doc_id":    result.DocID,
		"doc_url":   result.DocURL,
		"title":     result.Title,
		"stock_folder": result.StockFolder,
		"stock_folder_url": result.StockFolderURL,
		"languages": func() []map[string]interface{} {
			var out []map[string]interface{}
			for _, lr := range result.Languages {
				out = append(out, map[string]interface{}{
					"language":              lr.Language,
					"frasi_importanti":      len(lr.FrasiImportanti),
					"nomi_speciali":         len(lr.NomiSpeciali),
					"parole_importanti":     len(lr.ParoleImportant),
					"entita_con_immagine":   len(lr.EntitaConImmagine),
					"associations":          len(lr.Associations),
					"artlist_matches":       countArtlistMatches(lr.Associations),
					"avg_confidence":        avgConfidence(lr.Associations),
				})
			}
			return out
		}(),
	})
}

// sanitizeErrorMessage removes potentially sensitive details from error messages
func sanitizeErrorMessage(errMsg string) string {
	// Remove file paths
	if idx := strings.Index(errMsg, "failed to read Artlist index"); idx != -1 {
		return "Artlist index not found"
	}
	if idx := strings.Index(errMsg, "failed to create Google Doc"); idx != -1 {
		return "Failed to create document in Google Docs"
	}
	if idx := strings.Index(errMsg, "script too short"); idx != -1 {
		return "Generated script was too short. Please try a different topic."
	}
	// Generic fallback
	if len(errMsg) > 200 {
		return "An internal error occurred during script doc generation"
	}
	return errMsg
}

// countArtlistMatches counts how many associations are Artlist matches
func countArtlistMatches(assocs []scriptdocs.ClipAssociation) int {
	count := 0
	for _, a := range assocs {
		if a.Type == "ARTLIST" {
			count++
		}
	}
	return count
}

// avgConfidence calculates average confidence score
func avgConfidence(assocs []scriptdocs.ClipAssociation) float64 {
	if len(assocs) == 0 {
		return 0
	}
	sum := 0.0
	for _, a := range assocs {
		sum += a.Confidence
	}
	return sum / float64(len(assocs))
}
