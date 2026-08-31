// Package books contains use-case implementations for the Book
// generation flow. Each use case is the canonical bind+validate+invoke
// entry point invoked by the thin HTTP handler in
// internal/api/content/books.go.
//
// Boundaries (enforced by AGENTS.md):
//   - This package NEVER imports gin, database/sql, os/exec, or
//     google.golang.org/api/drive/v3.
//   - All side effects call into the books.Service (internal/capabilities/books) or
//     internal/kernel/job.Service through unexported interfaces
//     declared in this file (see asyncEnqueuer, bookProcessor,
//     driveBookProcessor); the production wiring in internal/app/
//     passes the concrete pointers, which satisfy these interfaces
//     automatically without an adapter layer.
package books

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/apiutil"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Package constants — single source of truth for the use-case timeouts
// and enqueue priorities. Future migrations of similar async+sync
// endpoints should follow the same naming shape (processXTimeout /
// processXxxxEnqueuePriority) so the contract is discoverable.
const (
	// processBookTimeout caps the synchronous books-Service.ProcessBook
	// execution. 30 minutes covers the worst-case rewrite + voiceover
	// pipeline under interactive load.
	processBookTimeout = 30 * time.Minute

	// processBookEnqueuePriority is the priority submitted when the
	// async branch enqueues a books-process job. 5 matches the bulk
	// upload / clip-enrich historical value so existing job queues
	// process new entries in the same order as pre-migration.
	processBookEnqueuePriority = 5

	// processBookActiveKeyPrefix is the prefix used to compute the
	// ActiveKey for async enqueues; surfaced as a constant so tests
	// can assert against the same value without string copies.
	processBookActiveKeyPrefix = "books_process_"
)

// asyncEnqueuer is the consumer-side abstraction for the subset of
// the job system that the use cases actually touch. Tests inject a
// fake that returns a canned *jobs.Job or canned error; the production
// wiring passes the concrete *jobs.Service, which satisfies this
// interface automatically.
type asyncEnqueuer interface {
	Enqueue(ctx context.Context, req *jobs.EnqueueRequest) (*jobs.Job, error)
}

// bookProcessor is the consumer-side abstraction for the books-Service
// ProcessBook call surface. Same rule: production wiring passes the
// concrete *Service, tests inject a fake.
type bookProcessor interface {
	ProcessBook(ctx context.Context, req *ProcessRequest) (*ProcessResult, error)
}

