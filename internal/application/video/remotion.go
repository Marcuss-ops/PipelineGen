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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
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
	if err := remotionjob.ValidateShortFormComposition(renderJob.Composition); err != nil {
		return nil, fmt.Errorf("video render producer: %w", err)
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
	if err := remotionjob.ValidateShortFormComposition(renderJob.Composition); err != nil {
		return RenderResult{}, fmt.Errorf("remotion renderer: %w", err)
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
	renderer  Renderer
	publisher delivery.Publisher
	log       *zap.Logger
}

func NewHandler(renderer Renderer, log *zap.Logger) *Handler {
	return NewHandlerWithPublisher(renderer, nil, log)
}

func NewHandlerWithPublisher(renderer Renderer, publisher delivery.Publisher, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{renderer: renderer, publisher: publisher, log: log}
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
	if err := remotionjob.ValidateShortFormComposition(payload.Composition); err != nil {
		return nil, fmt.Errorf("video render handler: %w", err)
	}
	result, err := h.renderer.Render(ctx, payload)
	if err != nil {
		return nil, err
	}
	h.log.Info("Remotion render completed", zap.String("job_id", j.ID), zap.String("output_path", result.OutputPath))
	out := map[string]any{"render_job_id": result.ID, "output_path": result.OutputPath}
	if payload.UploadToDrive {
		if h.publisher == nil {
			return nil, fmt.Errorf("video render handler: Drive publisher is not configured")
		}
		receipt, err := h.publisher.Publish(ctx, delivery.PublishRequest{
			Destination:         delivery.DestinationClipMetadata,
			LocalPath:           result.OutputPath,
			Filename:            payload.DriveFilename,
			Description:         "PipelineGen Shorts render " + payload.ID,
			AssetID:             payload.ID,
			ProjectID:           "shorts",
			Language:            payload.Language,
			DestinationFolderID: payload.DriveFolderID,
			ConflictPolicy:      delivery.ConflictOverwrite,
		})
		if err != nil {
			return nil, fmt.Errorf("video render handler: upload to Drive: %w", err)
		}
		out["drive_file_id"] = receipt.FileID
		out["drive_url"] = receipt.WebViewLink
		out["drive_folder_id"] = receipt.FolderID
	}
	return out, nil
}
