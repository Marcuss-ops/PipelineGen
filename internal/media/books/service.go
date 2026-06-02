package books

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Config holds books service configuration
type Config struct {
	Enabled       bool   `yaml:"enabled"`
	ScriptPath    string `yaml:"script_path"`
	PythonBin     string `yaml:"python_bin"`
	DriveFolderID string `yaml:"drive_folder_id"`
}

// DefaultConfig returns default books config
func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		ScriptPath:    "scripts/book_summarizer.py",
		PythonBin:     "python3",
		DriveFolderID: "",
	}
}

// Service provides book summarization/processing functionality
type Service struct {
	db           *sql.DB
	cfg          *Config
	log          *zap.Logger
	scriptPath   string
	driveFolder  string
}

// NewService creates a new books service
func NewService(cfg *Config, db *sql.DB, driveFolder string, log *zap.Logger) *Service {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Resolve script path to absolute
	scriptPath := cfg.ScriptPath
	if !filepath.IsAbs(scriptPath) {
		absPath, err := filepath.Abs(scriptPath)
		if err == nil {
			scriptPath = absPath
		}
	}

	return &Service{
		db:          db,
		cfg:         cfg,
		log:         log,
		scriptPath:  scriptPath,
		driveFolder: driveFolder,
	}
}

// ProcessRequest represents a book processing request
type ProcessRequest struct {
	FilePath        string `json:"file_path"`                 // Path to PDF or EPUB file
	GoogleDocURL    string `json:"google_doc_url"`            // Google Docs URL to download
	Instruction     string `json:"instruction,omitempty"`     // Custom rewrite instruction
	Model           string `json:"model,omitempty"`           // Ollama model (default: gemma3:12b)
	PagesPerChunk   int    `json:"pages_per_chunk,omitempty"` // Pages per chunk for PDF (default: 4)
	ChunkSize       int    `json:"chunk_size,omitempty"`      // Max chars per chunk for EPUB (default: 12000)
	MaxChunks       int    `json:"max_chunks,omitempty"`      // Process only first N chunks (0 = all)
	OllamaURL       string `json:"ollama_url,omitempty"`      // Ollama endpoint
	DriveFolderID   string `json:"drive_folder_id,omitempty"` // Override Drive folder
	OutputPath      string `json:"output_path,omitempty"`     // Custom output path
	Language        string `json:"language,omitempty"`        // Target language for translation
	TranslateOnly   bool   `json:"translate_only,omitempty"`  // Skip rewriting, only translate
	GeneratePDF     bool   `json:"generate_pdf,omitempty"`    // Generate PDF version
}

// ProcessResult represents the result of book processing
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

