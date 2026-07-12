// Package script — handler_generate_handler.go owns the narrow HTTP
// orchestration shell for POST /api/script/generate. Request construction and
// response mapping live in focused companion files in the same package.
package script

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
)

// HandlerGenerate is the narrow HTTP handler for script generation.
type HandlerGenerate struct {
	submitter generationSubmitter
	log       *zap.Logger
	caps      PreflightCaps
	validator *usecase.PayloadValidator
}

// NewHandlerGenerate constructs the handler from the canonical dependencies.
func NewHandlerGenerate(
	submitter generationSubmitter,
	log *zap.Logger,
	caps PreflightCaps,
	validator *usecase.PayloadValidator,
) *HandlerGenerate {
	if log == nil {
		log = zap.NewNop()
	}
	if validator == nil {
		validator = usecase.NewDefaultPayloadValidator()
	}
	return &HandlerGenerate{
		submitter: submitter,
		log:       log,
		caps:      caps,
		validator: validator,
	}
}

// GenerateRoute registers POST /generate.
func (h *HandlerGenerate) GenerateRoute(r *gin.RouterGroup) {
	if h == nil {
		return
	}
	r.POST("/generate", h.Generate)
}

// Generate validates the request, submits it through the canonical operations
// service, and writes the async response.
func (h *HandlerGenerate) Generate(c *gin.Context) {
	env, ok := h.bindGenerateEnvelope(c)
	if !ok {
		return
	}
	if h.submitter == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "operations service not initialized",
		})
		return
	}

	req, ok := buildGenerateSubmitRequest(c, &env)
	if !ok {
		return
	}

	submitCtx, cancel := context.WithTimeout(c.Request.Context(), enqueueTimeout)
	defer cancel()

	res, err := h.submitter.Submit(submitCtx, req)
	if err != nil {
		writeGenerateSubmitError(c, err)
		return
	}
	writeGenerateSubmitSuccess(c, res)
}
