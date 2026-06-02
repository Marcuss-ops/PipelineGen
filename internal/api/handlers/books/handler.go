package books

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	booksService "velox/go-master/internal/media/books"
	"velox/go-master/internal/pkg/apiutil"
)

// Handler exposes book processing endpoints
type Handler struct {
	svc  *booksService.Service
	log  *zap.Logger
}

// NewHandler creates a new books handler
func NewHandler(svc *booksService.Service, log *zap.Logger) *Handler {
	return &Handler{
		svc:  svc,
		log:  log,
	}
}

// RegisterRoutes registers /api/books routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.ProcessBook)
	r.POST("/generate", h.ProcessBook) // alias for consistency with other endpoints
}

// ProcessBookRequest is the input for book processing
type ProcessBookRequest struct {
	FilePath      string `json:"file_path"`                             // Path to PDF/EPUB file (required if no GoogleDocURL)
	GoogleDocURL  string `json:"google_doc_url"`                        // Google Docs URL to download and process
	Instruction   string `json:"instruction,omitempty"`                  // Custom rewrite instruction
	Model         string `json:"model,omitempty"`                        // Ollama model (default: gemma3:12b)
	PagesPerChunk int    `json:"pages_per_chunk,omitempty"`              // Pages per chunk for PDF (default: 4)
	ChunkSize     int    `json:"chunk_size,omitempty"`                   // Max chars per chunk for EPUB (default: 12000)
	MaxChunks     int    `json:"max_chunks,omitempty"`                   // Process only first N chunks (0 = all)
	OllamaURL     string `json:"ollama_url,omitempty"`                   // Ollama endpoint override
	DriveFolderID string `json:"drive_folder_id,omitempty"`              // Google Drive folder for upload
	OutputPath    string `json:"output_path,omitempty"`                  // Custom output path
	Async         bool   `json:"async,omitempty"`                        // Run as background job
	Language      string `json:"language,omitempty"`                     // Target language for translation (en, es, fr, de, it, pt, etc.)
	TranslateOnly bool   `json:"translate_only,omitempty"`               // Skip rewriting, only translate original text
	GeneratePDF   bool   `json:"generate_pdf,omitempty"`                 // Generate PDF version in addition to text
}

// ProcessBook handles POST /api/books/process
// Processes a PDF/EPUB book using the book_summarizer.py script
func (h *Handler) ProcessBook(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "books service not initialized")
		return
	}

	req, ok := apiutil.BindJSON[ProcessBookRequest](c)
	if !ok {
		return
	}

	if req.FilePath == "" && req.GoogleDocURL == "" {
		apiutil.BadRequest(c, "file_path or google_doc_url is required")
		return
	}

	// Check if async processing is requested
	if req.Async {
		h.processAsync(c, &req)
		return
	}

	// Synchronous processing with timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()

	h.log.Info("processing book synchronously",
		zap.String("file", req.FilePath),
		zap.String("google_doc_url", req.GoogleDocURL),
		zap.String("instruction", req.Instruction),
	)

	result, err := h.svc.ProcessBook(ctx, &booksService.ProcessRequest{
		FilePath:       req.FilePath,
		GoogleDocURL:   req.GoogleDocURL,
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
	})

	if err != nil {
		h.log.Error("book processing failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	if !result.Success {
		apiutil.Error(c, http.StatusInternalServerError, result.Error)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":             true,
		"success":        true,
		"output_path":    result.OutputPath,
		"pdf_path":       result.PDFPath,
		"drive_folder":   result.DriveFolderURL,
		"drive_doc_url":  result.DriveDocURL,
		"drive_pdf_url":  result.DrivePDFURL,
		"word_count":     result.WordCount,
		"chunks_processed": result.ChunksProcessed,
		"language":       result.Language,
	})
}

// processAsync creates a background job for book processing
func (h *Handler) processAsync(c *gin.Context, req *ProcessBookRequest) {
	// TODO: Integrate with job system for async processing
	// For now, fall back to sync processing
	h.log.Info("async requested but not implemented yet, falling back to sync")
	
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()

	result, err := h.svc.ProcessBook(ctx, &booksService.ProcessRequest{
		FilePath:       req.FilePath,
		GoogleDocURL:   req.GoogleDocURL,
		Instruction:    req.Instruction,
		Model:          req.Model,
		PagesPerChunk:  req.PagesPerChunk,
		ChunkSize:      req.ChunkSize,
		MaxChunks:      req.MaxChunks,
		OllamaURL:      req.OllamaURL,
		DriveFolderID:  req.DriveFolderID,
		OutputPath:     req.OutputPath,
		Language:       req.Language,
		TranslateOnly:  req.TranslateOnly,
		GeneratePDF:    req.GeneratePDF,
	})

	if err != nil {
		h.log.Error("book processing failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":             true,
		"success":        true,
		"output_path":    result.OutputPath,
		"pdf_path":       result.PDFPath,
		"drive_folder":   result.DriveFolderURL,
		"drive_doc_url":  result.DriveDocURL,
		"drive_pdf_url":  result.DrivePDFURL,
		"word_count":     result.WordCount,
		"chunks_processed": result.ChunksProcessed,
		"language":       result.Language,
	})
}