// Package lessons contains use-case implementations for the Lesson
// generation flow. Each use case is the canonical bind+validate+invoke
// entry point invoked by the thin HTTP handler in
// internal/api/content/lessons.go.
//
// See internal/application/books for the boundary contract; this
// package mirrors it. asyncEnqueuer and lessonProcessor are
// unexported interfaces declared in this file so the production
// wiring in internal/app/ passes the concrete pointers
// (*jobs.Service, *Service) without an adapter layer — they
// satisfy the interface automatically.
package lessons

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/apiutil"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Package constants — single source of truth for the use-case timeouts
// and enqueue priorities. Naming mirrors the books package so the
// template for the 13 future migrations is uniformly grep-able.
const (
	// generateLessonTimeout caps the synchronous
	// lessons-Service.GenerateLesson execution. 30 minutes covers the
	// worst-case chapter-generation + image-generation + PDF pipeline.
	generateLessonTimeout = 30 * time.Minute

	// generateLessonEnqueuePriority is the priority submitted when the
	// async branch enqueues a lessons-process job. 5 matches the
	// historical convention for "user-visible async jobs" so existing
	// worker scheduling is preserved.
	generateLessonEnqueuePriority = 5

	// generateLessonActiveKeyPrefix is the prefix used to compute the
	// ActiveKey for async enqueues; surfaced as a constant so tests
	// can assert against the same value without string copies.
	generateLessonActiveKeyPrefix = "lessons_generate_"

	// generateLessonActiveKeyMaxLen is the truncation ceiling for the
	// ActiveKey suffix; matches pkg/textutil.Truncate's 50-character
	// historical default so the schema is unchanged post-migration.
	generateLessonActiveKeyMaxLen = 50
)

// asyncEnqueuer is the consumer-side abstraction for the subset of
// the job system that lessons.GenerateLessonUseCase actually touches.
// Production wiring passes the concrete *jobs.Service; tests inject a
// fake that returns canned *jobs.Job or canned error values.
type asyncEnqueuer interface {
	Enqueue(ctx context.Context, req *jobs.EnqueueRequest) (*jobs.Job, error)
}

// lessonProcessor is the consumer-side abstraction for the
// lessons-Service GenerateLesson call surface. Production wiring
// passes the concrete *Service; tests inject a fake.
type lessonProcessor interface {
	GenerateLesson(ctx context.Context, req *LessonRequest) (*LessonResult, error)
}

// GenerateLessonRequest is the JSON body for POST /api/lessons/generate.
// When Async=true, the request is enqueued; otherwise the lesson is
// generated synchronously with a generateLessonTimeout ceiling.
type GenerateLessonRequest struct {
	SourceText     string `json:"source_text"`
	Title          string `json:"title,omitempty"`
	Language       string `json:"language,omitempty"`
	Tone           string `json:"tone,omitempty"`
	MaxChapters    int    `json:"max_chapters,omitempty"`
	GenerateImages bool   `json:"generate_images,omitempty"`
	ImageStyle     string `json:"image_style,omitempty"`
	ImageWidth     int    `json:"image_width,omitempty"`
	ImageHeight    int    `json:"image_height,omitempty"`
	GeneratePDF    bool   `json:"generate_pdf,omitempty"`
	OllamaURL      string `json:"ollama_url,omitempty"`
	Async          bool   `json:"async,omitempty"`
}

// GenerateLessonResult is the synchronous payload carried in the
// shared generation envelope.
type GenerateLessonResult struct {
	Title        string `json:"title"`
	Language     string `json:"language"`
	TotalWords   int    `json:"total_words"`
	MarkdownPath string `json:"markdown_path"`
	PDFPath      string `json:"pdf_path"`
	GeneratedAt  string `json:"generated_at"`
}

// Validate implements the handler-side validation contract.
func (r GenerateLessonRequest) Validate() error {
	if strings.TrimSpace(r.SourceText) == "" {
		return errors.New("source_text is required")
	}
	return nil
}

