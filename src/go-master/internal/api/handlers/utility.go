package handlers

import (
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
