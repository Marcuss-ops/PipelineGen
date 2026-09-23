// Package search (api/assets/search) — handler.go is the thin HTTP
// transport for the unified media search surface.
//
// Blocco A2 consolidation (June 2026): all independent search endpoints
// (Artlist /search + /search/live, YouTube /search, clips /search/advanced,
// scraper /search, /api/assets/search) are absorbed into a single
// POST /api/media/search endpoint. The handler delegates to the canonical
// Aggregator. Response envelope is the canonical Result
// projection (items, next_cursor, partial, provider_errors).
//
// Body: { "query": "text", "sources": ["youtube","artlist"], "mode": "hybrid",
//
//	"filters": { "source": "...", "media_type": "video", ... }, "limit": 20 }
package search

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	assetresolver "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/resolver"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// Handler is the thin HTTP transport for canonical Aggregator-backed
// unified media search. Wire shape: POST /api/media/search with JSON body.
type Handler struct {
	aggreg   *Aggregator
	resolver interface {
		Resolve(context.Context, assetresolver.Request) (*providers.FetchedAsset, error)
	}
	log *zap.Logger
}

// NewHandler constructs the canonical Aggregator-backed SearchHandler.
func NewHandler(aggreg *Aggregator, resolver interface {
	Resolve(context.Context, assetresolver.Request) (*providers.FetchedAsset, error)
}, log *zap.Logger) *Handler {
	return &Handler{aggreg: aggreg, resolver: resolver, log: log}
}

// RegisterRoutes registers search routes under the given group.
// The /api/media/search route is mounted by the assetsapi module.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/search", h.Search)
	r.POST("/resolve/:asset_id", h.Resolve)
}

type resolveRequest struct {
	Source        string `json:"source"`
	SourceRef     string `json:"source_ref"`
	DestinationID string `json:"destination_id,omitempty"`
	NoAudio       bool   `json:"no_audio,omitempty"`
}

// Resolve materializes only the selected asset. Search remains metadata-only.
func (h *Handler) Resolve(c *gin.Context) {
	if h.resolver == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "asset resolver not wired")
		return
	}
	req, ok := apiutil.BindJSON[resolveRequest](c)
	if !ok {
		return
	}
	assetID := strings.TrimSpace(c.Param("asset_id"))
	if assetID == "" || strings.TrimSpace(req.Source) == "" || strings.TrimSpace(req.SourceRef) == "" {
		apiutil.BadRequest(c, "asset_id, source and source_ref are required")
		return
	}
	resolved, err := h.resolver.Resolve(c.Request.Context(), assetresolver.Request{
		Source: req.Source, AssetID: assetID, SourceRef: req.SourceRef,
		DestinationID: req.DestinationID, NoAudio: req.NoAudio,
	})
	if err != nil {
		if errors.Is(err, assetresolver.ErrNotWired) || errors.Is(err, assetresolver.ErrProviderMissing) || errors.Is(err, assetresolver.ErrUnsupported) {
			apiutil.Error(c, http.StatusServiceUnavailable, err.Error())
			return
		}
		apiutil.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "asset_id": assetID, "source": req.Source, "local_path": resolved.LocalPath, "bytes": resolved.Bytes})
}

// searchRequest is the canonical JSON body for POST /api/media/search.
//
// Field semantics (Blocco A2):
//   - query     — Text field for canonical Query
//   - sources   — Source filter (empty=all: artlist,youtube,stock,clips,sound_effect)
//   - mode      — "hybrid" (default) or "ann" for semantic backend
//   - filters   — Structured filters; YouTube discovery accepts sort=relevance|newest|oldest|longest|shortest|views and published_after as an RFC3339 inclusive date floor.
//   - min_score — Optional inclusive score floor in [0,1], forwarded to capable catalog backends and applied uniformly to merged results.
//   - limit     — Page size. Aggregator clamps to DefaultLimit / MaxLimit.
//   - cursor    — Opaque base64-JSON pagination token.
type searchRequest struct {
	Query    string              `json:"query"`
	Sources  []string            `json:"sources,omitempty"`
	Mode     string              `json:"mode,omitempty"`
	Universe string              `json:"universe,omitempty"` // "catalog" (default) | "discovery" | "blended"
	Filters  searchRequestFilter `json:"filters,omitempty"`
	MinScore float64             `json:"min_score,omitempty"`
	Limit    int                 `json:"limit,omitempty"`
	Cursor   string              `json:"cursor,omitempty"`
}

