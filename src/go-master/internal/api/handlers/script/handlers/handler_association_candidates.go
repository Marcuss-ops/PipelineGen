package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/service/association"
)

func (h *ScriptDocsHandler) AssociationCandidates(c *gin.Context) {
	var req association.CandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, association.CandidatesResponse{OK: false, Error: err.Error()})
		return
	}
	req.Normalize()

	resp, err := h.assocService.BuildCandidates(c.Request.Context(), req)
	if err != nil {
		zap.L().Error("association candidates failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, association.CandidatesResponse{OK: false, Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
