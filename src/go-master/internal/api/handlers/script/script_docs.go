package script

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
	r.GET("/modes", h.Modes)
	r.POST("/generate", h.Generate)
	r.POST("/generate/stock", h.GenerateStock)
	r.POST("/generate/preview", h.GeneratePreview)
	r.POST("/generate/fullartlist", h.GenerateFullArtlist)
	r.POST("/generate/imagesfull", h.GenerateImagesFull)
	r.POST("/generate/imagesonly", h.GenerateImagesOnly)
	r.POST("/generate/mixed", h.GenerateMixed)
	r.POST("/generate/jitstock", h.GenerateJITStock)
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
	h.generateScriptDocWithMode(c, scriptdocs.AssociationModeDefault, false, scriptdocs.AssociationModeDefault)
}

// GenerateStock generates a standard stock-first script doc.
func (h *ScriptDocsHandler) GenerateStock(c *gin.Context) {
	h.generateScriptDocWithMode(c, scriptdocs.AssociationModeDefault, false, scriptdocs.AssociationModeDefault)
}

// GenerateFullArtlist generates docs using Artlist-only associations.
// Forces association_mode=fullartlist and defaults language to English.
func (h *ScriptDocsHandler) GenerateFullArtlist(c *gin.Context) {
	h.generateScriptDocWithMode(c, scriptdocs.AssociationModeFullArtlist, true, scriptdocs.AssociationModeFullArtlist)
}

// GenerateImagesFull generates image-rich docs while preserving clip and Artlist associations.
// Forces association_mode=images_full and defaults language to English.
func (h *ScriptDocsHandler) GenerateImagesFull(c *gin.Context) {
	h.generateScriptDocWithMode(c, scriptdocs.AssociationModeImagesFull, true, scriptdocs.AssociationModeImagesFull)
}

// GenerateImagesOnly generates docs using image-only associations without clip embedding.
func (h *ScriptDocsHandler) GenerateImagesOnly(c *gin.Context) {
	h.generateScriptDocWithMode(c, scriptdocs.AssociationModeImagesOnly, true, scriptdocs.AssociationModeImagesOnly)
}

// GenerateMixed generates docs that can combine clips and images.
func (h *ScriptDocsHandler) GenerateMixed(c *gin.Context) {
	h.generateScriptDocWithMode(c, scriptdocs.AssociationModeMixed, false, scriptdocs.AssociationModeMixed)
}

// GenerateJITStock generates docs that allow just-in-time stock creation.
func (h *ScriptDocsHandler) GenerateJITStock(c *gin.Context) {
	h.generateScriptDocWithMode(c, scriptdocs.AssociationModeJITStock, false, scriptdocs.AssociationModeJITStock)
}

// Modes returns the supported script-doc generation modes.
// @Summary List script-doc generation modes
// @Description Returns the supported modes and their intended use.
// @Tags script-docs
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /script-docs/modes [get]
func (h *ScriptDocsHandler) Modes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"modes": []gin.H{
			{
				"mode":         scriptdocs.AssociationModeDefault,
				"label":        "stock",
				"description":  "Stock-first video generation with standard clip matching and Artlist fallback.",
				"fallbacks":    []string{"dynamic-cache", "stockdb", "artlist", "llm-expansion", "dynamic-search"},
				"allows_jit":   false,
				"default_lang": []string{"it"},
			},
			{
				"mode":         scriptdocs.AssociationModeFullArtlist,
				"label":        "full artlist",
				"description":  "Artlist-only generation with timeline-oriented output.",
				"fallbacks":    []string{"artlist"},
				"allows_jit":   false,
				"default_lang": []string{"en"},
			},
			{
				"mode":         scriptdocs.AssociationModeImagesFull,
				"label":        "images full",
				"description":  "Image-rich generation that keeps stock and Artlist clips, then adds chapter images.",
				"fallbacks":    []string{"dynamic-cache", "stockdb", "artlist", "imagesdb-cache", "entityimages", "download"},
				"allows_jit":   false,
				"default_lang": []string{"en"},
			},
			{
				"mode":         scriptdocs.AssociationModeImagesOnly,
				"label":        "images only",
				"description":  "Image-only generation for visual docs without clip embedding.",
				"fallbacks":    []string{"imagesdb-cache", "entityimages", "download"},
				"allows_jit":   false,
				"default_lang": []string{"en"},
			},
			{
				"mode":         scriptdocs.AssociationModeMixed,
				"label":        "mixed",
				"description":  "Mixed generation that can place either clips or images per chapter.",
				"fallbacks":    []string{"clip", "image"},
				"allows_jit":   false,
				"default_lang": []string{"it"},
			},
			{
				"mode":         scriptdocs.AssociationModeJITStock,
				"label":        "jit stock",
				"description":  "Just-in-time stock generation that can search YouTube, approve with Gemma, download, process, and upload to Drive.",
				"fallbacks":    []string{"stockdb", "artlist", "youtube", "gemma", "download", "drive"},
				"allows_jit":   true,
				"default_lang": []string{"it"},
			},
		},
	})
}

