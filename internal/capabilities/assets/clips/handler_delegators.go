// Package clips contains compatibility delegators from the aggregate Handler
// to the focused HTTP handlers. Business dependencies remain on those focused
// handlers, never on the aggregate.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

func (h *Handler) CreateClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.CreateClip(c)
}

func (h *Handler) CreateAIStockClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.CreateAIStockClip(c)
}

func (h *Handler) VerifyClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.VerifyClip(c)
}

func (h *Handler) Reconcile(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Reconcile(c)
}

func (h *Handler) Cleanup(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Cleanup(c)
}
