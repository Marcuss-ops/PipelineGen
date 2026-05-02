package artlist

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/apiutil"
)

func (h *Handler) SyncCatalogs(c *gin.Context) {
	if h.catalogSync == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "catalog sync service not configured")
		return
	}

	summary, err := h.catalogSync.SyncAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, summary)
		return
	}

	apiutil.OK(c, summary)
}
