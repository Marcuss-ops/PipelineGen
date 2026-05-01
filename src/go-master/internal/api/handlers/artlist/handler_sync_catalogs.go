package artlist

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) SyncCatalogs(c *gin.Context) {
	if h.catalogSync == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "catalog sync service not configured"})
		return
	}

	summary, err := h.catalogSync.SyncAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, summary)
		return
	}

	c.JSON(http.StatusOK, summary)
}
