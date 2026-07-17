// Package video contains the application-side Remotion hand-off.
package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainvideo "github.com/Marcuss-ops/PipelineGen/internal/domain/video"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/remotionjob"
	"go.uber.org/zap"
)

const RenderJobType = domainvideo.TypeRender

type Producer struct{ jobs job.Service }

func NewProducer(jobs job.Service) *Producer { return &Producer{jobs: jobs} }

func (p *Producer) Enqueue(ctx context.Context, renderJob remotionjob.RenderJob) (*job.Job, error) {
	if p == nil || p.jobs == nil {
		return nil, fmt.Errorf("video render producer: jobs service is not configured")
	}
	if renderJob.SchemaVersion != remotionjob.SchemaVersion || strings.TrimSpace(renderJob.ID) == "" {
		return nil, fmt.Errorf("video render producer: invalid remotion job")
	}
	return p.jobs.Enqueue(ctx, &job.EnqueueRequest{Type: RenderJobType, Payload: renderJob, CorrelationID: renderJob.ID, ActiveKey: "remotion:" + renderJob.ID, MaxRetries: 1})
}

type Renderer interface {
	Render(context.Context, remotionjob.RenderJob) (RenderResult, error)
}
type RenderResult struct {
	ID         string `json:"id"`
	OutputPath string `json:"outputPath"`
}

type HTTPRenderer struct {
	BaseURL string
	Client  *http.Client
}

func (r *HTTPRenderer) Render(ctx context.Context, renderJob remotionjob.RenderJob) (RenderResult, error) {
	if r == nil || r.Client == nil {
		return RenderResult{}, fmt.Errorf("remotion renderer: http client is not configured")
	}
	base, err := url.Parse(strings.TrimRight(r.BaseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return RenderResult{}, fmt.Errorf("remotion renderer: invalid base URL")
	}
	endpoint := *base
	endpoint.Path = strings.TrimRight(base.Path, "/") + "/render"
	body, err := json.Marshal(renderJob)
	if err != nil {
		return RenderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return RenderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.Client.Do(req)
	if err != nil {
		return RenderResult{}, fmt.Errorf("remotion renderer request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RenderResult{}, fmt.Errorf("remotion renderer returned HTTP %d", resp.StatusCode)
	}
	var result RenderResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return RenderResult{}, fmt.Errorf("remotion renderer response: %w", err)
	}
	return result, nil
}

type Handler struct {
	renderer Renderer
	log      *zap.Logger
}

func NewHandler(renderer Renderer, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{renderer: renderer, log: log}
}

func (h *Handler) Register(svc *appjobs.Service) error {
	if h == nil || h.renderer == nil {
		return fmt.Errorf("video render handler: renderer is required")
	}
	if svc == nil {
		return fmt.Errorf("video render handler: jobs service is required")
	}
	return svc.RegisterHandler(RenderJobType, appjobs.HandlerFunc(h.Handle))
}

func (h *Handler) Handle(ctx context.Context, j *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
	if j == nil {
		return nil, fmt.Errorf("video render handler: job is nil")
	}
	var payload remotionjob.RenderJob
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("video render handler: decode payload: %w", err)
	}
	result, err := h.renderer.Render(ctx, payload)
	if err != nil {
		return nil, err
	}
	h.log.Info("Remotion render completed", zap.String("job_id", j.ID), zap.String("output_path", result.OutputPath))
	return map[string]any{"render_job_id": result.ID, "output_path": result.OutputPath}, nil
}