// activeKey returns the deterministic ActiveKey used to dedupe async
// enqueues for the same title. Uses pkg/textutil.Truncate so long
// titles don't blow past the SQLite indexed-column width limit.
func (r GenerateLessonRequest) activeKey() string {
	return generateLessonActiveKeyPrefix + textutil.Truncate(r.Title, generateLessonActiveKeyMaxLen)
}

// payload projects the request fields onto the JSON payload the
// lesson worker reads at dequeue time. Internal to the package.
func (r GenerateLessonRequest) payload() map[string]any {
	return map[string]any{
		"source_text":     r.SourceText,
		"title":           r.Title,
		"language":        r.Language,
		"tone":            r.Tone,
		"max_chapters":    r.MaxChapters,
		"generate_images": r.GenerateImages,
		"image_style":     r.ImageStyle,
		"image_width":     r.ImageWidth,
		"image_height":    r.ImageHeight,
		"generate_pdf":    r.GeneratePDF,
		"ollama_url":      r.OllamaURL,
	}
}

// GenerateLessonResponse reuses the shared generation envelope so the
// lessons API matches the other text-generation endpoints.
type GenerateLessonResponse = apiutil.Response[GenerateLessonResult]

// Sentinels returned by the use case. The handler's ErrorMapper
// translates each into a stable HTTP status so the wire surface is
// predictable for the 13 future migrations.
var (
	// ErrLessonsServiceUnavailable — sync path was called but the
	// lessons service is nil. Maps to 503.
	ErrLessonsServiceUnavailable = errors.New("lessons service not initialized")

	// ErrJobsSystemUnavailable — async path was called but the job
	// system is nil. Maps to 503.
	ErrJobsSystemUnavailable = errors.New("job system not initialized")

	// ErrEnqueueFailed — jobsSvc.Enqueue returned a non-nil error.
	// Wraps the original error via fmt.Errorf("%w") so errors.Is
	// matches either way. Maps to 503.
	ErrEnqueueFailed = errors.New("enqueue job failed")
)

// ErrGenerateFailed carries the failure message returned by
// Service.GenerateLesson (success=false or runtime error). The mapper
// propagates the message verbatim.
type ErrGenerateFailed struct{ Message string }

func (e ErrGenerateFailed) Error() string { return e.Message }

// GenerateLessonUseCase is the canonical UseCase[I,O] for
// /api/lessons/generate.
type GenerateLessonUseCase struct {
	svc     lessonProcessor
	jobsSvc asyncEnqueuer
	log     *zap.Logger
}

// NewGenerateLessonUseCase constructs the use case.
func NewGenerateLessonUseCase(svc lessonProcessor, jobsSvc asyncEnqueuer, log *zap.Logger) *GenerateLessonUseCase {
	return &GenerateLessonUseCase{svc: svc, jobsSvc: jobsSvc, log: log}
}

// Handle implements the canonical handler-use-case contract.
// Same async/sync split as books.ProcessBookUseCase.
func (uc *GenerateLessonUseCase) Handle(ctx context.Context, req GenerateLessonRequest) (GenerateLessonResponse, error) {
	if req.Async {
		return uc.enqueueLessonJob(ctx, req, string(jobs.TypeLessonsProcess))
	}
	return uc.handleSync(ctx, req)
}

func (uc *GenerateLessonUseCase) handleSync(ctx context.Context, req GenerateLessonRequest) (GenerateLessonResponse, error) {
	if uc.svc == nil {
		return GenerateLessonResponse{}, ErrLessonsServiceUnavailable
	}
	ctxC, cancel := context.WithTimeout(ctx, generateLessonTimeout)
	defer cancel()

	uc.log.Info("generating lesson synchronously",
		zap.String("title", req.Title),
		zap.Int("source_len", len(req.SourceText)),
		zap.Int("max_chapters", req.MaxChapters),
		zap.Bool("generate_images", req.GenerateImages),
		zap.Bool("generate_pdf", req.GeneratePDF),
	)
	result, err := uc.svc.GenerateLesson(ctxC, &LessonRequest{
		SourceText:     req.SourceText,
		Title:          req.Title,
		Language:       req.Language,
		Tone:           req.Tone,
		MaxChapters:    req.MaxChapters,
		GenerateImages: req.GenerateImages,
		ImageStyle:     req.ImageStyle,
		ImageWidth:     req.ImageWidth,
		ImageHeight:    req.ImageHeight,
		GeneratePDF:    req.GeneratePDF,
		OllamaURL:      req.OllamaURL,
	})
	if err != nil {
		return GenerateLessonResponse{}, err
	}
	if !result.Success {
		return GenerateLessonResponse{}, ErrGenerateFailed{Message: result.Error}
	}
	return apiutil.Sync("lesson", GenerateLessonResult{
		Title:        result.Title,
		Language:     result.Language,
		TotalWords:   result.TotalWords,
		MarkdownPath: result.MarkdownPath,
		PDFPath:      result.PDFPath,
		GeneratedAt:  result.GeneratedAt,
	}), nil
}

