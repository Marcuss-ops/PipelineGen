// Package books — books.Service is the apply-layer orchestrator
// for the book-summarisation pipeline (Fase 7 Spina Dorsale,
// July 2026). All Python-execution responsibility moved OUT of
// this file into internal/infrastructure/books/pythontransformer/.
// The Service now routes the summarisation work through the
// canonical BookTransformer port (godlike/06 "one owner per
// fact" — apply layer has zero Python subprocess dependency).
//
// What lives here now:
//
//   - Service struct: holds the narrow PublisherPort + drive.Reader
//   - voiceover.VoiceoverGenerator + books.BookTransformer ports.
//   - ProcessRequest / ProcessResult: stable JSON types for the
//     /api/books/{process,generate}/{process-from-drive} wire
//     surface.
//   - ProcessBook / ProcessBookWithProgress: maps the public
//     ProcessRequest shape to the internal TransformRequest
//     shape and delegates to the transformer port.
//   - ProcessBookFromDrive (drive.go): live in drive.go for
//     historical reasons; downloads via drive.Reader, then calls
//     back into ProcessBook.
//
// What used to live here (pre-Fase-7) and was MOVED:
//
//   - exec.CommandContext(s.cfg.PythonBin, args...)         →
//     pythontransformer.SubprocessTransformer.Transform
//   - buildArgs / parseOutput / parseProgressLine         →
//     pythontransformer (private helpers of the concrete)
//
// The user-visible pipeline behaviour is unchanged; the only
// externally visible delta is the port boundary (apply layer
// no longer imports os/exec).
package books

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"

	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// Config controls the books capability at construction time.
// Fase 7 review-fix #3 BACKFILL (July 2026): the ScriptPath /
// PythonBin / Enabled fields have been moved OUT of books.Config
// and INTO the canonical pythontransformer concrete's own Config
// (internal/infrastructure/books/pythontransformer/python_transformer.go).
// godlike/06 "one canonical owner per fact" — the apply-layer
// Service does not need to know about Python-execution details
// (script path, interpreter binary). The apply-layer Config now
// holds ONLY DriveFolderID; the transformer-owning Config holds
// ScriptPath + PythonBin + Enabled.
//
// The Enabled feature flag moved to a dedicated `enabled bool`
// field on books.Service (set via SetEnabled by the composition
// root) — the apply layer reaches the flag via the struct field,
// not via Config.
type Config struct {
	DriveFolderID string `yaml:"drive_folder_id"`
}

func DefaultConfig() *Config {
	return &Config{
		DriveFolderID: "",
	}
}

// PublisherPort is the narrow dependency for Drive uploads in
// the books capability. Satisfied by delivery.Publisher (via an
// adapter in the composition root). nil means Drive is disabled.
type PublisherPort interface {
	Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)
}

// ErrBookTransformerMissing is surfaced by ProcessBook +
// ProcessBookWithProgress when the Service was constructed with
// a nil BookTransformer port (the composition root never wired
// pythontransformer.SubprocessTransformer). Behaviourally a
// "service not initialised" failure: tests + production callers
// branch via errors.Is(err, ErrBookTransformerMissing). Maps
// to 503 in the books ErrorMappers.
var ErrBookTransformerMissing = errors.New("books transformer port not wired — cannot run book pipeline")

// Service is the apply-layer orchestrator for book processing.
// Phase 7 Spina Dorsale: the Python-execution responsibility
// moved to a BookTransformer port (wired by composition root to
// pythontransformer.SubprocessTransformer); Service no longer
// imports os/exec.
//
// Constructor: NewService. Either transformer or publisher may
// be nil so partial deployments (Drive disabled) keep the
// local-file path ProcessBook alive; the Drive-dependent paths
// (driveToDrive, ProcessBookFromDrive) surface the canonical
// "drive not configured" error on demand; the transformer-
// dependent paths (ProcessBook + ProcessBookWithProgress) surface
// ErrBookTransformerMissing on nil.
type Service struct {
	db          *sql.DB
	cfg         *Config
	enabled     bool // Fase 7 review-fix #3 BACKFILL: Enabled moved out of Config to a struct field; composition root sets it via SetEnabled.
	log         *zap.Logger
	driveFolder string
	publisher   PublisherPort
	reader      drive.Reader
	transformer BookTransformer // Phase 7: downstream port (pythontransformer.SubprocessTransformer)
}

