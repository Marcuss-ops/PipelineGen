package common

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/slugify"
)

type UtilityHandler struct{}

func NewUtilityHandler() *UtilityHandler {
	return &UtilityHandler{}
}

func (h *UtilityHandler) Slugify(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing query parameter q"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"input": q,
		"slug":  slugify.Marshal(q),
	})
}

// ParseFloatQuery helper: parse float query parameter
func ParseFloatQuery(c *gin.Context, key string, defaultVal float64) float64 {
	if val := c.Query(key); val != "" {
		var result float64
		if _, err := fmt.Sscanf(val, "%f", &result); err == nil {
			return result
		}
	}
	return defaultVal
}

// ParseIntQuery helper: parse int query parameter
func ParseIntQuery(c *gin.Context, key string, defaultVal int) int {
	if val := c.Query(key); val != "" {
		var result int
		if _, err := fmt.Sscanf(val, "%d", &result); err == nil {
			return result
		}
	}
	return defaultVal
}
