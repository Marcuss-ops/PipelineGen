package catalog

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/catalogdb"
)

type CatalogHandler struct {
	catalog *catalogdb.CatalogDB
}

type catalogSearchRequest struct {
	Query       string `json:"query"`
	Source      string `json:"source"`
	FolderID    string `json:"folder_id"`
	Limit       int    `json:"limit"`
	MinDuration int    `json:"min_duration"`
	MaxDuration int    `json:"max_duration"`
}

func NewCatalogHandler(catalog *catalogdb.CatalogDB) *CatalogHandler {
	return &CatalogHandler{catalog: catalog}
}

func (h *CatalogHandler) RegisterRoutes(rg *gin.RouterGroup) {
	catalog := rg.Group("/catalog")
	{
		catalog.POST("/reindex-bridge", h.Search)
	}
}

func (h *CatalogHandler) RegisterPublicRoutes(rg *gin.RouterGroup) {
	catalog := rg.Group("/catalog")
	{
		catalog.GET("/stats", h.GetStats)
		catalog.POST("/search", h.Search)
		catalog.POST("/suggest", h.Suggest)
		catalog.GET("/clips/:id", h.GetClip)
	}
}

func (h *CatalogHandler) GetStats(c *gin.Context) {
	if h == nil || h.catalog == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "catalog unavailable"})
		return
	}
	stats, err := h.catalog.GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

func (h *CatalogHandler) Search(c *gin.Context) {
	if h == nil || h.catalog == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "catalog unavailable"})
		return
	}
	var req catalogSearchRequest
	_ = c.ShouldBindJSON(&req)
	if req.Query == "" {
		req.Query = c.Query("q")
	}
	results, err := h.catalog.SearchClips(catalogdb.SearchOptions{
		Query:       req.Query,
		Source:      req.Source,
		FolderID:    req.FolderID,
		Limit:       req.Limit,
		MinDuration: req.MinDuration,
		MaxDuration: req.MaxDuration,
		OnlyActive:  true,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results, "count": len(results)})
}

func (h *CatalogHandler) Suggest(c *gin.Context) {
	if h == nil || h.catalog == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "catalog unavailable"})
		return
	}
	var req catalogSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil && req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 12
	}
	results, err := h.catalog.SearchClips(catalogdb.SearchOptions{
		Query:       query,
		Source:      req.Source,
		FolderID:    req.FolderID,
		Limit:       limit,
		MinDuration: req.MinDuration,
		MaxDuration: req.MaxDuration,
		OnlyActive:  true,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	best := interface{}(nil)
	if len(results) > 0 {
		best = results[0]
	}
	c.JSON(http.StatusOK, gin.H{
		"query":     query,
		"best_match": best,
		"results":   results,
		"count":     len(results),
	})
}

func (h *CatalogHandler) GetClip(c *gin.Context) {
	if h == nil || h.catalog == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "catalog unavailable"})
		return
	}
	clip, err := h.catalog.GetClip(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if clip == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "clip not found"})
		return
	}
	c.JSON(http.StatusOK, clip)
}