// NewService constructs a books.Service. Publisher + Reader +
// Transformer are wired via constructor injection (godlike/06
// Pattern 0 + F2.10 closure precedent). The post-construction
// SetDrive* setters were removed (F2.10). The legacy
// `driveUploader *drive.Uploader` arg was dropped in F2.10.
//
// Phase 7 update: NewService now takes a BookTransformer port
// (8th positional arg, last) — composition root threads the
// pythontransformer.SubprocessTransformer concrete in via
// internal/app/build_bundles_core.go::buildBooksService. Tests
// pass nil (the ProcessBook + ProcessBookWithProgress paths
// fail-closed with ErrBookTransformerMissing; tests asserting
// ProcessBookFromDrive behaviour do not exercise ProcessBook).
//
// Fase 7 review-fix #3 BACKFILL: NewService defaults `enabled`
// to true; composition root flips it via SetEnabled based on
// cfg.Books.Enabled (the platform-config Boolean). Tests
// invoking ProcessBook without prior SetEnabled see enabled=true,
// matching the historical default (the pre-fix ProcessBook check
// was `if !s.cfg.Enabled`); the disabled path is now an explicit
// composition-root decision rather than a Config field.
func NewService(cfg *Config, db *sql.DB, driveFolder string, log *zap.Logger, publisher PublisherPort, reader drive.Reader, transformer BookTransformer) *Service {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Service{
		db:          db,
		cfg:         cfg,
		enabled:     true,
		log:         log,
		driveFolder: driveFolder,
		publisher:   publisher,
		reader:      reader,
		transformer: transformer,
	}
}

// SetEnabled flips the apply-layer feature flag. Called by the
// composition root with cfg.Books.Enabled (the platform-config
// Boolean). After Fase 7 review-fix #3 BACKFILL, the apply-layer
// Config no longer carries Enabled; this setter is the SINGLE
// site where the apply layer learns the platform's enable-state.
func (s *Service) SetEnabled(enabled bool) {
	s.enabled = enabled
}

type ProcessRequest struct {
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
	Language      string `json:"language,omitempty"`
	TranslateOnly bool   `json:"translate_only,omitempty"`
	GeneratePDF   bool   `json:"generate_pdf,omitempty"`
	PDFStyle      string `json:"pdf_style,omitempty"`
}

type ProcessResult struct {
	Success         bool   `json:"success"`
	OutputPath      string `json:"output_path,omitempty"`
	PDFPath         string `json:"pdf_path,omitempty"`
	DriveFolderURL  string `json:"drive_folder_url,omitempty"`
	DriveDocURL     string `json:"drive_doc_url,omitempty"`
	DrivePDFURL     string `json:"drive_pdf_url,omitempty"`
	WordCount       int    `json:"word_count,omitempty"`
	ChunksProcessed int    `json:"chunks_processed,omitempty"`
	Language        string `json:"language,omitempty"`
	Error           string `json:"error,omitempty"`
}

// ProcessBook runs the book-summarisation pipeline against the
// supplied request. Pre-Fase-7, this method called exec and
// parsed stdout inline; Phase 7 routes the work through
// s.transformer.Transform (the BookTransformer port). Composition
// root threads pythontransformer.SubprocessTransformer so the
// production wire-shape matches the legacy (Python script)
// behaviour 1:1; future concrete adapters (in-process LLM
// pipeline, REST summariser, etc.) plug into the same port
// without changing this method.
func (s *Service) ProcessBook(ctx context.Context, req *ProcessRequest) (*ProcessResult, error) {
	if !s.enabled {
		return nil, fmt.Errorf("books service is disabled")
	}
	if s.transformer == nil {
		return nil, ErrBookTransformerMissing
	}

	transformReq, err := s.processRequestToTransform(req)
	if err != nil {
		return nil, err
	}

	transformOut, err := s.transformer.Transform(ctx, transformReq)
	if err != nil {
		return nil, fmt.Errorf("failed to process book: %w", err)
	}
	return s.transformResultToProcess(transformOut, req), nil
}

// ProcessBookWithProgress is the streaming variant of
// ProcessBook. The onProgress callback is invoked by the
// transformer with [pct int, msg string] per [PROGRESS] %d %s
// line emitted by the Python subprocess (or by the future
// in-process pipeline). The implementation reaches the
// Python-aware concrete via the BookTransformer port's
// TransformWithProgress method.
func (s *Service) ProcessBookWithProgress(ctx context.Context, req *ProcessRequest, onProgress func(int, string)) (*ProcessResult, error) {
	if !s.enabled {
		return nil, fmt.Errorf("books service is disabled")
	}
	if s.transformer == nil {
		return nil, ErrBookTransformerMissing
	}

	transformReq, err := s.processRequestToTransform(req)
	if err != nil {
		return nil, err
	}

	transformOut, err := s.transformer.TransformWithProgress(ctx, transformReq, onProgress)
	if err != nil {
		return nil, fmt.Errorf("failed to process book: %w", err)
	}
	return s.transformResultToProcess(transformOut, req), nil
}

