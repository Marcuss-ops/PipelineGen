package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	jobyoutube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type StockJobHandler struct {
	service *stockplan.StockService
	log     *zap.Logger
}

func NewStockJobHandler(service *stockplan.StockService, log *zap.Logger) *StockJobHandler {
	return &StockJobHandler{service: service, log: log}
}

func (h *StockJobHandler) HandleJob(ctx context.Context, j *kerneljob.Job, tools *jobtools.JobTools) (map[string]any, error) {
	if h == nil || h.service == nil {
		return nil, fmt.Errorf("youtube stock: handler is not wired")
	}
	var req stockplan.YouTubeStockRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("youtube stock: decode request: %w", err)
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(5, "metadata and transcript acquisition")
	}
	result, err := h.service.Run(ctx, req)
	if err != nil {
		return nil, err
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "YouTube stock clips persisted")
	}
	return map[string]any{
		"ok": true, "job_type": jobyoutube.TypeStock,
		"videos_analyzed":   result.VideosAnalyzed,
		"selected_segments": result.SelectedSegments,
	}, nil
}
