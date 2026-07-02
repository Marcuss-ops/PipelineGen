package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	assetdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

var (
	ErrUnsupportedType = errors.New("unsupported generation type")
	ErrTypeDisabled    = errors.New("generation type disabled")
	ErrJobNotFound     = errors.New("generation job not found")
)

// JobService captures the enqueue/status contract used by the unified API.
type JobService interface {
	Enqueue(ctx context.Context, req *jobdomain.EnqueueRequest) (*jobdomain.Job, error)
	Get(ctx context.Context, id string) (*jobdomain.Job, error)
	Cancel(ctx context.Context, id string) error
	ListEvents(ctx context.Context, jobID string) ([]jobdomain.Event, error)
}

// AssetStore resolves persisted source assets into local or drive-backed inputs.
type AssetStore interface {
	Get(ctx context.Context, id string) (*assetdomain.Details, error)
}

// Service creates and inspects generation jobs via the canonical job system.
type Service struct {
	jobs   JobService
	assets AssetStore
	reg    *Registry
}

// NewService constructs the unified generation service.
func NewService(jobs JobService, assets AssetStore, reg *Registry) *Service {
	return &Service{jobs: jobs, assets: assets, reg: reg}
}

// CreateResponse is returned by POST /api/generations.
type CreateResponse struct {
	OK  bool `json:"ok"`
	Job struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Status    string `json:"status"`
		StatusURL string `json:"status_url"`
	} `json:"job"`
}

// StatusResponse is returned by GET /api/generations/:id.
type StatusResponse struct {
	OK  bool      `json:"ok"`
	Job JobStatus `json:"job"`
}

// JobStatus is the canonical public job projection.
type JobStatus struct {
	ID          string                  `json:"id"`
	Type        string                  `json:"type"`
	Status      string                  `json:"status"`
	Progress    int                     `json:"progress"`
	Phase       string                  `json:"phase,omitempty"`
	Message     string                  `json:"message,omitempty"`
	CreatedAt   string                  `json:"created_at"`
	StartedAt   string                  `json:"started_at,omitempty"`
	CompletedAt string                  `json:"completed_at,omitempty"`
	Result      json.RawMessage         `json:"result,omitempty"`
	Error       *domaingeneration.Error `json:"error,omitempty"`
}

// Create enqueues a generation request.
func (s *Service) Create(ctx context.Context, req domaingeneration.Request) (*CreateResponse, error) {
	if s == nil || s.jobs == nil {
		return nil, fmt.Errorf("generation service not initialized")
	}
	if s.reg == nil {
		return nil, fmt.Errorf("generation registry not initialized")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	def, ok := s.reg.Resolve(req.Type)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedType, req.Type)
	}
	if !def.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrTypeDisabled, req.Type)
	}
	if err := def.Validate(req.Input); err != nil {
		return nil, err
	}
	payload, err := def.BuildJob(ctx, req.Input, s)
	if err != nil {
		return nil, err
	}
	j, err := s.jobs.Enqueue(ctx, &jobdomain.EnqueueRequest{
		Type:          def.JobType,
		Payload:       payload,
		MaxRetries:    2,
		Priority:      5,
		CorrelationID: corid.FromContext(ctx),
	})
	if err != nil {
		return nil, err
	}
	resp := &CreateResponse{OK: true}
	resp.Job.ID = j.ID
	resp.Job.Type = j.Type
	resp.Job.Status = string(j.Status)
	resp.Job.StatusURL = "/api/generations/" + j.ID
	return resp, nil
}

