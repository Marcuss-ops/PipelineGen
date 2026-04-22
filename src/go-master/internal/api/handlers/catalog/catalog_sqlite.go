package catalog

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/clipdb"
)

type CatalogSQLiteHandler struct {
	db *clipdb.SQLiteDB
}

func NewCatalogSQLiteHandler(dbPath string) (*CatalogSQLiteHandler, error) {
	db, err := clipdb.OpenSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	return &CatalogSQLiteHandler{db: db}, nil
}

func (h *CatalogSQLiteHandler) RegisterRoutes(rg *gin.RouterGroup) {
	clips := rg.Group("/clips")
	{
		clips.GET("/resolve", h.Resolve)
	}
}

func (h *CatalogSQLiteHandler) Resolve(c *gin.Context) {
	tagsParam := c.Query("tags")
	minDur := common.ParseFloatQuery(c, "min_duration", 0)
	maxDur := common.ParseFloatQuery(c, "max_duration", 0)

	var tags []string
	if tagsParam != "" {
		tags = strings.Split(tagsParam, ",")
	}

	params := clipdb.QueryParams{
		Tags:   tags,
		MinDur: minDur,
		MaxDur: maxDur,
	}

	clips, err := h.db.Resolve(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"count": len(clips),
		"clips": clips,
	})
}
