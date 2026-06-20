package sources

import (
	"strings"

	"github.com/gin-gonic/gin"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"go.uber.org/zap"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
)

// SearchRequest represents a search request
type SearchRequest struct {
	Q     string `form:"q" binding:"required"`
	Type  string `form:"type"` // video, image, audio, all
	Limit int    `form:"limit,default=20"`
	Sort  string `form:"sort"`
}

// Search dispatches source-level searches via the canonical
// providers.Registry. Every registered SearchProvider is fanned
// out uniformly and the response is keyed by Provider.Name().
//
// Capability filtering (req.Type):
//
//   - "" / "all" / "video" → every SearchProvider;
//   - "audio"              → providers advertising CapabilityMusic;
//   - "image"              → providers advertising CapabilityImage.
//
// Local DB searches (catalogRepo, clipsRepo) are NOT routed via the
// registry: they are internal-state queries, not source providers.
// They remain direct dispatches.
func (h *Handler) Search(c *gin.Context) {
	var req SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	results := gin.H{}

	// ── Source-level search dispatch ──────────────────────────────────
	// Fan out to every registered SearchProvider via the canonical
	// providers.Registry. When providerRegistry is nil (e.g. unit
	// tests that construct the handler without a registry), the
	// loop body simply never executes — no results returned for
	// source-level searches.
	for _, p := range h.providers() {
		if !typeAllowed(p.Capabilities(), req.Type) {
			// Surface silent filter rejections so CI / operator logs
			// catch typos like req.Type="vidio" early. Debug level
			// (not Warn) keeps the boot path quiet for the expected
			// ""/"all"/"video" defaults.
			h.log.Debug("provider excluded by SearchRequest.Type filter",
				zap.String("provider", p.Name()),
				zap.String("requested_type", req.Type))
			continue
		}
		sp, ok := p.(providers.SearchProvider)
		if !ok {
			// Defensive: ByCapability already filtered to SearchProvider-capable
			// adapters in practice, but a future registry bug could surface here.
			h.log.Warn("provider asserted CapabilitySearch but not SearchProvider interface",
				zap.String("provider", p.Name()))
			continue
		}
		sr := providers.SearchRequest{
			Query: req.Q,
			Limit: req.Limit,
			Filters: providers.SearchFilters{
				Sort: providers.SortMode(req.Sort),
			},
		}
		out, err := sp.Search(c.Request.Context(), sr)
		source := p.Name()
		if err != nil {
			h.log.Warn("provider search failed", zap.String("provider", source), zap.Error(err))
			results[source] = gin.H{
				"count":   0,
				"results": []any{},
				"error":   err.Error(),
			}
			continue
		}
		results[source] = gin.H{
			"count":   len(out.Candidates),
			"results": out.Candidates,
			"source":  source,
			// NextPageToken is exposed for callers that want
			// cursor-based pagination. Empty string means "no
			// more pages" per the providers.SearchResult contract.
			"next_page_token": out.NextPageToken,
		}
	}

	// ── Local DB searches (registry-independent) ──────────────────────
	// Catalog + media_assets table queries are NOT provider-shaped —
	// they are internal-state lookups. Kept as direct dispatches
	// outside the ByCapability loop.
	if h.catalogRepo != nil {
		catalogResults, err := h.catalogRepo.SearchAll(c.Request.Context(), req.Q)
		if err != nil {
			h.log.Warn("catalog search failed", zap.Error(err))
		} else {
			results["catalog"] = gin.H{
				"count":   len(catalogResults),
				"results": catalogResults,
			}
		}
	}

	if h.clipsRepo != nil && (req.Type == "" || req.Type == "video" || req.Type == "all") {
		localClips, err := h.clipsRepo.SearchClips(c.Request.Context(), "all", req.Q)
		if err != nil {
			h.log.Warn("local unified search failed", zap.Error(err))
		} else {
			results["local"] = gin.H{
				"count":   len(localClips),
				"results": localClips,
			}
		}
	}

	apiutil.OK(c, gin.H{
		"query":   req.Q,
		"type":    req.Type,
		"results": results,
	})
}

// typeAllowed decides whether a provider advertising the given
// capabilities should run for the user-requested SearchRequest.Type.
//
// Mapping rationale:
//   - "" / "all" / "video": no filter (matches legacy Search
//     behaviour which always allowed video+music upstreams);
//   - "audio": only providers with CapabilityMusic (voice/music
//     branches defer to ingest use case later);
//   - "image": only providers with CapabilityImage.
//
// Providers declaring other capabilities (eg. voice) are not
// currently returned by Search and so are not considered here.
// providers returns the search-capable providers registered in the
// canonical registry, or an empty slice when no registry is wired.
func (h *Handler) providers() []providers.Provider {
	if h.providerRegistry == nil {
		return nil
	}
	return h.providerRegistry.ByCapability(providers.CapabilitySearch)
}

func typeAllowed(caps []providers.Capability, reqType string) bool {
	switch reqType {
	case "", "all", "video":
		return true
	case "audio":
		for _, c := range caps {
			if c == providers.CapabilityMusic {
				return true
			}
		}
	case "image":
		for _, c := range caps {
			if c == providers.CapabilityImage {
				return true
			}
		}
	}
	return false
}

// Stats godoc
func (h *Handler) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	stats, err := h.assetIndexSvc.GetStats(ctx)
	if err != nil {
		h.log.Error("failed to get asset stats", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"total":     stats.Total,
		"by_type":   stats.ByType,
		"by_status": stats.ByStatus,
	})
}
