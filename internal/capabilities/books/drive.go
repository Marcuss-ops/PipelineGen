package books

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

var ErrBookReaderNotConfigured = fmt.Errorf("drive reader not configured — cannot download from Drive")

type ProcessFromDriveRequest struct {
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

type ProcessFromDriveResult struct {
	Success    bool           `json:"success"`
	BookResult *ProcessResult `json:"book_result,omitempty"`
	Error      string         `json:"error,omitempty"`
}

func (s *Service) ProcessBookFromDrive(ctx context.Context, req *ProcessFromDriveRequest) (*ProcessFromDriveResult, error) {
	// Fase 7 review-fix #3 BACKFILL: Enabled moved from books.Config
	// to books.Service.enabled (composition root projects
	// cfg.Books.Enabled via SetEnabled). This is the only
	// remaining reference of `s.cfg.Enabled` in the books surface
	// after the BACKFILL — the ProcessBook + ProcessBookWithProgress
	// + IsEnabled paths already migrated in the canonical BACKFILL
	// commit; this ProcessBookFromDrive line completes the surface
	// (drive.go also gates on the apply-layer feature flag).
	if !s.enabled {
		return nil, fmt.Errorf("books service is disabled")
	}
	// F2.10+: driveUpload (concrete *drive.Uploader) was retired in
	// favour of the canonical drive.Reader port per DRIVE-005
	// closure. A nil reader keeps the error contract that the
	// pre-F2.10 driveUpload check enforced ("Drive not configured")
	// — tests that exercise ProcessBookFromDrive with Drive disabled
	// stay green.
	if s.reader == nil {
		return nil, ErrBookReaderNotConfigured
	}

	fileID, err := urlutil.FileIDFromDriveLink(req.DriveFileURL)
	if err != nil {
		return nil, fmt.Errorf("invalid drive file URL: %w", err)
	}
	if fileID == "" {
		return nil, fmt.Errorf("drive_file_url is required")
	}

	s.log.Info("downloading book from drive",
		zap.String("file_id", fileID),
		zap.String("url", req.DriveFileURL),
	)

	meta, err := s.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive file metadata: %w", err)
	}

	body, _, err := s.reader.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("failed to download file from drive: %w", err)
	}
	defer body.Close()

	tempDir := filepath.Join(os.TempDir(), "book_from_drive")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	tempPath := filepath.Join(tempDir, meta.Name)
	f, err := os.Create(tempPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	n, err := io.Copy(f, body)
	f.Close()
	if err != nil {
		os.Remove(tempPath)
		return nil, fmt.Errorf("failed to write downloaded file: %w", err)
	}

	s.log.Info("downloaded file from drive",
		zap.String("path", tempPath),
		zap.String("name", meta.Name),
		zap.Int64("bytes", n),
	)

	defer func() {
		if err := os.Remove(tempPath); err != nil {
			s.log.Warn("failed to clean up temp file", zap.String("path", tempPath), zap.Error(err))
		}
	}()

	processReq := &ProcessRequest{
		FilePath:      tempPath,
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
	}

	bookResult, err := s.ProcessBook(ctx, processReq)
	if err != nil {
		return &ProcessFromDriveResult{
			Success: false,
			Error:   fmt.Sprintf("book processing failed: %v", err),
		}, nil
	}
	if !bookResult.Success {
		return &ProcessFromDriveResult{
			Success: false,
			Error:   bookResult.Error,
		}, nil
	}

	return &ProcessFromDriveResult{
		Success:    true,
		BookResult: bookResult,
	}, nil
}