// ProcessBookRequest is the JSON body for POST /api/books/process and
// POST /api/books/generate. When Async=true, the request is enqueued
// into the job system (where it will be executed by the books worker);
// when Async=false, the request runs synchronously with a
// processBookTimeout ceiling enforced by the use case. Either FilePath
// or GoogleDocURL must be set; the Validate method enforces this
// contract.
type ProcessBookRequest struct {
	FilePath      string `json:"file_path"`
	GoogleDocURL  string `json:"google_doc_url"`
	Instruction   string `json:"instruction,omitempty"`
	Model         string `json:"model,omitempty"`
	PagesPerChunk int    `json:"pages_per_chunk,omitempty"`
	ChunkSize     int    `json:"chunk_size,omitempty"`
	OverlapSize   int    `json:"overlap_size,omitempty"`
	MaxChunks     int    `json:"max_chunks,omitempty"`
	OllamaURL     string `json:"ollama_url,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	OutputPath    string `json:"output_path,omitempty"`
	Async         bool   `json:"async,omitempty"`
	Language      string `json:"language,omitempty"`
	TranslateOnly bool   `json:"translate_only,omitempty"`
	GeneratePDF   bool   `json:"generate_pdf,omitempty"`
	PDFStyle      string `json:"pdf_style,omitempty"`
}

// ProcessBookResult is the synchronous payload carried in the shared
// generation envelope.
type ProcessBookResult struct {
	OutputPath      string `json:"output_path"`
	PDFPath         string `json:"pdf_path"`
	DriveFolder     string `json:"drive_folder"`
	DriveDocURL     string `json:"drive_doc_url"`
	DrivePDFURL     string `json:"drive_pdf_url"`
	WordCount       int    `json:"word_count"`
	ChunksProcessed int    `json:"chunks_processed"`
	Language        string `json:"language"`
}

// Validate implements the handler-side validation contract. The HTTP
// handler calls this after binding and BEFORE invoking the use case;
// on error the handler returns 400 via apiutil.BadRequest.
func (r ProcessBookRequest) Validate() error {
	if strings.TrimSpace(r.FilePath) == "" && strings.TrimSpace(r.GoogleDocURL) == "" {
		return errors.New("file_path or google_doc_url is required")
	}
	return nil
}

// activeKey returns the deterministic ActiveKey used to dedupe async
// enqueues for the same source. Exposed only as an internal helper so
// tests can reuse the exact contract.
func (r ProcessBookRequest) activeKey() string {
	return processBookActiveKeyPrefix + r.FilePath + "|" + r.GoogleDocURL
}

// payload projects the request fields onto the JSON payload the jobs
// worker reads at dequeue time. Internal to the package; tests
// piggyback on it through the happy-path branch.
func (r ProcessBookRequest) payload() map[string]any {
	return map[string]any{
		"file_path":       r.FilePath,
		"google_doc_url":  r.GoogleDocURL,
		"instruction":     r.Instruction,
		"model":           r.Model,
		"pages_per_chunk": r.PagesPerChunk,
		"chunk_size":      r.ChunkSize,
		"max_chunks":      r.MaxChunks,
		"ollama_url":      r.OllamaURL,
		"drive_folder_id": r.DriveFolderID,
		"output_path":     r.OutputPath,
		"language":        r.Language,
		"translate_only":  r.TranslateOnly,
		"generate_pdf":    r.GeneratePDF,
		"pdf_style":       r.PDFStyle,
	}
}

// ProcessBookResponse reuses the shared generation envelope so books
// matches the other text-generation endpoints.
type ProcessBookResponse = apiutil.Response[ProcessBookResult]

// Sentinels returned by the use case. The handler's ErrorMapper
// translates each into a stable HTTP status so the wire surface is
// predictable for the 13 future migrations that will follow the
// template established here.
var (
	// ErrBooksServiceUnavailable — sync path was called but the
	// books service is nil (construction failed or wiring is
	// incomplete). Maps to 503.
	ErrBooksServiceUnavailable = errors.New("books service not initialized")

	// ErrJobsSystemUnavailable — async path was called but the job
	// system is nil. Maps to 503. Distinct from ErrEnqueueFailed so
	// the caller can tell "not wired at compile time" from
	// "runtime enqueue call failed".
	ErrJobsSystemUnavailable = errors.New("job system not initialized")

	// ErrEnqueueFailed — jobsSvc.Enqueue returned a non-nil error.
	// Wraps the original error via fmt.Errorf("%w") so errors.Is
	// matches either way. Maps to 503 (server temporarily unable to
	// accept async work).
	ErrEnqueueFailed = errors.New("enqueue job failed")
)

// ErrProcessFailed carries the message a books worker returned when
// the underlying script exited unsuccessfully (the Service returns a
// ProcessResult with Success=false and a non-empty Error). The mapper
// propagates the message verbatim so ops can still grep logs.
type ErrProcessFailed struct{ Message string }

// Error implements errors.Error so ProcessBookErrMapper can match via
// errors.As and surface the original worker message.
func (e ErrProcessFailed) Error() string { return e.Message }

// ProcessBookUseCase is the canonical UseCase[I,O] for the
// /api/books/{process,generate} endpoint.
type ProcessBookUseCase struct {
	svc     bookProcessor
	jobsSvc asyncEnqueuer
	log     *zap.Logger
}

// NewProcessBookUseCase constructs the use case. Either svc or
// jobsSvc may be nil — the use case returns the typed sentinel error
// ErrBooksServiceUnavailable / ErrJobsSystemUnavailable and the mapper
// translates it to 503.
func NewProcessBookUseCase(svc bookProcessor, jobsSvc asyncEnqueuer, log *zap.Logger) *ProcessBookUseCase {
	return &ProcessBookUseCase{svc: svc, jobsSvc: jobsSvc, log: log}
}

// Handle implements the canonical handler-use-case contract.
//
// Branch (1): req.Async == true. Defers to enqueueBookJob.
// Branch (2): req.Async == false. Runs svc.ProcessBook synchronously
// with a processBookTimeout ceiling; returns a response populated
// only with the sync result fields.
//
// Ctx propagation: the parent's ctx (typically the request ctx from
// gin) is propagated as-is to the async branch so a client disconnect
// cancels the enqueue (caller-side). In the sync branch the use case
// attaches its own WithTimeout so handler code does not need to know
// request-shape-specific timeouts.
func (uc *ProcessBookUseCase) Handle(ctx context.Context, req ProcessBookRequest) (ProcessBookResponse, error) {
	if req.Async {
		return uc.enqueueBookJob(ctx, req, string(jobs.TypeBooksProcess))
	}
	return uc.handleSync(ctx, req)
}

func (uc *ProcessBookUseCase) handleSync(ctx context.Context, req ProcessBookRequest) (ProcessBookResponse, error) {
	if uc.svc == nil {
		return ProcessBookResponse{}, ErrBooksServiceUnavailable
	}
	ctxC, cancel := context.WithTimeout(ctx, processBookTimeout)
	defer cancel()

	uc.log.Info("processing book synchronously",
		zap.String("file", req.FilePath),
		zap.String("google_doc_url", req.GoogleDocURL),
	)
	result, err := uc.svc.ProcessBook(ctxC, &ProcessRequest{
		FilePath:      req.FilePath,
		GoogleDocURL:  req.GoogleDocURL,
		Instruction:   req.Instruction,
		Model:         req.Model,
		PagesPerChunk: req.PagesPerChunk,
		ChunkSize:     req.ChunkSize,
		MaxChunks:     req.MaxChunks,
		OllamaURL:     req.OllamaURL,
		DriveFolderID: req.DriveFolderID,
		OutputPath:    req.OutputPath,
		Language:      req.Language,
		TranslateOnly: req.TranslateOnly,
		GeneratePDF:   req.GeneratePDF,
		PDFStyle:      req.PDFStyle,
	})
	if err != nil {
		return ProcessBookResponse{}, err
	}
	if !result.Success {
		return ProcessBookResponse{}, ErrProcessFailed{Message: result.Error}
	}
	return apiutil.Sync("book", ProcessBookResult{
		OutputPath:      result.OutputPath,
		PDFPath:         result.PDFPath,
		DriveFolder:     result.DriveFolderURL,
		DriveDocURL:     result.DriveDocURL,
		DrivePDFURL:     result.DrivePDFURL,
		WordCount:       result.WordCount,
		ChunksProcessed: result.ChunksProcessed,
		Language:        result.Language,
	}), nil
}

// enqueueBookJob is the shared helper for the async branch of any
// books-related use case (currently just ProcessBookUseCase; the
// drive-from variant is sync-only). It builds the payload, enqueues
// the job, and projects the worker's *jobs.Job onto the async
// ProcessBookResponse ack shape. Wrapping the original error in
// ErrEnqueueFailed lets the ErrorMapper produce a clean 503 on
// failure without leaking the inner driver error verbatim.
//
// jobType is passed as a string so callers can audit which job type
// they enqueued without reading internal/job constants in their own
// scope; future migrations should pass the same string constant
// (e.g. string(jobs.TypeBooksProcess)) directly from Handle.
func (uc *ProcessBookUseCase) enqueueBookJob(ctx context.Context, req ProcessBookRequest, jobType string) (ProcessBookResponse, error) {
	if uc.jobsSvc == nil {
		return ProcessBookResponse{}, ErrJobsSystemUnavailable
	}
	activeKey := req.activeKey()
	uc.log.Info("enqueuing async book process job",
		zap.String("file", req.FilePath),
		zap.String("job_type", jobType),
	)
	enqueued, err := uc.jobsSvc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:      jobType,
		Payload:   req.payload(),
		Priority:  processBookEnqueuePriority,
		ActiveKey: activeKey,
	})
	if err != nil {
		// errors.Join preserves both ErrEnqueueFailed (so the mapper
		// classifies as 503) AND the original error (so callers can
		// errors.Is(err, jobs.ErrNotWired) to distinguish a noted-
		// unwired state from a runtime enqueue failure). Plain
		// fmt.Errorf("%v") would lose the unwrap chain — see the
		// post-review decision log for the 13 future migrations.
		return ProcessBookResponse{}, errors.Join(
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
	return apiutil.Async[ProcessBookResult]("book", jobID, status, jobType), nil
}

// ProcessBookErrMapper maps use-case errors to HTTP status codes. It
// is the canonical ErrorMapper signature for the books-process handler.
// Sensible default: 503 for any service-layer unavailability or
// enqueue failure; 500 for ErrProcessFailed (worker-level failure
// with the original message exposed verbatim); pass-through (0, "")
// for unknown errors so the handler's safe 500 fallback kicks in.
func ProcessBookErrMapper(err error) (int, string) {
	if errors.Is(err, ErrBooksServiceUnavailable) {
		return http.StatusServiceUnavailable, "books service is not initialized"
	}
	if errors.Is(err, ErrJobsSystemUnavailable) {
		return http.StatusServiceUnavailable, "job system is not initialized"
	}
	if errors.Is(err, ErrEnqueueFailed) {
		return http.StatusServiceUnavailable, "job enqueue failed"
	}
	if errors.Is(err, ErrBookTransformerMissing) {
		return http.StatusServiceUnavailable, "books transformer port not wired"
	}
	var procErr ErrProcessFailed
	if errors.As(err, &procErr) {
		return http.StatusInternalServerError, procErr.Message
	}
	// Unknown errors fall through to the handler's safe default
	// (500 with err.Error() mapped via the package-level ErrorMapper,
	// preserving the leak-safe contract).
	return 0, ""
}

// Compile-time guarantee that ProcessBookUseCase satisfies the
// canonical Handler[I,O] contract. Other use cases in this package
// should add an equivalent assertion so a future refactor that
// breaks the signature is caught at build time, not at runtime.
//
// Note: the transport.JSON pipeline was removed in June 2026
// (Issue 9b consolidation). Use cases are invoked directly by
// handlers via apiutil.BindJSON + useCase.Handle + apiutil.OK/Error;
// the contract surface (Handle(ctx, In) (Out, error)) remains.
type useCaseContract[In any, Out any] interface {
	Handle(ctx context.Context, req In) (Out, error)
}

var _ useCaseContract[ProcessBookRequest, ProcessBookResponse] = (*ProcessBookUseCase)(nil)
