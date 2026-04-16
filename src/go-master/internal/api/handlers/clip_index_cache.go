package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CacheStatus returns cache statistics
// @Summary Get cache status
// @Description Get suggestion cache statistics
// @Tags Clip Index
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/cache [get]
func (h *ClipIndexHandler) CacheStatus(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	cache := h.indexer.GetCache()
	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"cache_size": cache.Size(),
		"max_size":   500,
	})
}

// ClearCache clears the suggestion cache
// @Summary Clear cache
// @Description Clear the suggestion cache
// @Tags Clip Index
// @Post /clip/index/cache/clear
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/cache/clear [post]
func (h *ClipIndexHandler) ClearCache(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	h.indexer.GetCache().Clear()
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Cache cleared",
	})
}
