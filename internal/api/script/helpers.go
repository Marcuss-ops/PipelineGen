// Package script (api/script) — helpers.go carrying the cross-cutting
// helpers shared across the script-flow transport: post-write context,
// the CurationJob/CatalogJob service interfaces, and the embedded
// script-history HTTP transport.
//
// Wave 14 (API compaction): the script-history module is wired by the
// composition root with feature flags and a prebuilt feature guard,
// so this transport file has no concrete configuration imports.
// Forwarding type aliases for application-side services were deleted:
// callers in handler_flow.go use usecase.* directly.
package script

import (
	"strconv"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ── Post-gen metadata helpers ───────────────────────────────────────────────
//
// Post-gen metadata builders live in the application/scripts package. PostGenUseCase calls them directly
// from its own package; this transport no longer re-exports them.

// ── Script history HTTP transport (companion to /api/script) ──────────────

// ScriptHistoryHandler handles script history endpoints.
type ScriptHistoryHandler struct {
	repo adapters.ScriptRepository
	log  *zap.Logger
}

// NewScriptHistoryHandler creates a new script history handler.
func NewScriptHistoryHandler(repo adapters.ScriptRepository, log *zap.Logger) *ScriptHistoryHandler {
	return &ScriptHistoryHandler{
		repo: repo,
		log:  log,
	}
}

// RegisterRoutes registers the script history routes (sibling of /api/script).
func (h *ScriptHistoryHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("", h.ListScripts)
	r.GET("/:id", h.GetScriptByID)
}

// ListScripts handles GET /scripts
func (h *ScriptHistoryHandler) ListScripts(c *gin.Context) {
	if h == nil || h.repo == nil {
		apiutil.Error(c, 503, "script repository is not initialized")
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
	limit = apiutil.ClampLimit(limit, 20, 1)

	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}
	offset = apiutil.ClampLimit(offset, 0, 0)

	scriptRecords, err := h.repo.ListScripts(c.Request.Context(), usecase.ScriptListFilter{Limit: limit, Offset: offset, Language: language, Status: template})
	if err != nil {
		h.log.Error("Failed to list scripts", zap.Error(err))
		apiutil.InternalError(c, err)
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
			"model_used": s.Model,
			"created_at": s.CreatedAt,
			"updated_at": s.UpdatedAt,
			"version":    s.Version,
			// DRIFT-23-4 (June 2026, HC-7): emit the actual parent ID
			// rather than the historical empty-string literal that
			// surfaced in /scripts as `parent_id: ""` regardless of
			// whether the script had a parent. Matches the canonical
			// GetScriptByID behaviour (line 193 of this file) which
			// emits scriptRec.ParentScriptID. The canonical JSON
			// field name is pkg/defaults.DefaultVideoConfig().ParentFieldName.
			"parent_id": s.ParentScriptID,
		})
	}

	apiutil.OK(c, gin.H{
		"scripts": scriptsRes,
		"total":   len(scriptRecords),
		"limit":   limit,
		"offset":  offset,
	})
}

// GetScriptByID handles GET /scripts/:id
func (h *ScriptHistoryHandler) GetScriptByID(c *gin.Context) {
	if h == nil || h.repo == nil {
		apiutil.Error(c, 503, "script repository is not initialized")
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		apiutil.BadRequest(c, "invalid script id")
		return
	}

	scriptRec, sections, stockMatches, err := h.repo.GetScriptByID(id)
	if err != nil {
		h.log.Error("Failed to get script", zap.Int64("id", id), zap.Error(err))
		apiutil.NotFound(c, "script not found")
		return
	}

	sectionsResp := make([]gin.H, 0, len(sections))
	for _, sec := range sections {
		sectionsResp = append(sectionsResp, gin.H{
			"id":             sec.ID,
			"section_type":   sec.SectionType,
			"section_title":  sec.SectionTitle,
			"content":        sec.Content,
			"sort_order":     sec.SortOrder,
			"voiceover_link": sec.VoiceoverLink,
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

	apiutil.OK(c, gin.H{
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

// ── Script-history module + handler ────────────────────────────────────────
//
// ScriptHistoryModule no longer takes the dense application config
// struct. Composition root injects:
//
//   - the handler (always)
//   - the logger (always)
//   - a prebuilt feature_guard middleware (always; may be a no-op
//     gin.HandlerFunc if the caller skips the guard)
//   - an enabled flag reflecting the feature-flag decision
//
// This keeps zero configuration reach-through into the transport layer.

// ScriptHistoryModule is a registrable module for Script History functionality.
// Mounted on the /scripts prefix (sibling of /script).
type ScriptHistoryModule struct {
	enabled      bool
	log          *zap.Logger
	handler      *ScriptHistoryHandler
	featureGuard gin.HandlerFunc
}

// NewScriptHistoryModule creates a new ScriptHistory module.
//
// enabled reflects the resolved feature-flag value at composition time.
// featureGuard is the prebuilt route middleware (may be nil for tests or
// when the composition root has already enforced the flag decision).
func NewScriptHistoryModule(
	handler *ScriptHistoryHandler,
	log *zap.Logger,
	featureGuard gin.HandlerFunc,
	enabled bool,
) *ScriptHistoryModule {
	return &ScriptHistoryModule{
		handler:      handler,
		log:          log,
		featureGuard: featureGuard,
		enabled:      enabled,
	}
}

// Name returns the module name.
func (m *ScriptHistoryModule) Name() string {
	return "scripts"
}

// Enabled checks if this module is enabled.
// Composition root passes the resolved feature-flag value at construction
// time so no live configuration lookup is required here.
func (m *ScriptHistoryModule) Enabled() bool {
	return m.enabled
}

// RegisterRoutes registers the module's routes. The feature guard (when
// non-nil) is applied to the /scripts sub-group.
func (m *ScriptHistoryModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		if m.log != nil {
			m.log.Warn("script history handler is nil, skipping route registration")
		}
		return
	}

	group := rg.Group("/scripts")
	if m.featureGuard != nil {
		group.Use(m.featureGuard)
	}
	m.handler.RegisterRoutes(group)
}
