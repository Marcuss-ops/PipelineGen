package common

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/pkg/textutil"
)

type CatalogHandler struct {
	repo *catalog.Repository
}

func NewCatalogHandler(repo *catalog.Repository) *CatalogHandler {
	return &CatalogHandler{
		repo: repo,
	}
}

func (h *CatalogHandler) SearchFolders(c *gin.Context) {
	q := textutil.NormalizeQuery(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing query parameter q"})
		return
	}

	results, err := h.repo.SearchAll(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"query":   q,
		"count":   len(results),
		"results": results,
	})
}
