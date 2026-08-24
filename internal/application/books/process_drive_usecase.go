package books

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/apiutil"
)

// processBookFromDriveTimeout caps the synchronous
// books-Service.ProcessBookFromDrive execution. 60 minutes covers the
// worst-case Drive download + rewrite + voiceover pipeline under
// interactive load; the 30m ceiling from ProcessBookUseCase is too
// tight for the Drive path that has network IO at the front.
const processBookFromDriveTimeout = 60 * time.Minute

// driveBookProcessor is the consumer-side abstraction for the
// books-Service ProcessBookFromDrive call surface. Production wiring
// passes the concrete *Service, which already has the
// matching method; tests inject a fake to assert the response shape
// without touching the real Drive stack.
type driveBookProcessor interface {
	ProcessBookFromDrive(ctx context.Context, req *ProcessFromDriveRequest) (*ProcessFromDriveResult, error)
}

// ProcessBookFromDriveRequest is the JSON body for POST /api/books/process-from-drive.
// The endpoint does not support Async=true (no client-visible flag);
// downstream calls happen synchronously because Drive downloads +
// book processing + optional voiceover generation fit in a single
// user-visible request. The processBookFromDriveTimeout ceiling is
// enforced by the use case.
type ProcessBookFromDriveRequest struct {
	DriveFileURL  string `json:"drive_file_url"`
	Instruction   string `json:"instruction,omitempty"`
	Model         string `json:"model,omitempty"`
	PagesPerChunk int    `json:"pages_per_chunk,omitempty"`
	ChunkSize     int    `json:"chunk_size,omitempty"`
	OverlapSize   int    `json:"overlap_size,omitempty"`
	MaxChunks     int    `json:"max_chunks,omitempty"`
	OllamaURL     string `json:"ollama_url,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	OutputPath    string `json:"output_path,omitempty"`
	Language      string `json:"language,omitempty"`
	TranslateOnly bool   `json:"translate_only,omitempty"`
	GeneratePDF   bool   `json:"generate_pdf,omitempty"`
	PDFStyle      string `json:"pdf_style,omitempty"`
}

// ProcessBookFromDriveResult is the synchronous payload carried in the
// shared generation envelope.
type ProcessBookFromDriveResult struct {
	OutputPath      string `json:"output_path"`
	PDFPath         string `json:"pdf_path"`
	DriveFolder     string `json:"drive_folder"`
	DriveDocURL     string `json:"drive_doc_url"`
	DrivePDFURL     string `json:"drive_pdf_url"`
	ChunksProcessed int    `json:"chunks_processed"`
	Language        string `json:"language"`
}

// Validate implements the handler-side validation contract.
func (r ProcessBookFromDriveRequest) Validate() error {
	if strings.TrimSpace(r.DriveFileURL) == "" {
		return errors.New("drive_file_url is required")
	}
	return nil
}

// ProcessBookFromDriveResponse reuses the shared generation envelope
// so the books API matches the other text-generation endpoints.
type ProcessBookFromDriveResponse = apiutil.Response[ProcessBookFromDriveResult]

// ErrDriveMissing is returned when the books Service is nil at
// construction. The ErrorMapper translates to 503. Reuses the same
// sentinel name as the rest of the package so the wire surface is
// internally consistent (the handler and mapper distinguish via
// domain — see ProcessBookErrMapper vs ProcessBookFromDriveErrMapper).
var ErrDriveMissing = errors.New("books service not initialized")

// ProcessBookFromDriveUseCase is the canonical UseCase for
// /api/books/process-from-drive.
type ProcessBookFromDriveUseCase struct {
	svc driveBookProcessor
	log *zap.Logger
}

// NewProcessBookFromDriveUseCase constructs the use case.
func NewProcessBookFromDriveUseCase(svc driveBookProcessor, log *zap.Logger) *ProcessBookFromDriveUseCase {
	return &ProcessBookFromDriveUseCase{svc: svc, log: log}
}

// Handle implements the canonical handler-use-case contract. It runs
// the books pipeline synchronously with a processBookFromDriveTimeout
// ceiling because the worst case is a drive download + rewrite +
// voiceover generation pipeline the user is waiting on interactively.
func (uc *ProcessBookFromDriveUseCase) Handle(ctx context.Context, req ProcessBookFromDriveRequest) (ProcessBookFromDriveResponse, error) {
	if uc.svc == nil {
		return ProcessBookFromDriveResponse{}, ErrDriveMissing
	}
	ctxC, cancel := context.WithTimeout(ctx, processBookFromDriveTimeout)
	defer cancel()

	uc.log.Info("processing book from drive",
		zap.String("drive_file_url", req.DriveFileURL),
	)
	result, err := uc.svc.ProcessBookFromDrive(ctxC, &ProcessFromDriveRequest{
		DriveFileURL:  req.DriveFileURL,
		Instruction:   req.Instruction,
		Model:         req.Model,
		PagesPerChunk: req.PagesPerChunk,
		ChunkSize:     req.ChunkSize,
		OverlapSize:   req.OverlapSize,
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
		return ProcessBookFromDriveResponse{}, err
	}
	if !result.Success {
		return ProcessBookFromDriveResponse{}, ErrProcessFailed{Message: result.Error}
	}
	resp := ProcessBookFromDriveResult{}
	if result.BookResult != nil {
		resp.OutputPath = result.BookResult.OutputPath
		resp.PDFPath = result.BookResult.PDFPath
		resp.DriveFolder = result.BookResult.DriveFolderURL
		resp.DriveDocURL = result.BookResult.DriveDocURL
		resp.DrivePDFURL = result.BookResult.DrivePDFURL
		resp.ChunksProcessed = result.BookResult.ChunksProcessed
		resp.Language = result.BookResult.Language
	}
	return apiutil.Sync("book", resp), nil
}

// ProcessBookFromDriveErrMapper maps use-case errors to HTTP responses.
// Reuses the package-level ErrProcessFailed sentinel so the wire
// surface is consistent with the /process endpoint. This endpoint has
// no async path, so ErrEnqueueFailed / ErrJobsSystemUnavailable do
// not appear here.
func ProcessBookFromDriveErrMapper(err error) (int, string) {
	if errors.Is(err, ErrDriveMissing) {
		return http.StatusServiceUnavailable, "books service is not initialized"
	}
	var procErr ErrProcessFailed
	if errors.As(err, &procErr) {
		return http.StatusInternalServerError, procErr.Message
	}
	return 0, ""
}

var _ useCaseContract[ProcessBookFromDriveRequest, ProcessBookFromDriveResponse] = (*ProcessBookFromDriveUseCase)(nil)