// Status returns the current job state in the public generation shape.
func (s *Service) Status(ctx context.Context, id string) (*StatusResponse, error) {
	if s == nil || s.jobs == nil {
		return nil, fmt.Errorf("generation service not initialized")
	}
	j, err := s.jobs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrJobNotFound
	}
	out := &StatusResponse{OK: true}
	out.Job = jobStatusFromJob(*j)
	if events, err := s.jobs.ListEvents(ctx, id); err == nil && len(events) > 0 {
		last := events[len(events)-1]
		if out.Job.Phase == "" {
			out.Job.Phase = strings.TrimSpace(last.Type)
		}
		if out.Job.Message == "" {
			out.Job.Message = strings.TrimSpace(last.Message)
		}
	}
	return out, nil
}

// Cancel cancels a generation job.
func (s *Service) Cancel(ctx context.Context, id string) error {
	if s == nil || s.jobs == nil {
		return fmt.Errorf("generation service not initialized")
	}
	return s.jobs.Cancel(ctx, id)
}

func jobStatusFromJob(j jobdomain.Job) JobStatus {
	out := JobStatus{
		ID:        j.ID,
		Type:      j.Type,
		Status:    string(j.Status),
		Progress:  j.Progress,
		CreatedAt: j.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if j.StartedAt != nil {
		out.StartedAt = j.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if j.CompletedAt != nil {
		out.CompletedAt = j.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if len(j.Result) > 0 {
		out.Result = json.RawMessage(append([]byte(nil), j.Result...))
	}
	if strings.TrimSpace(j.Error) != "" {
		out.Error = &domaingeneration.Error{
			Code:      "JOB_FAILED",
			Message:   j.Error,
			Retryable: false,
		}
	}
	return out
}

// BuildDefaultRegistry returns the canonical set of supported generation types.
func BuildDefaultRegistry(bookEnabled, lessonEnabled, scriptEnabled bool) *Registry {
	reg := NewRegistry()

	_ = reg.Register(Definition{
		Type:    domaingeneration.TypeScriptGenerate,
		JobType: string(jobdomain.TypeScriptGenerate),
		Enabled: scriptEnabled,
		Validate: func(raw json.RawMessage) error {
			var input ScriptSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return err
			}
			if len(input.ClipIDs) == 0 && strings.TrimSpace(input.Topic) == "" && strings.TrimSpace(input.SourceText) == "" {
				return fmt.Errorf("clip_ids, topic or source_text is required")
			}
			return nil
		},
		BuildJob: func(ctx context.Context, raw json.RawMessage, _ *Service) (any, error) {
			var input ScriptSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			return input, nil
		},
	})

	_ = reg.Register(Definition{
		Type:    domaingeneration.TypeScriptFromClips,
		JobType: string(jobdomain.TypeScriptGenerate),
		Enabled: scriptEnabled,
		Validate: func(raw json.RawMessage) error {
			var input ScriptSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return err
			}
			if len(input.ClipIDs) == 0 && strings.TrimSpace(input.Topic) == "" && strings.TrimSpace(input.SourceText) == "" {
				return fmt.Errorf("clip_ids, topic or source_text is required")
			}
			return nil
		},
		BuildJob: func(ctx context.Context, raw json.RawMessage, _ *Service) (any, error) {
			var input ScriptSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			return input, nil
		},
	})

	_ = reg.Register(Definition{
		Type:    domaingeneration.TypeScriptWithImages,
		JobType: string(jobdomain.TypeScriptGenerate),
		Enabled: scriptEnabled,
		Validate: func(raw json.RawMessage) error {
			var input ScriptSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return err
			}
			if len(input.ClipIDs) == 0 && strings.TrimSpace(input.Topic) == "" && strings.TrimSpace(input.SourceText) == "" {
				return fmt.Errorf("clip_ids, topic or source_text is required")
			}
			return nil
		},
		BuildJob: func(ctx context.Context, raw json.RawMessage, _ *Service) (any, error) {
			var input ScriptSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			input.GenerateSceneImages = true
			return input, nil
		},
	})

	_ = reg.Register(Definition{
		Type:    domaingeneration.TypeScriptBatch,
		JobType: string(jobdomain.TypeScriptGenerate),
		Enabled: scriptEnabled,
		Validate: func(raw json.RawMessage) error {
			var input BatchSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return err
			}
			if len(input.Items) == 0 && len(input.BatchTopics) == 0 {
				return fmt.Errorf("items or batch_topics is required")
			}
			return nil
		},
		BuildJob: func(ctx context.Context, raw json.RawMessage, _ *Service) (any, error) {
			var input BatchSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			input.Async = true
			return input, nil
		},
	})

	_ = reg.Register(Definition{
		Type:    domaingeneration.TypeLessonGenerate,
		JobType: string(jobdomain.TypeLessonsProcess),
		Enabled: lessonEnabled,
		Validate: func(raw json.RawMessage) error {
			var input LessonSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return err
			}
			if strings.TrimSpace(input.SourceText) == "" && strings.TrimSpace(input.Topic) == "" {
				return fmt.Errorf("topic or source_text is required")
			}
			return nil
		},
		BuildJob: func(ctx context.Context, raw json.RawMessage, _ *Service) (any, error) {
			var input LessonSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			sourceText := strings.TrimSpace(input.SourceText)
			if sourceText == "" {
				sourceText = strings.TrimSpace(input.Topic)
			}
			req := LessonSource{
				SourceText:     sourceText,
				Title:          input.Title,
				Language:       input.Language,
				Tone:           input.Tone,
				Model:          input.Model,
				MaxChapters:    input.MaxChapters,
				GenerateImages: input.GenerateImages,
				ImageStyle:     input.ImageStyle,
				ImageWidth:     input.ImageWidth,
				ImageHeight:    input.ImageHeight,
				GeneratePDF:    input.GeneratePDF,
				OllamaURL:      input.OllamaURL,
				Async:          true,
			}
			return req, nil
		},
	})

	_ = reg.Register(Definition{
		Type:    domaingeneration.TypeBookGenerate,
		JobType: string(jobdomain.TypeBooksProcess),
		Enabled: bookEnabled,
		Validate: func(raw json.RawMessage) error {
			var input BookSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return err
			}
			if strings.TrimSpace(input.SourceAssetID) == "" && strings.TrimSpace(input.GoogleDocURL) == "" {
				return fmt.Errorf("source_asset_id or google_doc_url is required")
			}
			return nil
		},
		BuildJob: func(ctx context.Context, raw json.RawMessage, svc *Service) (any, error) {
			var input BookSource
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			req := BookSource{
				FilePath:      input.FilePath,
				GoogleDocURL:  input.GoogleDocURL,
				Instruction:   input.Instruction,
				Model:         input.Model,
				PagesPerChunk: input.PagesPerChunk,
				ChunkSize:     input.ChunkSize,
				OverlapSize:   input.OverlapSize,
				MaxChunks:     input.MaxChunks,
				OllamaURL:     input.OllamaURL,
				DriveFolderID: input.DriveFolderID,
				OutputPath:    input.OutputPath,
				Language:      input.Language,
				TranslateOnly: input.TranslateOnly,
				GeneratePDF:   input.GeneratePDF,
				PDFStyle:      input.PDFStyle,
			}
			if strings.TrimSpace(input.SourceAssetID) != "" {
				if svc == nil || svc.assets == nil {
					return nil, fmt.Errorf("asset store is not configured")
				}
				details, err := svc.assets.Get(ctx, strings.TrimSpace(input.SourceAssetID))
				if err != nil {
					return nil, err
				}
				fp, gd := resolveBookSource(details)
				if req.FilePath == "" {
					req.FilePath = fp
				}
				if req.GoogleDocURL == "" {
					req.GoogleDocURL = gd
				}
			}
			if strings.TrimSpace(req.FilePath) == "" && strings.TrimSpace(req.GoogleDocURL) == "" {
				return nil, fmt.Errorf("book source could not be resolved to a local file or Google Doc URL")
			}
			return req, nil
		},
	})

	return reg
}
