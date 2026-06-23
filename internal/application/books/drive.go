package books

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

type ProcessFromDriveRequest struct {
	DriveFileURL      string `json:"drive_file_url"`
	Instruction       string `json:"instruction,omitempty"`
	Model             string `json:"model,omitempty"`
	PagesPerChunk     int    `json:"pages_per_chunk,omitempty"`
	ChunkSize         int    `json:"chunk_size,omitempty"`
	OverlapSize       int    `json:"overlap_size,omitempty"`
	MaxChunks         int    `json:"max_chunks,omitempty"`
	OllamaURL         string `json:"ollama_url,omitempty"`
	DriveFolderID     string `json:"drive_folder_id,omitempty"`
	OutputPath        string `json:"output_path,omitempty"`
	Language          string `json:"language,omitempty"`
	TranslateOnly     bool   `json:"translate_only,omitempty"`
	GeneratePDF       bool   `json:"generate_pdf,omitempty"`
	PDFStyle          string `json:"pdf_style,omitempty"`
	GenerateVoiceover bool   `json:"generate_voiceover"`
	VoiceoverLanguage string `json:"voiceover_language,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`
}

type ProcessFromDriveResult struct {
	Success            bool           `json:"success"`
	BookResult         *ProcessResult `json:"book_result,omitempty"`
	VoiceoverPath      string         `json:"voiceover_path,omitempty"`
	VoiceoverDriveLink string         `json:"voiceover_drive_link,omitempty"`
	VoiceoverDriveID   string         `json:"voiceover_drive_id,omitempty"`
	VoiceoverError     string         `json:"voiceover_error,omitempty"`
	Error              string         `json:"error,omitempty"`
}

func (s *Service) ProcessBookFromDrive(ctx context.Context, req *ProcessFromDriveRequest) (*ProcessFromDriveResult, error) {
	if !s.cfg.Enabled {
		return nil, fmt.Errorf("books service is disabled")
	}
	if s.driveUpload == nil {
		return nil, fmt.Errorf("drive uploader not configured — cannot download from Drive")
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

	meta, err := s.driveUpload.GetFileMeta(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive file metadata: %w", err)
	}

	body, _, err := s.driveUpload.DownloadFile(ctx, fileID)
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

	result := &ProcessFromDriveResult{
		Success:    true,
		BookResult: bookResult,
	}

	if req.GenerateVoiceover && s.voiceoverSvc != nil {
		s.log.Info("generating voiceover from book output",
			zap.String("output_path", bookResult.OutputPath),
		)

		outputContent, err := os.ReadFile(bookResult.OutputPath)
		if err != nil {
			result.VoiceoverError = fmt.Sprintf("failed to read book output: %v", err)
			s.log.Warn("voiceover generation skipped: cannot read output", zap.Error(err))
			return result, nil
		}

		voiceoverLang := req.VoiceoverLanguage
		if voiceoverLang == "" {
			voiceoverLang = "it"
		}

		filename := fmt.Sprintf("book_voiceover_%s.mp3", filepath.Base(bookResult.OutputPath))

		var voResult *voiceover.VoiceoverResult
		if req.VoiceoverFolderID != "" {
			voResult, err = s.voiceoverSvc.GenerateWithDestination(ctx, string(outputContent), voiceoverLang, filename, &voiceover.DestinationRequest{
				FolderID: req.VoiceoverFolderID,
			})
		} else {
			voResult, err = s.voiceoverSvc.Generate(ctx, string(outputContent), voiceoverLang, filename)
		}

		if err != nil {
			result.VoiceoverError = fmt.Sprintf("voiceover generation failed: %v", err)
			s.log.Warn("voiceover generation failed", zap.Error(err))
		} else if voResult != nil && voResult.OK {
			result.VoiceoverPath = voResult.Path
			result.VoiceoverDriveLink = voResult.DriveLink
			result.VoiceoverDriveID = voResult.DriveFileID
			s.log.Info("voiceover generated successfully",
				zap.String("path", voResult.Path),
				zap.String("drive_link", voResult.DriveLink),
			)
		} else if voResult != nil && !voResult.OK {
			result.VoiceoverError = fmt.Sprintf("voiceover generation returned error: %s", voResult.Error)
		}
	} else if req.GenerateVoiceover && s.voiceoverSvc == nil {
		result.VoiceoverError = "voiceover service not configured"
	}

	return result, nil
}
