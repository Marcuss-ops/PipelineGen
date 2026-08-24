package youtube

import (
	"encoding/json"
	"fmt"

	transport "github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// SubmitStock accepts the canonical YouTubeStockRequest and only enqueues it;
// metadata, transcript, selection and media work stay in the worker.
func (h *YouTubeClipHandler) SubmitStock(c *gin.Context) {
	if h.stockService == nil {
		apiutil.InternalError(c, fmt.Errorf("youtube stock capability is not wired"))
		return
	}
	req, ok := apiutil.BindJSON[stockplan.YouTubeStockRequest](c)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	raw, err := json.Marshal(req)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("encode YouTube stock request: %w", err))
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		apiutil.InternalError(c, fmt.Errorf("prepare YouTube stock payload: %w", err))
		return
	}
	transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type: appjobs.TypeYouTubeStock, Payload: payload,
	}, "YouTube stock job enqueued.")
}
