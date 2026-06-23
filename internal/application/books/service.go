package books

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

type Config struct {
	Enabled       bool   `yaml:"enabled"`
	ScriptPath    string `yaml:"script_path"`
	PythonBin     string `yaml:"python_bin"`
	DriveFolderID string `yaml:"drive_folder_id"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		ScriptPath:    "scripts/bridges/book_summarizer.py",
		PythonBin:     "python3",
		DriveFolderID: "",
	}
}

type Service struct {
	db           *sql.DB
	cfg          *Config
	log          *zap.Logger
	scriptPath   string
	driveFolder  string
	driveUpload  *drive.Uploader
	voiceoverSvc *voiceover.Service
}

// NewService constructs a books.Service. Drive uploader is wired via
// constructor injection; the post-construction SetDriveUploader setter was
// removed in PR4-H Commit 3.
func NewService(cfg *Config, db *sql.DB, driveFolder string, log *zap.Logger, voiceoverSvc *voiceover.Service, driveUploader *drive.Uploader) *Service {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	scriptPath := cfg.ScriptPath
	if !filepath.IsAbs(scriptPath) {
		absPath, err := filepath.Abs(scriptPath)
		if err == nil {
			scriptPath = absPath
		}
	}

	return &Service{
		db:           db,
		cfg:          cfg,
		log:          log,
		scriptPath:   scriptPath,
		driveFolder:  driveFolder,
		voiceoverSvc: voiceoverSvc,
		driveUpload:  driveUploader,
	}
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

func (s *Service) buildArgs(req *ProcessRequest) ([]string, error) {
	if req.FilePath == "" && req.GoogleDocURL == "" {
		return nil, fmt.Errorf("file_path or google_doc_url is required")
	}

	args := []string{filepath.Base(s.scriptPath)}

	if req.GoogleDocURL != "" {
		docID := extractGoogleDocID(req.GoogleDocURL)
		if docID == "" {
			return nil, fmt.Errorf("invalid google_doc_url: could not extract document ID")
		}
		args = append(args, "--google-doc-id", docID)
	} else {
		args = append(args, "--file", req.FilePath)
	}

	model := req.Model
	if model == "" {
		model = "gemma4:e4b"
	}
	args = append(args, "--model", model)

	if req.PagesPerChunk > 0 {
		args = append(args, "--pages-per-chunk", fmt.Sprintf("%d", req.PagesPerChunk))
	}
	chunkSize := req.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 12000
	}
	args = append(args, "--chunk-size", fmt.Sprintf("%d", chunkSize))
	if req.MaxChunks > 0 {
		args = append(args, "--max-chunks", fmt.Sprintf("%d", req.MaxChunks))
	}

	if req.OverlapSize > 0 {
		args = append(args, "--overlap-size", fmt.Sprintf("%d", req.OverlapSize))
	} else {
		args = append(args, "--overlap-size", "2000")
	}

	ollamaURL := req.OllamaURL
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}
	args = append(args, "--ollama-url", ollamaURL)

	if req.Instruction != "" {
		args = append(args, "--instruction", req.Instruction)
	}
	if req.OutputPath != "" {
		args = append(args, "--output", req.OutputPath)
	}

	driveFolderID := req.DriveFolderID
	if driveFolderID == "" {
		driveFolderID = s.driveFolder
	}
	if driveFolderID != "" {
		args = append(args, "--drive-folder-id", driveFolderID)
	}
	if req.Language != "" {
		args = append(args, "--language", req.Language)
	}
	if req.TranslateOnly {
		args = append(args, "--translate-only")
	}
	if req.GeneratePDF {
		args = append(args, "--generate-pdf")
	}
	if req.PDFStyle != "" {
		args = append(args, "--pdf-style", req.PDFStyle)
	}

	return args, nil
}

func (s *Service) ProcessBook(ctx context.Context, req *ProcessRequest) (*ProcessResult, error) {
	if !s.cfg.Enabled {
		return nil, fmt.Errorf("books service is disabled")
	}

	args, err := s.buildArgs(req)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, s.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(s.scriptPath)

	s.log.Info("processing book via script",
		zap.String("file", req.FilePath),
		zap.String("script", s.scriptPath),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &ProcessResult{
			Success: false,
			Error:   fmt.Errorf("book processing failed: %w, output: %s", err, strings.TrimSpace(string(output))).Error(),
		}, fmt.Errorf("failed to process book: %w", err)
	}

	return s.parseOutput(string(output), req), nil
}

func (s *Service) ProcessBookWithProgress(ctx context.Context, req *ProcessRequest, onProgress func(int, string)) (*ProcessResult, error) {
	if !s.cfg.Enabled {
		return nil, fmt.Errorf("books service is disabled")
	}

	args, err := s.buildArgs(req)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, s.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(s.scriptPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start book processor: %w", err)
	}

	var fullOutput strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		fullOutput.WriteString(line)
		fullOutput.WriteString("\n")

		if strings.HasPrefix(line, "[PROGRESS] ") {
			trimmed := strings.TrimPrefix(line, "[PROGRESS] ")
			if pct, msg, ok := parseProgressLine(trimmed); ok {
				if onProgress != nil {
					onProgress(pct, msg)
				}
				continue
			}
		}

		s.log.Debug("book script output", zap.String("line", line))
	}

	stderrBytes, _ := io.ReadAll(stderr)

	if err := cmd.Wait(); err != nil {
		errOutput := fullOutput.String() + "\n" + string(stderrBytes)
		return &ProcessResult{
			Success: false,
			Error:   fmt.Errorf("book processing failed: %w, output: %s", err, strings.TrimSpace(errOutput)).Error(),
		}, fmt.Errorf("failed to process book: %w", err)
	}

	return s.parseOutput(fullOutput.String(), req), nil
}

func parseProgressLine(s string) (int, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), "%", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	pct, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || pct < 0 || pct > 100 {
		return 0, "", false
	}
	return pct, strings.TrimSpace(parts[1]), true
}

func (s *Service) parseOutput(outputStr string, req *ProcessRequest) *ProcessResult {
	s.log.Info("book processed", zap.String("output_preview", outputStr[:min(len(outputStr), 300)]))

	result := &ProcessResult{
		Success:  true,
		Language: req.Language,
	}

	if idx := strings.Index(outputStr, "[RESULT]"); idx >= 0 {
		rawJSON := outputStr[idx+8:]
		if closeIdx := strings.LastIndex(rawJSON, "}"); closeIdx >= 0 {
			rawJSON = rawJSON[:closeIdx+1]
		}
		jsonStr := strings.TrimSpace(rawJSON)
		var resultJSON map[string]any
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
			if drive, ok := resultJSON["drive"].(map[string]any); ok {
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

	if result.OutputPath == "" && req.OutputPath != "" {
		result.OutputPath = req.OutputPath
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

func (s *Service) SetVoiceoverService(v *voiceover.Service) {
	s.voiceoverSvc = v
}

func (s *Service) IsEnabled() bool {
	return s.cfg.Enabled
}

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
