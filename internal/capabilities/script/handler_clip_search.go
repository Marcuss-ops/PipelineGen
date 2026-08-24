// Package script — handler_clip_search.go exposes a lightweight
// GET /script/clips/search?q=... endpoint so operators and UIs can
// discover media_asset IDs without querying SQLite directly.
//
// PR-FIX (June 2026): before this endpoint, the user had to manually
// query SQLite or Qdrant (whose point IDs differ from media_assets.id)
// to find clips for source.type="clips". Now a single GET returns
// matching clip IDs with their names, source, and drive links.
package script

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ClipSearchHit is a lightweight result for clip discovery.
type ClipSearchHit struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	DriveLink string `json:"drive_link,omitempty"`
}

// ClipSearcher is the narrow port for searching media_assets by name.
// The production adapter queries SQLite media_assets with LIKE.
type ClipSearcher interface {
	SearchByName(ctx context.Context, query string, limit int) ([]ClipSearchHit, error)
}

// SearchClipsByName handles GET /script/clips/search?q=<query>&limit=<n>.
//
// Query parameter "q" is required. "limit" defaults to 20, max 100.
// Returns a JSON array of {id, name, source, drive_link} objects
// matching the query (case-insensitive LIKE %query%).
func (h *ScriptFlowHandler) SearchClipsByName(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}

	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}

	if h.clipsSearcher == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip search not configured")
		return
	}

	hits, err := h.clipsSearcher.SearchByName(c.Request.Context(), q, limit)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"query": q,
		"count": len(hits),
		"clips": hits,
	})
}
