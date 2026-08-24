// Package youtube hosts the HTTP handlers for the YouTube clip download,
// info, extract, search, and diagnostics endpoints. Split out from the
// now-deleted internal/api/sources/ package (PR-A consolidation)
// YouTube transport isolated from the rest of the SourcesHandler.
//
// The clips repository is injected at construction time so the handler has
// no late-binding setters.
//
// Split topology (godlike/06 SSOT one-canonical-owner-per-fact):
//   - youtube_handlers.go — types + struct + ctor + RegisterRoutes + GetVideoInfo
//   - youtube_extract.go  — Extract + normalizeExtractionDestination
//   - youtube_search.go   — Diagnostics (incl. search stats) + SearchCatalog
package assets

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// YouTubeClipService is the narrow service surface the HTTP handler needs.
// *youtube.Service satisfies this interface.
type YouTubeClipService interface {
	Config() yttypes.RuntimeConfig
	GetVideoInfo(ctx context.Context, url string) (*ytports.DownloaderMetadata, error)
	SearchByTopicWithFilter(ctx context.Context, query string, limit int, sortMode, publishedAfter string) (*youtube.TopicSearchResponse, error)
	Extract(ctx context.Context, req *yttypes.ExtractRequest) (*yttypes.ExtractResponse, error)
	GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error)
}

// Compile-time pin (godlike/06 SSOT drift-prevention):
// *youtube.Service MUST keep satisfying YouTubeClipService. Any future
// signature drift on either side surfaces as a build failure here
// rather than a runtime panic at the first /api/clips/* call. The
// pin is also the canonical SOLE place that asserts the type —
// downstream callers (composition root + test fixtures) only need
// to satisfy the interface, not re-import this file.
var _ YouTubeClipService = (*youtube.Service)(nil)

// YouTubeClipHandler owns the HTTP transport for YouTube clip operations:
// download, info, topic search, advanced search, diagnostics, and stats. Construction
// mirrors the legacy api/sources package, but lives here (in package
// youtube) so sub-handlers can be tested in isolation.
//
// PR8 (June 2026): added Idempotency field — the reusable Gin
// idempotency middleware instance installed on POST /clips/process
// (the only Write route in this handler). Read routes fall through.
type YouTubeClipHandler struct {
	service     YouTubeClipService
	log         *zap.Logger
	jobsSvc     jobs.Service
	clipsRepo   ytports.ClipStorePort
	toolChecker appassets.ToolChecker
	Idempotency gin.HandlerFunc
	// searchSvc (Wave 4, July 2026): canonical search.Aggregator for
	// SearchCatalog. When nil, the route returns 503.
	searchSvc *search.Aggregator
	// searchFanOut (Wave 4, July 2026): canonical SearchFanOut decorator
	// whose per-backend telemetry is surfaced by Diagnostics (the former
	// GET /api/clips/stats payload).
	searchFanOut search.SearchFanOut
	stockService *stockplan.StockService
} // NewYouTubeClipHandler builds the YouTubeClipHandler.
// service          - YouTube service used by this handler.
// log              - zap logger for diagnostics.
// jobsSvc          - job system used by the async extract endpoint.
// clipsRepo        - canonical YouTube clip-store port.
// toolChecker      - external-tool probe used by Diagnostics.
// idempotencyMiddleware - reusable Gin idempotency middleware; nil disables.
// searchSvc        - canonical search.Aggregator for SearchCatalog.
// searchFanOut     - canonical SearchFanOut decorator for the search
//
//	telemetry surfaced by Diagnostics.
//
// Wave 4 (July 2026): SearchCatalog + search telemetry route through
// the canonical search.Aggregator. SearchCatalog uses searchSvc.Search;
// Diagnostics surfaces searchFanOut.Stats().
//
// PR8 (June 2026): added idempotencyMiddleware to wrap POST /clips/process
// (the only Write route in the handler). Read routes (info, search,
// diagnostics) are unchanged. nil disables idempotency for
// test fixtures.
func NewYouTubeClipHandler(service YouTubeClipService, log *zap.Logger, jobsSvc jobs.Service, clipsRepo ytports.ClipStorePort, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc, searchSvc *search.Aggregator, searchFanOut search.SearchFanOut) *YouTubeClipHandler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	return &YouTubeClipHandler{
		service:      service,
		log:          log,
		jobsSvc:      jobsSvc,
		clipsRepo:    clipsRepo,
		toolChecker:  toolChecker,
		Idempotency:  idem,
		searchSvc:    searchSvc,
		searchFanOut: searchFanOut,
		stockService: nil,
	}
}

// RegisterRoutes wires the YouTube clip endpoints onto the supplied
// gin router group. Mounts on /api/clips/* in production (the assets
// module registers this descriptor on the parent /api group with
// prefix "/clips", producing /api/clips/process, /api/clips/info, etc.).
//
// PR8 (June 2026): POST /process (the YouTube clip extraction job
// enqueue endpoint) installs h.Idempotency so Idempotency-Key replay
// works across retry storms. Read routes (info, search, diagnostics)
// fall through unchanged.
func (h *YouTubeClipHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.Idempotency, h.Extract)
	if h.stockService != nil {
		r.POST("/stock", h.Idempotency, h.SubmitStock)
	}
	r.GET("/info", h.GetVideoInfo)
	r.GET("/search", h.SearchByTopic)
	r.GET("/diagnostics", h.Diagnostics)
}

// Wave 16 PR1 (June 2026): SearchTopics + searchTopicsViaProvider +
// providersToTopicResults removed — canonical search is
// SearchCatalog via GET/POST /api/media/clips/search. See
// architecture/deprecations.yaml#PR-YT-SEARCHTOPICS for the removal
// record (deprecation ID + owner_capability + replacement +
// introduction_date + removal_date + tracking_issue + compatibility_test +
// usage_metric + status).
//
// PR-CLIP-YT-REGISTRY-CLEANUP (June 2026, this PR): the
// provider-registry wiring (resolveProvider + providerSearch field +
// providerReg field + providerResolve sync.Once + providerRegistry ctor
// arg + providers import) was COLLAPSED here. The handler is now
// transport-only against the canonical clip-repo; the providers.Registry
// continues to exist as a composition-root dispatch concern (root.Search.
// ProviderRegistry in WireRegistry→pr.Freeze()) but does not reach any
// per-handler field. See architecture/deprecations.yaml#PR-CLIP-YT-REGISTRY-CLEANUP
// for the deprecation record.

// GetVideoInfo returns metadata for a single YouTube URL.
func (h *YouTubeClipHandler) GetVideoInfo(c *gin.Context) {
	url := c.Query("url")
	if url == "" {
		apiutil.BadRequest(c, "url parameter is required")
		return
	}

	metadata, err := h.service.GetVideoInfo(c.Request.Context(), url)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, metadata)
}

// S3d (June 2026) removal: getAllClipRepos() is REMOVED.
// SearchCatalog + search telemetry (Diagnostics) now route through
// the canonical *search.Aggregator. clipsRepo stays for downstream
// uses (reprocess / download paths that don't aggregate provider fan-out).
