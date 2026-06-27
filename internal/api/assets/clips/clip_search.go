// Package clips — AdvancedSearch endpoint (Wave 21 PR 10 CUTOVER).
//
// PR 10 (June 2026): the AdvancedSearch endpoint now consumes
// *search.Aggregator (the canonical Search capability, Wave 21 SSOT)
// directly. The legacy *internal/application/assets/clipssearch.Service
// was removed in PR 10 (see architecture/deprecations.yaml
// PR-SEARCH-LEGACY-CLIPSSEARCH). HTTP wire shape is preserved at
// the field-name level ({ok, total, count, limit, offset, clips})
// but the population now flows through the canonical Aggregator
// (fanout → 4-key dedup → ranking cursor) rather than the legacy
// per-source AdvancedSearchRepo fan-out.
//
// X-Deprecation header is set on every response so monitoring
// dashboards and migration tooling can spot callers still using
// the legacy shape during the migration window.
package clips

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// AdvancedSearch performs a multi-source clip search with structured filters.
//
//	@Summary		Advanced clip search with filters
//	@Description	Search media assets with structured filters (category, date range,
//	@Description	duration, transcript, source, Drive link).
//	@Tags			search
//	@Accept			json
//	@Produce		json
//	@Success		200  {object} object
//	@Header			200  {string}  X-Deprecation    "true (Wave 21 PR 10 cutover)"
//	@Router			/api/media/search/advanced [post]
func (h *Handler) AdvancedSearch(c *gin.Context) {
	if h.searchSvc == nil {
		h.setDeprecationHeader(c)
		apiutil.InternalError(c, fmt.Errorf("advanced search aggregator not available"))
		return
	}

	var req asset.AdvancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.setDeprecationHeader(c)
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	q := translateAdvanceRequestToQuery(req)
	res, err := h.searchSvc.Search(c.Request.Context(), q)
	if err != nil {
		h.setDeprecationHeader(c)
		apiutil.InternalError(c, err)
		return
	}

	h.setDeprecationHeader(c)
	apiutil.OK(c, gin.H{
		"ok":     true,
		"total":  len(res.Items),
		"count":  len(res.Items),
		"limit":  req.Limit,
		"offset": req.Offset,
		"clips":  toAssetResults(res),
	})
}

// setDeprecationHeader installs the Wave 21 PR 10 cutover sentinel
// header on every response from this route. Dashboards and migration
// tooling grep for X-Deprecation: true to find legacy consumers.
func (h *Handler) setDeprecationHeader(c *gin.Context) {
	c.Header("X-Deprecation", "true")
	c.Header("X-Deprecation-Migration", "aggregator")
	c.Header("Link", `</api/v2/search>; rel="successor-version"`)
}

// translateAdvanceRequestToQuery converts the legacy
// asset.AdvancedSearchRequest into the canonical search.Query the
// Aggregator consumes. Kept as a package-local function so the
// Wave 19 cross-capability import rule is preserved (search package
// is stdlib-only; the api layer owns the bridge).
func translateAdvanceRequestToQuery(req asset.AdvancedSearchRequest) search.Query {
	limit := req.Limit
	if limit <= 0 {
		limit = 50 // legacy default
	}
	var mediaTypes []string
	if req.Source != "" && req.Source != "all" && req.Source != "unified" {
		mediaTypes = []string{req.Source}
	}
	return search.Query{
		Text:  req.Q,
		Limit: limit,
		Mode:  search.SearchModeHybrid,
		Filters: search.Filters{
			Source:        req.Source,
			MediaType:     req.Source,
			Tags:          nil, // legacy AdvancedSearchRequest does not expose Tags
			DurationMsMin: req.MinDuration * 1000,
		},
		MediaTypes: mediaTypes,
	}
}

// toAssetResults converts the Aggregator's canonical search.Result
// back into []*asset.Asset for the legacy envelope shape
// ({ok,total,count,limit,offset,clips}). Uses typed-string conversions
// because asset.Asset's Source/MediaType fields are typed names
// (`type Source string`, `type MediaType string`) per the asset
// domain — not plain strings.
func toAssetResults(r *search.Result) []*asset.Asset {
	if r == nil {
		return nil
	}
	out := make([]*asset.Asset, 0, len(r.Items))
	for _, c := range r.Items {
		out = append(out, &asset.Asset{
			ID:        c.AssetID,
			Name:      c.Title,
			Source:    asset.Source(c.Source),
			MediaType: asset.MediaType(c.MediaType),
		})
	}
	return out
}
