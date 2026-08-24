// Package admin — handler_research_cache.go: operational endpoint to
// invalidate research_cache rows without manual DELETE statements.
//
// POST /admin/research/cache/invalidate (mounted at
// /api/admin/research/cache/invalidate) accepts:
//
//	{"scope": "aggregate" | "candidate", "topic": "...", "ranking_metric": "..."}
//
// The scope disambiguates the three research-cache layers the operator can
// target:
//
//   - candidate evidence cache (scope=candidate): per-candidate rows
//     (resolver_version = "webresearch"), keyed by the candidate canonical name.
//   - aggregate cache (scope=aggregate): the fanout aggregate row
//     (resolver_version = "webresearch-fanout").
//   - ranking cache (scope=aggregate): the ranking is NOT a separate table —
//     it is persisted inside the aggregate row's research_report_json.ranking,
//     so invalidating the aggregate also invalidates its ranking.
//
// godlike/07 NO-FAKE-AVAILABILITY: the handler performs a real DELETE against
// the wired repository and returns the number of rows removed. It does not
// return a canned success.
package admin

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ResearchCacheInvalidator is the narrow port the admin handler depends on to
// invalidate research_cache entries by scope. The production binding is the
// SQLite topicsourcecache.Repository.
type ResearchCacheInvalidator interface {
	// DeleteResearchCache deletes rows matching scope (aggregate|candidate)
	// narrowed by topic and, for aggregate scope, an optional ranking_metric.
	DeleteResearchCache(ctx context.Context, scope, topic, rankingMetric string) (int64, error)
}

// ResearchCacheInvalidateRequest is the JSON body for
// POST /admin/research/cache/invalidate.
type ResearchCacheInvalidateRequest struct {
	// Scope selects the cache layer: "aggregate" (fanout aggregate row,
	// including its embedded ranking) or "candidate" (per-candidate evidence).
	Scope string `json:"scope"`

	// Topic narrows the match: the parent topic for aggregate scope, the
	// candidate canonical name for candidate scope.
	Topic string `json:"topic"`

	// RankingMetric optionally narrows aggregate invalidation to the rows
	// whose ranking requested_metric matches. Ignored for candidate scope.
	RankingMetric string `json:"ranking_metric,omitempty"`
}

// ResearchCacheInvalidateResponse is the JSON response for a successful
// invalidation. Layers records which cache layers were invalidated.
type ResearchCacheInvalidateResponse struct {
	OK      bool     `json:"ok"`
	Deleted int64    `json:"deleted"`
	Scope   string   `json:"scope"`
	Topic   string   `json:"topic"`
	Layers  []string `json:"layers"`
}

// ResearchCacheInvalidateHandler serves POST /admin/research/cache/invalidate.
type ResearchCacheInvalidateHandler struct {
	invalidator ResearchCacheInvalidator
	log         *zap.Logger
}

// NewResearchCacheInvalidateHandler constructs the handler with its
// mandatory dependency. A nil logger falls back to zap.NewNop().
func NewResearchCacheInvalidateHandler(inv ResearchCacheInvalidator, log *zap.Logger) *ResearchCacheInvalidateHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &ResearchCacheInvalidateHandler{invalidator: inv, log: log}
}

// RegisterRoutes mounts the cache/invalidate endpoint. Caller is responsible
// for attaching RequireAdminToken middleware.
func (h *ResearchCacheInvalidateHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/cache/invalidate", h.Invalidate)
}

// Invalidate handles POST /admin/research/cache/invalidate.
func (h *ResearchCacheInvalidateHandler) Invalidate(c *gin.Context) {
	req, ok := apiutil.BindJSON[ResearchCacheInvalidateRequest](c)
	if !ok {
		return
	}

	scope := strings.TrimSpace(req.Scope)
	topic := strings.TrimSpace(req.Topic)
	metric := strings.TrimSpace(req.RankingMetric)

	if scope != "aggregate" && scope != "candidate" {
		apiutil.BadRequest(c, "scope must be one of: aggregate, candidate")
		return
	}
	if topic == "" {
		apiutil.BadRequest(c, "topic is required")
		return
	}

	if h.invalidator == nil {
		h.log.Error("research cache invalidate: invalidator not wired")
		apiutil.Error(c, http.StatusServiceUnavailable, "research cache invalidator not wired — check composition root")
		return
	}

	deleted, err := h.invalidator.DeleteResearchCache(c.Request.Context(), scope, topic, metric)
	if err != nil {
		h.log.Error("research cache invalidate failed",
			zap.String("scope", scope),
			zap.String("topic", topic),
			zap.String("ranking_metric", metric),
			zap.Error(err),
		)
		apiutil.InternalError(c, err)
		return
	}

	// The ranking is embedded in the aggregate row, so aggregate invalidation
	// touches both the assembled evidence pack and the ranking decision.
	layers := []string{"candidate"}
	if scope == "aggregate" {
		layers = []string{"aggregate", "ranking"}
	}

	h.log.Info("research cache invalidated",
		zap.String("scope", scope),
		zap.String("topic", topic),
		zap.String("ranking_metric", metric),
		zap.Int64("deleted", deleted),
	)

	apiutil.OK(c, ResearchCacheInvalidateResponse{
		OK:      true,
		Deleted: deleted,
		Scope:   scope,
		Topic:   topic,
		Layers:  layers,
	})
}
