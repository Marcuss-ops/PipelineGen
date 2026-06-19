package books

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	booksService "github.com/Marcuss-ops/PipelineGen/internal/media/books"
)

// BooksHandler exposes book processing endpoints
type BooksHandler struct {
	svc     *booksService.Service
	jobsSvc *jobs.Service
	log     *zap.Logger
}

// NewHandler creates a new books handler
func NewBooksHandler(svc *booksService.Service, jobsSvc *jobs.Service, log *zap.Logger) *BooksHandler {
	return &BooksHandler{
		svc:     svc,
		jobsSvc: jobsSvc,
		log:     log,
	}
}

// RegisterRoutes registers /api/books routes
func (h *BooksHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.ProcessBook)
	r.POST("/generate", h.ProcessBook) // alias for consistency with other endpoints
	r.POST("/process-from-drive", h.ProcessBookFromDrive)
	r.GET("/jobs", h.ListJobs)
}

// ProcessBookRequest is the input for book processing
type ProcessBookRequest struct {
	FilePath      string `json:"file_path"`                 // Path to PDF/EPUB file (required if no GoogleDocURL)
	GoogleDocURL  string `json:"google_doc_url"`            // Google Docs URL to download and process
	Instruction   string `json:"instruction,omitempty"`     // Custom rewrite instruction
	Model         string `json:"model,omitempty"`           // Ollama model (default: gemma4:e4b)
	PagesPerChunk int    `json:"pages_per_chunk,omitempty"` // Pages per chunk for PDF (default: 4)
	ChunkSize     int    `json:"chunk_size,omitempty"`      // Max chars per chunk for EPUB (default: 12000)
	OverlapSize   int    `json:"overlap_size,omitempty"`    // Characters of overlap/context from previous chunk (default: 2000)
	MaxChunks     int    `json:"max_chunks,omitempty"`      // Process only first N chunks (0 = all)
	OllamaURL     string `json:"ollama_url,omitempty"`      // Ollama endpoint override
	DriveFolderID string `json:"drive_folder_id,omitempty"` // Google Drive folder for upload
	OutputPath    string `json:"output_path,omitempty"`     // Custom output path
	Async         bool   `json:"async,omitempty"`           // Run as background job
	Language      string `json:"language,omitempty"`        // Target language for translation (en, es, fr, de, it, pt, etc.)
	TranslateOnly bool   `json:"translate_only,omitempty"`  // Skip rewriting, only translate original text
	GeneratePDF   bool   `json:"generate_pdf,omitempty"`    // Generate PDF version in addition to text
	PDFStyle      string `json:"pdf_style,omitempty"`       // Style theme for PDF (default: modern)
}

// ProcessBook handles POST /api/books/process
// Processes a PDF/EPUB book using the book_summarizer.py script
func (h *BooksHandler) ProcessBook(c *gin.Context) {
	if !api.RequireService(c, h.svc, "books service") {
		return
	}

	var req ProcessBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}

	if req.FilePath == "" && req.GoogleDocURL == "" {
		api.BadRequest(c, "file_path or google_doc_url is required")
		return
	}

	// Check if async processing is requested
	if req.Async {
		if !api.RequireService(c, h.jobsSvc, "job system") {
			return
		}
		h.log.Info("enqueuing async book process job", zap.String("file", req.FilePath))
			api.EnqueueAsync(c, h.jobsSvc, &api.EnqueueInput{
			Type: job.TypeBooksProcess,
			Payload: map[string]any{
				"file_path":       req.FilePath,
				"google_doc_url":  req.GoogleDocURL,
				"instruction":     req.Instruction,
				"model":           req.Model,
				"pages_per_chunk": req.PagesPerChunk,
				"chunk_size":      req.ChunkSize,
				"max_chunks":      req.MaxChunks,
				"ollama_url":      req.OllamaURL,
				"drive_folder_id": req.DriveFolderID,
				"output_path":     req.OutputPath,
				"language":        req.Language,
				"translate_only":  req.TranslateOnly,
				"generate_pdf":    req.GeneratePDF,
				"pdf_style":       req.PDFStyle,
			},
			Priority:  5,
			ActiveKey: "books_process_" + req.FilePath + "|" + req.GoogleDocURL,
		}, "Book processing enqueued.")
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
		h.log.Error("book processing failed", zap.Error(err))
		api.InternalError(c, err)
		return
	}

	if !result.Success {
		api.Error(c, http.StatusInternalServerError, result.Error)
		return
	}

	api.OK(c, gin.H{
		"ok":               true,
		"success":          true,
		"output_path":      result.OutputPath,
		"pdf_path":         result.PDFPath,
		"drive_folder":     result.DriveFolderURL,
		"drive_doc_url":    result.DriveDocURL,
		"drive_pdf_url":    result.DrivePDFURL,
		"word_count":       result.WordCount,
		"chunks_processed": result.ChunksProcessed,
		"language":         result.Language,
	})
}

