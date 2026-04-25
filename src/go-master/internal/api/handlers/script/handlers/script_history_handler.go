package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/scripts"
)

// ScriptHistoryHandler handles script history endpoints
type ScriptHistoryHandler struct {
	repo *scripts.ScriptRepository
	log  *zap.Logger
}

// NewScriptHistoryHandler creates a new script history handler
func NewScriptHistoryHandler(repo *scripts.ScriptRepository, log *zap.Logger) *ScriptHistoryHandler {
	return &ScriptHistoryHandler{
		repo: repo,
		log:  log,
	}
}

// RegisterRoutes registers the script history routes
func (h *ScriptHistoryHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("", h.ListScripts)
	r.GET("/:id", h.GetScriptByID)
}

// ListScripts handles GET /scripts
func (h *ScriptHistoryHandler) ListScripts(c *gin.Context) {
	if h == nil || h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "script repository is not initialized"})
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")
	language := c.Query("language")
	template := c.Query("template")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	scriptRecords, total, err := h.repo.ListScripts(limit, offset, language, template)
	if err != nil {
		h.log.Error("Failed to list scripts", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list scripts"})
		return
	}

	scriptsRes := make([]gin.H, 0, len(scriptRecords))
	for _, s := range scriptRecords {
		scriptsRes = append(scriptsRes, gin.H{
			"id":         s.ID,
			"topic":      s.Topic,
			"duration":   s.Duration,
			"language":   s.Language,
			"template":   s.Template,
			"mode":       s.Mode,
			"model_used": s.ModelUsed,
			"created_at": s.CreatedAt,
			"updated_at": s.UpdatedAt,
			"version":    s.Version,
			"parent_id":  s.ParentScriptID,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"scripts": scriptsRes,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// GetScriptByID handles GET /scripts/:id
func (h *ScriptHistoryHandler) GetScriptByID(c *gin.Context) {
	if h == nil || h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "script repository is not initialized"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid script id"})
		return
	}

	scriptRec, sections, stockMatches, err := h.repo.GetScriptByID(id)
	if err != nil {
		h.log.Error("Failed to get script", zap.Int64("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "script not found"})
		return
	}

	sectionsResp := make([]gin.H, 0, len(sections))
	for _, sec := range sections {
		sectionsResp = append(sectionsResp, gin.H{
			"id":            sec.ID,
			"section_type":  sec.SectionType,
			"section_title": sec.SectionTitle,
			"content":       sec.Content,
			"sort_order":    sec.SortOrder,
		})
	}

	stockResp := make([]gin.H, 0, len(stockMatches))
	for _, m := range stockMatches {
		stockResp = append(stockResp, gin.H{
			"id":            m.ID,
			"segment_index": m.SegmentIndex,
			"stock_path":    m.StockPath,
			"stock_source":  m.StockSource,
			"score":         m.Score,
			"matched_terms": m.MatchedTerms,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"id":             scriptRec.ID,
		"topic":          scriptRec.Topic,
		"duration":       scriptRec.Duration,
		"language":       scriptRec.Language,
		"template":       scriptRec.Template,
		"mode":           scriptRec.Mode,
		"narrative_text": scriptRec.NarrativeText,
		"timeline_json":  scriptRec.TimelineJSON,
		"entities_json":  scriptRec.EntitiesJSON,
		"metadata_json":  scriptRec.MetadataJSON,
		"full_document":  scriptRec.FullDocument,
		"model_used":     scriptRec.ModelUsed,
		"created_at":     scriptRec.CreatedAt,
		"updated_at":     scriptRec.UpdatedAt,
		"version":        scriptRec.Version,
		"parent_id":      scriptRec.ParentScriptID,
		"sections":       sectionsResp,
		"stock_matches":  stockResp,
	})
}