// GeneratePreview creates a local-only preview document without uploading to Google Docs.
func (h *ScriptDocsHandler) GeneratePreview(c *gin.Context) {
	var req scriptdocs.ScriptDocRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	req.PreviewOnly = true

	result, err := h.service.GenerateScriptDoc(c.Request.Context(), req)
	if err != nil {
		logger.Error("Script doc preview generation failed", zap.Error(err))
		sanitizedErr := sanitizeErrorMessage(err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": sanitizedErr})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"preview_only":     true,
		"doc_id":           result.DocID,
		"doc_url":          result.DocURL,
		"title":            result.Title,
		"stock_folder":     result.StockFolder,
		"stock_folder_url": result.StockFolderURL,
		"image_plan":       result.ImagePlan,
		"image_plan_path":  result.ImagePlanPath,
		"audit_path":       result.AuditPath,
		"preview_path":     result.PreviewPath,
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

func (h *ScriptDocsHandler) generateScriptDocWithMode(c *gin.Context, mode string, forceEnglish bool, responseMode string) {
	var req scriptdocs.ScriptDocRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	req.AssociationMode = mode
	if forceEnglish && len(req.Languages) == 0 {
		req.Languages = []string{"en"}
	}

	result, err := h.service.GenerateScriptDoc(c.Request.Context(), req)
	if err != nil {
		logger.Error("Script doc generation failed", zap.String("mode", mode), zap.Error(err))
		sanitizedErr := sanitizeErrorMessage(err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": sanitizedErr})
		return
	}

	c.JSON(http.StatusOK, buildScriptDocResponse(result, responseMode))
}

func buildScriptDocResponse(result *scriptdocs.ScriptDocResult, mode string) gin.H {
	return gin.H{
		"ok":               true,
		"mode":             mode,
		"doc_id":           result.DocID,
		"doc_url":          result.DocURL,
		"title":            result.Title,
		"stock_folder":     result.StockFolder,
		"stock_folder_url": result.StockFolderURL,
		"image_plan":       result.ImagePlan,
		"image_plan_path":  result.ImagePlanPath,
		"audit_path":       result.AuditPath,
		"preview_path":     result.PreviewPath,
		"languages": func() []map[string]interface{} {
			var out []map[string]interface{}
			for _, lr := range result.Languages {
				out = append(out, map[string]interface{}{
					"language":            lr.Language,
					"frasi_importanti":    len(lr.FrasiImportanti),
					"nomi_speciali":       len(lr.NomiSpeciali),
					"parole_importanti":   len(lr.ParoleImportant),
					"entita_con_immagine": len(lr.EntitaConImmagine),
					"associations":        len(lr.Associations),
					"artlist_matches":     countArtlistMatches(lr.Associations),
					"image_associations":  len(lr.ImageAssociations),
					"avg_confidence":      avgConfidence(lr.Associations),
				})
			}
			return out
		}(),
	}
}
