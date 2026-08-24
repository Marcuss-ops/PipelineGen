package mediasearch

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// mapSearchError translates typed sentinels from the aggregator layer
// into HTTP status codes.
//
// Commit 2 BACKFILL/CUTOVER: ErrMissingWorkspace now matches the
// canonical search.ErrMissingWorkspace (godlike/06 SSOT). The legacy
// mediasearch.* sentinels that have NO canonical search counterpart
// yet (ErrHybridRequiresSparse, ErrNoBackendAvailable,
// ErrAllBackendsFailed) keep the deprecation alias import. They are
// application-level "fail-closed" sentinels; the BACKFILL wave that
// ports them into search/ is tracked in
// architecture/deprecations.yaml#SEARCH-MEDIASEARCH-CONTRACT-WAVE.
func (h *Handler) mapSearchError(c *gin.Context, err error, workspaceID string) {
	switch {
	case errors.Is(err, search.ErrInvalidCursor):
		apiutil.Error(c, http.StatusUnprocessableEntity, "invalid cursor")
	case errors.Is(err, search.ErrMissingWorkspace):
		apiutil.Error(c, http.StatusForbidden, "workspace_id required in context")
	case errors.Is(err, search.ErrHybridRequiresSparse):
		apiutil.Error(c, http.StatusUnprocessableEntity,
			"hybrid mode unavailable: sparse vector channel or BM25 tokenizer not configured")
	case errors.Is(err, search.ErrNoBackendAvailable):
		apiutil.Error(c, http.StatusServiceUnavailable,
			"no search backend available for the requested query")
	case errors.Is(err, search.ErrAllBackendsFailed):
		apiutil.Error(c, http.StatusBadGateway,
			"all search backends failed to return results")
	default:
		h.safeError("mediasearch.Search failed",
			zap.String("workspace", workspaceID),
			zap.Error(err))
		apiutil.InternalError(c, err)
	}
}
