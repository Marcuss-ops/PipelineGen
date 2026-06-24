// Package script (api/script) — helpers.go carrying the cross-cutting
// helpers shared across the script-flow transport: post-write context,
// metadata builders, the CurationJob/CatalogJob service interfaces,
// and the embedded script-history HTTP transport.
//
// PR3 (June 2026): this file consolidates four prior files:
//
//   postwrite.go               (withPostWriteContext — survives client disconnect)
//   handler_metadata.go        (BuildMetadataLanguages, GenerateVideoMetadata)
//   interfaces.go              (CurationJobService + CatalogJobService)
//   script_history_handler.go  (GET /api/scripts/{, /:id})
//   module_scripthistory.go    (ScriptHistoryModule)
//
// The script-history module is mounted on /scripts (sibling of /script)
// with its own admin-gated middleware. Both handler and module are
// co-located here because they share ScriptHistoryHandler as a receiver
// and the module is essentially a 30-line wiring shim.
package script

import (
	"context"
	"strconv"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job" // alias JobEnqueueService
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/contextutil"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ── Post-write context ───────────────────────────────────────────────────────

// postWriteTimeout caps how long post-generation side-effects (DB
// writes, Google Doc uploads, cache persists) are allowed to run.
//
// The previous behaviour in this package was to reuse the request
// context for every post-generation write. That looked clean but
// caused silent data loss whenever the HTTP client disconnected
// before the response was sent: the LLM had produced the script,
// the DB save was in flight, and then c.Request.Context() was
// cancelled — taking the save with it. The 30s budget is generous
// for SQLite WAL writes and Google API calls on the local network
// and small enough that a hung save won't pin the worker.
const postWriteTimeout = 30 * time.Second

// withPostWriteContext returns a fresh context using an independent
// 30s-timeout context, decoupled from the caller's request context.
// Delegates to pkg/contextutil.PostWriteContext.
//
// Kept as a convenience wrapper for existing callers in this package.
func withPostWriteContext(parent context.Context, log *zap.Logger, op string) (context.Context, context.CancelFunc) {
	return contextutil.PostWriteContext(parent, log, op, postWriteTimeout)
}

// ── Metadata helpers → now in application/scripts/metadata.go ──────────────
//
// Agente 4 — F (June 2026): BuildMetadataLanguages and GenerateVideoMetadata
// moved to internal/application/scripts/metadata.go. PostGenUseCase calls
// them directly from the same package. VideoMetadata is re-exported as a
// type alias for back-compat.

// ── Job service interfaces ──────────────────────────────────────────────────
//
// CurationJobService and CatalogJobService are the narrow ports the
// ScriptFlowHandler binds to via ScriptFlowDeps.{Curation,Catalog}JobService.
// They are NOT instantiated by WireRegistry today (both fields are nil in
// PR4.E June 2026 — see AGENTS.md), but the types are kept so the future
// wiring (background script.curate / script.generate_from_catalog jobs)
// can drop them in without API churn.

// CurationJobService handles background curation jobs (script.curate).
type CurationJobService interface {
	HandleCurateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error)
}

// CatalogJobService handles background catalog-to-script generation jobs.
type CatalogJobService interface {
	HandleCatalogScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error)
}

// ── Script history HTTP transport (companion to /api/script) ──────────────

// ScriptHistoryHandler handles script history endpoints.
type ScriptHistoryHandler struct {
	repo scripts.ScriptRepository
	log  *zap.Logger
}

// NewScriptHistoryHandler creates a new script history handler.
func NewScriptHistoryHandler(repo scripts.ScriptRepository, log *zap.Logger) *ScriptHistoryHandler {
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
		api.Error(c, 503, "script repository is not initialized")
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
	limit = api.ClampLimit(limit, 20, 1)

	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}
	offset = api.ClampLimit(offset, 0, 0)

	scriptRecords, err := h.repo.ListScripts(c.Request.Context(), scripts.ScriptListFilter{Limit: limit, Offset: offset, Language: language, Status: template})
	if err != nil {
		h.log.Error("Failed to list scripts", zap.Error(err))
		api.InternalError(c, err)
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
			"parent_id":  "",
		})
	}

	api.OK(c, gin.H{
		"scripts": scriptsRes,
		"total":   len(scriptRecords),
		"limit":   limit,
		"offset":  offset,
	})
}

// GetScriptByID handles GET /scripts/:id
func (h *ScriptHistoryHandler) GetScriptByID(c *gin.Context) {
	if h == nil || h.repo == nil {
		api.Error(c, 503, "script repository is not initialized")
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		api.BadRequest(c, "invalid script id")
		return
	}

	scriptRec, sections, stockMatches, err := h.repo.GetScriptByID(id)
	if err != nil {
		h.log.Error("Failed to get script", zap.Int64("id", id), zap.Error(err))
		api.NotFound(c, "script not found")
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

	api.OK(c, gin.H{
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

// ── ClipServices bundle (back-compat type aliases — PR2 extraction) ──────
//
// PR2 (June 2026): ClipServices and its 7 narrow port interfaces have been
// extracted to internal/application/scripts/clip_services.go. The API layer
// re-exports them as type aliases for zero-churn back-compat. New code should
// import from internal/application/scripts/ directly.

// ClipSearchService narrows realtime.MatchAsset search.
type ClipSearchService = scripts.ClipSearchService

// AssociationService narrows association.CandidatesRequest building.
type AssociationService = scripts.AssociationService

// DriveCheckService narrows drive.Uploader.FileIsNotTrashed.
type DriveCheckService = scripts.DriveCheckService

// ImageSearchService narrows images.Service ingest + generation.
type ImageSearchService = scripts.ImageSearchService

// TextTranslationService narrows ollama.Generator.TranslateTextWithModel.
type TextTranslationService = scripts.TextTranslationService

// JobEnqueueService narrows job.Service.Enqueue.
type JobEnqueueService = scripts.JobEnqueueService

// HarvestService narrows AutoHarvestService.EnqueueHarvest.
type HarvestService = scripts.HarvestService

// VoiceoverService narrows voiceover.Service.GenerateWithDestination.
type VoiceoverService = scripts.VoiceoverService

// ClipServices bundles all service dependencies for standalone clip-related
// functions. Back-compat alias for scripts.ClipServices.
type ClipServices = scripts.ClipServices

// ── Script-history module + handler ────────────────────────────────────────

// ScriptHistoryModule is a registrable module for Script History functionality.
// Mounted on the /scripts prefix (sibling of /script).
type ScriptHistoryModule struct {
	cfg     *config.Config
	log     *zap.Logger
	handler *ScriptHistoryHandler
}

// NewScriptHistoryModule creates a new ScriptHistory module.
func NewScriptHistoryModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *ScriptHistoryHandler,
) *ScriptHistoryModule {
	return &ScriptHistoryModule{
		cfg:     cfg,
		log:     log,
		handler: handler,
	}
}

// Name returns the module name.
func (m *ScriptHistoryModule) Name() string {
	return "scripts"
}

// Enabled checks if this module is enabled.
func (m *ScriptHistoryModule) Enabled() bool {
	return m.cfg.Features.ScriptClipsEnabled
}

// RegisterRoutes registers the module's routes.
func (m *ScriptHistoryModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		m.log.Warn("script history handler is nil, skipping route registration")
		return
	}

	group := rg.Group("/scripts")
	group.Use(middleware.ScriptClipsEnabled(m.cfg))
	m.handler.RegisterRoutes(group)
}