type searchRequestFilter struct {
	Source         string          `json:"source,omitempty"`
	MediaType      string          `json:"media_type,omitempty"`
	Category       string          `json:"category,omitempty"`
	Language       string          `json:"language,omitempty"`
	Tags           []string        `json:"tags,omitempty"`
	Sort           string          `json:"sort,omitempty"`
	PublishedAfter json.RawMessage `json:"published_after,omitempty"`
	// AssetKind / SemanticRole are the canonical taxonomy dimensions,
	// distinct from Source (physical provenance). They must be declared here:
	// the JSON binder drops unknown keys silently, so before they existed
	// `filters: {"asset_kind": "stock_video"}` was accepted with HTTP 200 and
	// returned the FULL unfiltered result set — a filter that appears honoured
	// and is not, which is worse than an explicit 400.
	AssetKind     string `json:"asset_kind,omitempty"`
	SemanticRole  string `json:"semantic_role,omitempty"`
	DurationMsMin int    `json:"duration_ms_min,omitempty"`
}

// Search is the HTTP handler for POST /api/media/search.
//
// Status codes:
//
//	200 — OK, canonical Result envelope in body
//	400 — invalid query (empty query, malformed cursor)
//	422 — invalid cursor (semantic error from Aggregator)
//	500 — internal error from Aggregator fanout (providerErrors populated)
//	503 — search aggregator not wired
func parsePublishedAfter(raw json.RawMessage) (*time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func containsSource(sources []string, want string) bool {
	for _, source := range sources {
		if canonicalSourceName(source) == want {
			return true
		}
	}
	return false
}

func (h *Handler) Search(c *gin.Context) {
	if h.aggreg == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "search aggregator not wired")
		return
	}

	req, ok := apiutil.BindJSON[searchRequest](c)
	if !ok {
		return // 400 already rendered
	}

	q := strings.TrimSpace(req.Query)
	if q == "" {
		apiutil.BadRequest(c, "query is required")
		return
	}
	limit := defaults.Int(req.Limit, DefaultLimit)
	if universe := strings.TrimSpace(req.Universe); universe != "" && !IsValidUniverse(universe) {
		apiutil.BadRequest(c, "universe must be one of catalog, discovery, blended")
		return
	}
	if math.IsNaN(req.MinScore) || math.IsInf(req.MinScore, 0) || req.MinScore < 0 || req.MinScore > 1 {
		apiutil.BadRequest(c, "min_score must be between 0 and 1")
		return
	}
	publishedAfter, err := parsePublishedAfter(req.Filters.PublishedAfter)
	if err != nil {
		apiutil.BadRequest(c, "filters.published_after must be an RFC3339 timestamp")
		return
	}
	sortMode := strings.ToLower(strings.TrimSpace(req.Filters.Sort))
	if sortMode != "" && sortMode != "relevance" && (ParseUniverse(req.Universe) != SearchDiscovery || !containsSource(req.Sources, "youtube")) {
		apiutil.BadRequest(c, "filters.sort is supported only for YouTube discovery")
		return
	}
	if publishedAfter != nil && (ParseUniverse(req.Universe) != SearchDiscovery || !containsSource(req.Sources, "youtube")) {
		apiutil.BadRequest(c, "filters.published_after is supported only for YouTube discovery")
		return
	}
	switch sortMode {
	case "", "relevance", "newest", "oldest", "longest", "shortest", "views":
	default:
		apiutil.BadRequest(c, "filters.sort must be one of relevance, newest, oldest, longest, shortest, views")
		return
	}

	// Parse mode from the wire value; unknown/empty defaults to hybrid.
	mode := ParseMode(req.Mode)

	// PR-SEARCH-HANDLER-MOUNT (July 2026): the /api/media/search
	// endpoint is admin-authenticated. Set IsAdmin=true so the
	// semantic backend's CompileQdrantFilter skips the workspace
	// must-clause (IsSystem=true path). Without this, every admin
	// search with mode=hybrid fails with
	// "SearchScope.WorkspaceID is required" from the Qdrant
	// filter compiler, producing 503 + ErrSemanticBackendUnavailable.
	actor := Actor{IsAdmin: true, IsSystem: true}
	if isAdmin, ok := c.Get("is_admin"); ok {
		if adminFlag, ok2 := isAdmin.(bool); ok2 {
			actor.IsAdmin = adminFlag
			actor.IsSystem = adminFlag
		}
	}

	res, err := h.aggreg.Search(c.Request.Context(), Query{
		Text:     q,
		Sources:  req.Sources,
		Limit:    limit,
		MinScore: req.MinScore,
		Mode:     mode,
		Universe: ParseUniverse(req.Universe),
		Cursor:   req.Cursor,
		Actor:    actor,
		Filters: Filters{
			Source:         strings.TrimSpace(req.Filters.Source),
			MediaType:      strings.TrimSpace(req.Filters.MediaType),
			Category:       strings.TrimSpace(req.Filters.Category),
			Language:       strings.TrimSpace(req.Filters.Language),
			Tags:           req.Filters.Tags,
			AssetKind:      strings.TrimSpace(req.Filters.AssetKind),
			SemanticRole:   strings.TrimSpace(req.Filters.SemanticRole),
			DurationMsMin:  req.Filters.DurationMsMin,
			Sort:           sortMode,
			PublishedAfter: publishedAfter,
		},
	})
	if err != nil {
		// Use errors.Is because the Aggregator wraps sentinels with fmt.Errorf("%w: ...").
		// A bare switch on the error value would miss wrapped forms (e.g. ErrAllBackendsFailed
		// is wrapped as "%w: N backend(s) failed").
		switch {
		case errors.Is(err, ErrInvalidCursor):
			apiutil.Error(c, http.StatusUnprocessableEntity, "invalid cursor")
		case errors.Is(err, ErrCursorEncoding):
			h.log.Error("search cursor encoding failed", zap.Error(err))
			apiutil.InternalError(c, err)
		case errors.Is(err, ErrAllBackendsFailed):
			// 502 Bad Gateway — every eligible backend errored.
			// res.ProviderErrors carries per-backend diagnostics (e.g.
			// {"semantic":"embed channel ...", "local":"no such column: status"}).
			// Expose them so the caller can diagnose without server-log access.
			h.log.Error("search: all backends failed",
				zap.Error(err),
				zap.Any("provider_errors", func() map[string]string {
					if res != nil {
						return res.ProviderErrors
					}
					return nil
				}()))
			providerErrs := map[string]string{}
			if res != nil {
				providerErrs = res.ProviderErrors
			}
			c.JSON(http.StatusBadGateway, gin.H{
				"ok":              false,
				"error":           err.Error(),
				"provider_errors": providerErrs,
			})
		case errors.Is(err, ErrNoEligibleBackends),
			errors.Is(err, ErrSemanticBackendUnavailable):
			// 503 Service Unavailable — no backend matches the query filters.
			apiutil.Error(c, http.StatusServiceUnavailable, err.Error())
		default:
			h.log.Error("search failed", zap.Error(err))
			apiutil.InternalError(c, err)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":           res.Items,
		"next_cursor":     res.NextCursor,
		"partial":         res.Partial,
		"provider_errors": res.ProviderErrors,
	})
}
