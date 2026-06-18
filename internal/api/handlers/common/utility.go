package common

import (
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"github.com/gin-gonic/gin"
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
		"slug":  textutil.Slugify(q),
	})
}
