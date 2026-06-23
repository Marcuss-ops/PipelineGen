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
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/association"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job" // alias JobEnqueueService
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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

// ── Metadata helpers ─────────────────────────────────────────────────────────

// BuildMetadataLanguages builds the list of languages for metadata generation.
// Always includes English first, then adds base language if different, then additional languages.
func BuildMetadataLanguages(baseLanguage string, additionalLanguages []string) []string {
	languages := []string{"en"} // Always include English for YouTube
	languageSet := map[string]bool{"en": true}

	// Add base language if not English
	if baseLanguage != "" && baseLanguage != "en" && !languageSet[baseLanguage] {
		languages = append(languages, baseLanguage)
		languageSet[baseLanguage] = true
	}

	// Add additional requested languages
	for _, lang := range additionalLanguages {
		if !languageSet[lang] {
			languages = append(languages, lang)
			languageSet[lang] = true
		}
	}

	return languages
}

// GenerateVideoMetadata generates YouTube metadata (title, description, tags) for multiple languages in parallel.
// Optimized: generates English metadata ONCE via LLM, then translates for all other languages.
// If model is non-empty, it's passed to the Generator for the metadata + translation calls.
func GenerateVideoMetadata(ctx context.Context, generator *ollama.Generator, title string, languages []string, model string) []VideoMetadata {
	var mu sync.Mutex
	metadata := make([]VideoMetadata, 0, len(languages))
	var wg sync.WaitGroup

	// Generate English metadata FIRST — single LLM call shared across all languages
	var enDesc string
	var enTags []string
	if desc, tags, err := generator.GenerateVideoMetadataWithModel(ctx, title, model); err == nil {
		enDesc = desc
		enTags = tags
	}

	for _, lang := range languages {
		lang := lang // capture
		wg.Add(1)
		concurrent.SafeGoFunc("video-metadata-"+lang, lang, func(lang string) {
			defer wg.Done()

			meta := VideoMetadata{Language: lang}

			// Translate title to target language
			titleTranslated, _ := generator.TranslateTextWithModel(ctx, title, lang, model)
			if titleTranslated != "" {
				meta.Title = titleTranslated
			} else {
				meta.Title = title // fallback to original
			}

			if lang == "en" {
				// Use directly generated English metadata (no translation needed)
				meta.Description = enDesc
				meta.Tags = enTags
			} else if enDesc != "" {
				// Translate the pre-generated English metadata to target language
				descTranslated, _ := generator.TranslateTextWithModel(ctx, enDesc, lang, model)
				if descTranslated != "" {
					meta.Description = descTranslated
				} else {
					meta.Description = enDesc
				}

				// Translate tags
				var translatedTags []string
				for _, tag := range enTags {
					if t, err := generator.TranslateTextWithModel(ctx, tag, lang, model); err == nil && t != "" {
						translatedTags = append(translatedTags, t)
					} else {
						translatedTags = append(translatedTags, tag) // fallback to original tag
					}
				}
				meta.Tags = translatedTags
			}

			mu.Lock()
			metadata = append(metadata, meta)
			mu.Unlock()
		})
	}
	wg.Wait()

	return metadata
}

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

// ── ClipServices bundle (cross-cutting narrow ports — PR3 absorption) ───────
//
// The ClipServices struct + the 7 narrow service interfaces were originally
// declared in their own file (internal/api/script/flow_clip_services.go).
// PR3 (June 2026) inlined them here in helpers.go because:
//
//   - the struct is cross-cutting (passed to ~10 functions across flow/,
//     handler_jobs/, handler_flow_*.go);
//   - the 7 interfaces are narrow ports — analogous to the
//     CurationJobService / CatalogJobService ports already inlined here;
//   - keeping the ≤8 api/script file-budget honest (this file absorbed
//     5 prior files; flow_clip_services.go would have been the 9th).
//
// PR3 wave-14 close keeps ClipServices' canonical identity (same field
// names, same interface signatures); only the declaration site moved.

// ClipSearchService narrows realtime.MatchAsset search.
type ClipSearchService interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]realtime.MatchAsset, error)
}

// AssociationService narrows association.CandidatesRequest building.
type AssociationService interface {
	BuildCandidates(ctx context.Context, req association.CandidatesRequest) (*association.CandidatesResponse, error)
}

// DriveCheckService narrows drive.Uploader.FileIsNotTrashed.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// ImageSearchService narrows images.Service ingest + generation.
type ImageSearchService interface {
	SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error)
	GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error)
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}

// TextTranslationService narrows ollama.Generator.TranslateTextWithModel.
type TextTranslationService interface {
	TranslateTextWithModel(ctx context.Context, text, targetLanguage, model string) (string, error)
}

// JobEnqueueService narrows job.Service.Enqueue.
type JobEnqueueService interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// HarvestService narrows AutoHarvestService.EnqueueHarvest (the interface
// already declared in handler_flow.go's ScriptFlowDep).
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

// VoiceoverService narrows voiceover.Service.GenerateWithDestination.
type VoiceoverService interface {
	GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error)
}

// ClipServices bundles all service dependencies for standalone clip-related
// functions in the script handlers package. Passed as a single struct to
// functions like SearchScriptAssets, SearchArtlistClips, etc.
type ClipServices struct {
	Logger        *zap.Logger
	RealtimeSvc   ClipSearchService
	AssocSvc      AssociationService
	DriveSvc      DriveCheckService
	Translator    TextTranslationService
	JobsSvc       JobEnqueueService
	ImgSvc        ImageSearchService
	VoSvc         VoiceoverService
	HarvestSvc    HarvestService
	ArtlistFolder string // root Drive folder ID for Artlist downloads
	MetadataModel string // lightweight model for metadata/translation tasks
}

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
