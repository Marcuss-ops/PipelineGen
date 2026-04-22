package clip

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ListClips godoc
// @Summary List all clips in the index
// @Description Returns a list of all indexed clips. Accessible from remote machines.
// @Tags clip-index
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/clips [get]
func (h *ClipIndexHandler) ListClips(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	index := h.indexer.GetIndex()
	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"clips": index.Clips,
	})
}

// GetClip godoc
// @Summary Get a specific clip by ID
// @Description Get details of a specific clip from the index. Accessible from remote machines.
// @Tags clip-index
// @Param id path string true "Clip ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /clip/index/clips/{id} [get]
func (h *ClipIndexHandler) GetClip(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	clipID := c.Param("id")
	index := h.indexer.GetIndex()

	for _, clip := range index.Clips {
		if clip.ID == clipID {
			c.JSON(http.StatusOK, gin.H{
				"ok":   true,
				"clip": clip,
			})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{
		"ok":      false,
		"error":   "Clip not found",
		"clip_id": clipID,
	})
}
