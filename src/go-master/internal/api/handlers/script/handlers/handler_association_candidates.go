package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	scriptpkg "velox/go-master/internal/api/handlers/script"
)

func (h *ScriptDocsHandler) AssociationCandidates(c *gin.Context) {
	var req scriptpkg.AssociationCandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, scriptpkg.AssociationCandidatesResponse{OK: false, Error: err.Error()})
		return
	}
	req.Normalize()

	resp, err := scriptpkg.BuildAssociationCandidates(c.Request.Context(), req, h.dataDir, h.nodeScraperDir, h.StockDriveRepo, h.ArtlistRepo, h.clipsOnlyRepo)
	if err != nil {
		zap.L().Error("association candidates failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, scriptpkg.AssociationCandidatesResponse{OK: false, Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
