package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/pkg/apiutil"
)

func (h *ScriptDocsHandler) AssociationCandidates(c *gin.Context) {
	if h.assocService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "association service not initialized")
		return
	}

	req, ok := apiutil.BindJSON[association.CandidatesRequest](c)
	if !ok {
		return
	}
	req.Normalize()

	resp, err := h.assocService.BuildCandidates(c.Request.Context(), req)
	if err != nil {
		zap.L().Error("association candidates failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}