// enqueueLessonJob is the shared helper for the async branch of any
// lessons-related use case (currently just GenerateLessonUseCase). It
// builds the payload, enqueues the job, and projects the worker's
// *jobs.Job onto the async GenerateLessonResponse ack shape. Wrapping
// the original error in ErrEnqueueFailed lets the ErrorMapper produce
// a clean 503 on failure without leaking the inner driver error.
//
// jobType is passed as a string so callers can audit which job type
// they enqueued without reading internal/job constants in their own
// scope.
func (uc *GenerateLessonUseCase) enqueueLessonJob(ctx context.Context, req GenerateLessonRequest, jobType string) (GenerateLessonResponse, error) {
	if uc.jobsSvc == nil {
		return GenerateLessonResponse{}, ErrJobsSystemUnavailable
	}
	activeKey := req.activeKey()
	uc.log.Info("enqueuing async lesson generate job",
		zap.String("title", req.Title),
		zap.String("job_type", jobType),
	)
	enqueued, err := uc.jobsSvc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:      jobType,
		Payload:   req.payload(),
		Priority:  generateLessonEnqueuePriority,
		ActiveKey: activeKey,
	})
	if err != nil {
		// errors.Join preserves both ErrEnqueueFailed (so the mapper
		// classifies as 503) AND the original error (so callers can
		// errors.Is(err, jobs.ErrNotWired) to distinguish a noted-
		// unwired state from a runtime enqueue failure). Plain
		// fmt.Errorf("%v") would lose the unwrap chain — see the
		// post-review decision log for the 13 future migrations.
		return GenerateLessonResponse{}, errors.Join(
			ErrEnqueueFailed,
			fmt.Errorf("type=%s: %w", jobType, err),
		)
	}
	jobID := ""
	status := ""
	if enqueued != nil {
		jobID = enqueued.ID
		status = string(enqueued.Status)
	}
	return apiutil.Async[GenerateLessonResult]("lesson", jobID, status, jobType), nil
}

// GenerateLessonErrMapper maps use-case errors to HTTP responses.
func GenerateLessonErrMapper(err error) (int, string) {
	if errors.Is(err, ErrLessonsServiceUnavailable) {
		return http.StatusServiceUnavailable, "lessons service is not initialized"
	}
	if errors.Is(err, ErrJobsSystemUnavailable) {
		return http.StatusServiceUnavailable, "job system is not initialized"
	}
	if errors.Is(err, ErrEnqueueFailed) {
		return http.StatusServiceUnavailable, "job enqueue failed"
	}
	var genErr ErrGenerateFailed
	if errors.As(err, &genErr) {
		return http.StatusInternalServerError, genErr.Message
	}
	return 0, ""
}

// useCaseContract is the local Handler[I,O] contract surface.
// Replaces the global transport.UseCase which was removed in June 2026
// (Issue 9b consolidation). Use cases are invoked directly by handlers
// via apiutil.BindJSON + useCase.Handle + apiutil.OK/Error.
type useCaseContract[In any, Out any] interface {
	Handle(ctx context.Context, req In) (Out, error)
}

var _ useCaseContract[GenerateLessonRequest, GenerateLessonResponse] = (*GenerateLessonUseCase)(nil)