// ListJobs returns all book processing jobs with status, progress, and results.
// GET /api/books/jobs?status=queued&limit=20&offset=0
func (h *BooksHandler) ListJobs(c *gin.Context) {
	if !api.RequireService(c, h.jobsSvc, "job system") {
		return
	}

	pag := api.ParsePagination(c, 20, 1000)
	jobType := job.TypeBooksProcess

	filter := job.Filter{
		Type:   &jobType,
		job.Status: (*job.job.Status)(api.ParseJobStatusFilter(c)),
		Limit:  pag.Limit,
		Offset: pag.Offset,
	}

	jobsList, err := h.jobsSvc.List(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("failed to list book jobs", zap.Error(err))
		api.InternalError(c, err)
		return
	}

	api.ListJobsResponse(c, api.BuildJobSummaries(jobsList))
}

// ProcessBookFromDriveRequest is the input for processing a book from a Drive file URL.
type ProcessBookFromDriveRequest struct {
	DriveFileURL      string `json:"drive_file_url"`                // Google Drive file URL (required)
	Instruction       string `json:"instruction,omitempty"`         // Custom rewrite instruction
	Model             string `json:"model,omitempty"`               // Ollama model (default: hf.co/unsloth/gemma-4-12b-it-GGUF:UD-Q4_K_XL)
	PagesPerChunk     int    `json:"pages_per_chunk,omitempty"`     // Pages per chunk for PDF (default: 4)
	ChunkSize         int    `json:"chunk_size,omitempty"`          // Max chars per chunk for EPUB (default: 24000)
	OverlapSize       int    `json:"overlap_size,omitempty"`        // Characters of overlap/context from previous chunk (default: 2000)
	MaxChunks         int    `json:"max_chunks,omitempty"`          // Process only first N chunks (0 = all)
	OllamaURL         string `json:"ollama_url,omitempty"`          // Ollama endpoint override
	DriveFolderID     string `json:"drive_folder_id,omitempty"`     // Google Drive folder for book upload
	OutputPath        string `json:"output_path,omitempty"`         // Custom output path
	Async             bool   `json:"async,omitempty"`               // Run as background job
	Language          string `json:"language,omitempty"`            // Target language for translation
	TranslateOnly     bool   `json:"translate_only,omitempty"`      // Skip rewriting, only translate
	GeneratePDF       bool   `json:"generate_pdf,omitempty"`        // Generate PDF version
	PDFStyle          string `json:"pdf_style,omitempty"`           // Style theme for PDF
	GenerateVoiceover bool   `json:"generate_voiceover,omitempty"`  // Generate voiceover from the rewritten text
	VoiceoverLanguage string `json:"voiceover_language,omitempty"`  // Language for voiceover (default: it)
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"` // Drive folder for voiceover upload
}

// ProcessBookFromDrive handles POST /api/books/process-from-drive
// Downloads a PDF/EPUB from Google Drive, processes it, and optionally generates voiceover.
func (h *BooksHandler) ProcessBookFromDrive(c *gin.Context) {
	if !api.RequireService(c, h.svc, "books service") {
		return
	}

	var req ProcessBookFromDriveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}

	if req.DriveFileURL == "" {
		api.BadRequest(c, "drive_file_url is required")
		return
	}

	h.log.Info("processing book from drive",
		zap.String("drive_file_url", req.DriveFileURL),
		zap.Bool("generate_voiceover", req.GenerateVoiceover),
	)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Minute)
	defer cancel()

	result, err := h.svc.ProcessBookFromDrive(ctx, &booksService.ProcessFromDriveRequest{
		DriveFileURL:      req.DriveFileURL,
		Instruction:       req.Instruction,
		Model:             req.Model,
		PagesPerChunk:     req.PagesPerChunk,
		ChunkSize:         req.ChunkSize,
		OverlapSize:       req.OverlapSize,
		MaxChunks:         req.MaxChunks,
		OllamaURL:         req.OllamaURL,
		DriveFolderID:     req.DriveFolderID,
		OutputPath:        req.OutputPath,
		Language:          req.Language,
		TranslateOnly:     req.TranslateOnly,
		GeneratePDF:       req.GeneratePDF,
		PDFStyle:          req.PDFStyle,
		GenerateVoiceover: req.GenerateVoiceover,
		VoiceoverLanguage: req.VoiceoverLanguage,
		VoiceoverFolderID: req.VoiceoverFolderID,
	})

	if err != nil {
		h.log.Error("book processing from drive failed", zap.Error(err))
		api.InternalError(c, err)
		return
	}

	if !result.Success {
		api.Error(c, http.StatusInternalServerError, result.Error)
		return
	}

	// Build response
	resp := gin.H{
		"ok":      true,
		"success": true,
	}

	if result.BookResult != nil {
		resp["output_path"] = result.BookResult.OutputPath
		resp["pdf_path"] = result.BookResult.PDFPath
		resp["drive_folder"] = result.BookResult.DriveFolderURL
		resp["drive_doc_url"] = result.BookResult.DriveDocURL
		resp["drive_pdf_url"] = result.BookResult.DrivePDFURL
		resp["chunks_processed"] = result.BookResult.ChunksProcessed
		resp["language"] = result.BookResult.Language
	}

	if result.VoiceoverPath != "" {
		resp["voiceover_path"] = result.VoiceoverPath
		resp["voiceover_drive_link"] = result.VoiceoverDriveLink
		resp["voiceover_drive_id"] = result.VoiceoverDriveID
	}
	if result.VoiceoverError != "" {
		resp["voiceover_error"] = result.VoiceoverError
	}

	api.OK(c, resp)
}