// processRequestToTransform maps the apply-layer ProcessRequest
// shape to the internal TransformRequest shape the port expects.
// Pre-Fase-7 the equivalent logic was inline in
// buildArgs (CLI args) + parseOutput (stdout parsing). The
// translation now lives here so the apply-layer Service is the
// only surface that knows about both wire shapes.
func (s *Service) processRequestToTransform(req *ProcessRequest) (*TransformRequest, error) {
	source := BookSourceDescription{}

	if req.GoogleDocURL != "" {
		docID := extractGoogleDocID(req.GoogleDocURL)
		if docID == "" {
			return nil, fmt.Errorf("invalid google_doc_url: could not extract document ID")
		}
		source.GoogleDocID = docID
	} else {
		source.LocalPath = req.FilePath
	}

	driveFolderID := req.DriveFolderID
	if driveFolderID == "" {
		driveFolderID = s.driveFolder
	}

	return &TransformRequest{
		Source:        source,
		Instruction:   req.Instruction,
		Model:         req.Model,
		PagesPerChunk: req.PagesPerChunk,
		ChunkSize:     req.ChunkSize,
		OverlapSize:   req.OverlapSize,
		MaxChunks:     req.MaxChunks,
		OllamaURL:     req.OllamaURL,
		DriveFolderID: driveFolderID,
		Language:      req.Language,
		TranslateOnly: req.TranslateOnly,
		GeneratePDF:   req.GeneratePDF,
		PDFStyle:      req.PDFStyle,
		OutputPath:    req.OutputPath,
	}, nil
}

// transformResultToProcess maps the internal TransformResult
// shape back to the apply-layer ProcessResult wire shape.
// Mirrors the canonical ProcessResult field set (Success +
// Error + OutputPath + PDF URLs + counters); the canonical
// ProcessResult Success=true/Error="" envelope mirrors the
// pre-Fase-7 buildArgs / parseOutput happy-path.
func (s *Service) transformResultToProcess(in *TransformResult, req *ProcessRequest) *ProcessResult {
	result := &ProcessResult{
		Success:  true,
		Language: in.Language,
	}

	if in.OutputPath != "" {
		result.OutputPath = in.OutputPath
	} else if req.OutputPath != "" {
		// Fallback preserved from pre-Fase-7 parseOutput: the
		// Python script may have written to req.OutputPath but the
		// [RESULT] block didn't include output_file (defensive).
		result.OutputPath = req.OutputPath
	}
	result.PDFPath = in.PDFPath
	result.DriveFolderURL = in.DriveFolderURL
	result.DriveDocURL = in.DriveDocURL
	result.DrivePDFURL = in.DrivePDFURL
	result.WordCount = in.WordCount
	result.ChunksProcessed = in.ChunksProcessed
	if result.Language == "" {
		result.Language = req.Language
	}
	return result
}

func (s *Service) ProcessBookAsync(ctx context.Context, req *ProcessRequest) (string, error) {
	result, err := s.ProcessBook(ctx, req)
	if err != nil {
		return "", err
	}
	if !result.Success {
		return "", errors.New(result.Error)
	}
	return fmt.Sprintf("book_sync_%d", time.Now().UnixNano()), nil
}

func (s *Service) IsEnabled() bool {
	return s.enabled
}

// extractGoogleDocID walks a Google Docs URL and returns the
// bare document ID. Pure URL parsing — kept here (apply layer)
// because the URL shape is part of the public wire contract
// (ProcessRequest.GoogleDocURL) and the transformer expects a
// bare ID at TransformRequest.Source.GoogleDocID. The mapping
// is the only place that knows both shapes.
//
// Pre-Fase-7: same helper existed in service.go::extractGoogleDocID.
// Phase 7 retained here because the apply Layer is the canonical
// place to convert IDs from external wire shapes into internal
// canonical IDs (godlike/06 "one owner per fact" per ID).
func extractGoogleDocID(url string) string {
	if url == "" {
		return ""
	}
	if !strings.Contains(url, "/") {
		return strings.TrimSpace(url)
	}
	parts := strings.Split(url, "/")
	for i, part := range parts {
		if part == "d" && i+1 < len(parts) {
			docID := parts[i+1]
			if idx := strings.Index(docID, "?"); idx > 0 {
				docID = docID[:idx]
			}
			return docID
		}
	}
	if strings.Contains(url, "document/d/") {
		if idx := strings.Index(url, "document/d/"); idx >= 0 {
			after := url[idx+12:]
			endIdx := strings.Index(after, "/")
			if endIdx > 0 {
				return after[:endIdx]
			}
			endIdx = strings.Index(after, "?")
			if endIdx > 0 {
				return after[:endIdx]
			}
			return after
		}
	}
	return ""
}