// ProcessBook processes a PDF/EPUB book using book_summarizer.py
func (s *Service) ProcessBook(ctx context.Context, req *ProcessRequest) (*ProcessResult, error) {
	if !s.cfg.Enabled {
		return nil, fmt.Errorf("books service is disabled")
	}

	if req.FilePath == "" && req.GoogleDocURL == "" {
		return nil, fmt.Errorf("file_path or google_doc_url is required")
	}

	// Build command arguments
	args := []string{filepath.Base(s.scriptPath)}

	// Handle input source - either file or Google Docs
	if req.GoogleDocURL != "" {
		// Extract doc ID from URL
		docID := extractGoogleDocID(req.GoogleDocURL)
		if docID == "" {
			return nil, fmt.Errorf("invalid google_doc_url: could not extract document ID")
		}
		args = append(args, "--google-doc-id", docID)
	} else {
		args = append(args, "--file", req.FilePath)
	}

	// Model
	model := req.Model
	if model == "" {
		model = "gemma3:12b"
	}
	args = append(args, "--model", model)

	// Pages per chunk
	if req.PagesPerChunk > 0 {
		args = append(args, "--pages-per-chunk", fmt.Sprintf("%d", req.PagesPerChunk))
	}

	// Chunk size
	if req.ChunkSize > 0 {
		args = append(args, "--chunk-size", fmt.Sprintf("%d", req.ChunkSize))
	}

	// Max chunks
	if req.MaxChunks > 0 {
		args = append(args, "--max-chunks", fmt.Sprintf("%d", req.MaxChunks))
	}

	// Ollama URL
	ollamaURL := req.OllamaURL
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}
	args = append(args, "--ollama-url", ollamaURL)

	// Custom instruction
	if req.Instruction != "" {
		args = append(args, "--instruction", req.Instruction)
	}

	// Output path
	if req.OutputPath != "" {
		args = append(args, "--output", req.OutputPath)
	}

	// Drive folder ID (prefer request override, else config)
	driveFolderID := req.DriveFolderID
	if driveFolderID == "" {
		driveFolderID = s.driveFolder
	}
	if driveFolderID != "" {
		args = append(args, "--drive-folder-id", driveFolderID)
	}

	// Language (translation target)
	if req.Language != "" {
		args = append(args, "--language", req.Language)
	}

	// Translate only mode
	if req.TranslateOnly {
		args = append(args, "--translate-only")
	}

	// Generate PDF
	if req.GeneratePDF {
		args = append(args, "--generate-pdf")
	}

	// Execute Python script from the script's directory
	cmd := exec.CommandContext(ctx, s.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(s.scriptPath)

	s.log.Info("processing book via script",
		zap.String("file", req.FilePath),
		zap.String("script", s.scriptPath),
		zap.String("model", model),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &ProcessResult{
			Success: false,
			Error:   fmt.Errorf("book processing failed: %w, output: %s", err, strings.TrimSpace(string(output))).Error(),
		}, fmt.Errorf("failed to process book: %w", err)
	}

	outputStr := string(output)
	s.log.Info("book processed", zap.String("output", outputStr[:min(len(outputStr), 500)]))

	// Parse result
	result := &ProcessResult{
		Success: true,
		Language: req.Language,
	}

	// Try to parse JSON result from output (the script outputs [RESULT] JSON)
	if idx := strings.Index(outputStr, "[RESULT]"); idx >= 0 {
		jsonStr := strings.TrimSpace(outputStr[idx+8:])
		var resultJSON map[string]interface{}
		if json.Unmarshal([]byte(jsonStr), &resultJSON) == nil {
			if v, ok := resultJSON["output_file"].(string); ok && v != "" {
				result.OutputPath = v
			}
			if v, ok := resultJSON["pdf_file"].(string); ok && v != "" {
				result.PDFPath = v
			}
			if v, ok := resultJSON["language"].(string); ok && v != "" {
				result.Language = v
			}
			if v, ok := resultJSON["chunks_processed"].(float64); ok {
				result.ChunksProcessed = int(math.Round(v))
			}
			if drive, ok := resultJSON["drive"].(map[string]interface{}); ok {
				if v, ok := drive["folder"].(string); ok && v != "" {
					result.DriveFolderURL = v
				}
				if v, ok := drive["document"].(string); ok && v != "" {
					result.DriveDocURL = v
				}
				if v, ok := drive["pdf"].(string); ok && v != "" {
					result.DrivePDFURL = v
				}
			}
		}
	} else {
		// Fallback: extract from text output
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Saved summary to:") {
				if parts := strings.Split(line, "Saved summary to:"); len(parts) > 1 {
					result.OutputPath = strings.TrimSpace(parts[1])
				}
			}
			if strings.Contains(line, "Generated PDF:") {
				if parts := strings.Split(line, "Generated PDF:"); len(parts) > 1 {
					result.PDFPath = strings.TrimSpace(parts[1])
				}
			}
			if strings.Contains(line, "Uploaded to Google Docs:") {
				if parts := strings.Split(line, "Uploaded to Google Docs:"); len(parts) > 1 {
					result.DriveDocURL = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	// Default output path if not found
	if result.OutputPath == "" && req.OutputPath != "" {
		result.OutputPath = req.OutputPath
	}

	return result, nil
}

// ProcessBookAsync creates a background job for book processing
func (s *Service) ProcessBookAsync(ctx context.Context, req *ProcessRequest) (string, error) {
	// For now, process synchronously and return a fake job ID
	// The job system integration will be handled by the job handler
	result, err := s.ProcessBook(ctx, req)
	if err != nil {
		return "", err
	}

	if !result.Success {
		return "", fmt.Errorf(result.Error)
	}

	return fmt.Sprintf("book_sync_%d", time.Now().UnixNano()), nil
}

// IsEnabled returns whether the service is enabled
func (s *Service) IsEnabled() bool {
	return s.cfg.Enabled
}

// extractGoogleDocID extracts the document ID from a Google Docs URL
// Supports formats:
// - https://docs.google.com/document/d/DOC_ID/edit
// - https://docs.google.com/document/d/DOC_ID/
// - DOC_ID (raw ID)
func extractGoogleDocID(url string) string {
	if url == "" {
		return ""
	}

	// If it's just the ID (no slashes), return it directly
	if !strings.Contains(url, "/") {
		return strings.TrimSpace(url)
	}

	// Extract from URL path
	// Pattern: /document/d/DOC_ID/
	parts := strings.Split(url, "/")
	for i, part := range parts {
		if part == "d" && i+1 < len(parts) {
			docID := parts[i+1]
			// Clean up any query parameters
			if idx := strings.Index(docID, "?"); idx > 0 {
				docID = docID[:idx]
			}
			return docID
		}
	}

	// Fallback: try regex-like extraction
	if strings.Contains(url, "document/d/") {
		if idx := strings.Index(url, "document/d/"); idx >= 0 {
			after := url[idx+12:]
			endIdx := strings.Index(after, "/")
			if endIdx > 0 {
				return after[:endIdx]
			}
			// Try without trailing slash
			endIdx = strings.Index(after, "?")
			if endIdx > 0 {
				return after[:endIdx]
			}
			return after
		}
	}

	return ""
}