package books

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// HandleJob processes the background job for book summarization.
// After the Python script finishes, uploads output files to Drive if they
// weren't already uploaded by the script (fallback).
func (s *Service) HandleJob(ctx context.Context, job *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("handling book.process job", zap.String("job_id", job.ID))

	var req ProcessRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting book processing")
	}

	// Use ProcessBookWithProgress for real-time progress tracking.
	// The Python script emits [PROGRESS] XX% markers that we parse and
	// forward to the job system for live progress updates.
	result, err := s.ProcessBookWithProgress(ctx, &req, func(pct int, msg string) {
		if tools.Progress != nil {
			tools.Progress(pct, msg)
		}
	})
	if err != nil {
		s.log.Error("book processing failed", zap.Error(err))
		return nil, fmt.Errorf("book processing failed: %w", err)
	}

	if !result.Success {
		return nil, errors.New(result.Error)
	}

	// Fallback Drive upload: if the Python script didn't upload, do it from Go
	s.driveToDrive(ctx, &req, result)

	if tools.Progress != nil {
		tools.Progress(100, "Book processing completed")
	}

	return map[string]any{
		"success":          true,
		"output_path":      result.OutputPath,
		"pdf_path":         result.PDFPath,
		"drive_folder_url": result.DriveFolderURL,
		"drive_doc_url":    result.DriveDocURL,
		"drive_pdf_url":    result.DrivePDFURL,
		"chunks_processed": result.ChunksProcessed,
		"language":         result.Language,
	}, nil
}

// driveToDrive uploads output files to Google Drive if the Python script
// didn't already do so (fallback). Uses the canonical Publisher (FASE 6)
// when available; falls back to legacy driveUpload for backward compat.
func (s *Service) driveToDrive(ctx context.Context, req *ProcessRequest, result *ProcessResult) {
	publisher := s.publisher
	driveUpload := s.driveUpload

	if publisher == nil && driveUpload == nil {
		return
	}

	// Determine target Drive folder (legacy fallback only)
	folderID := req.DriveFolderID
	if folderID == "" {
		folderID = s.driveFolder
	}
	if publisher == nil && folderID == "" {
		s.log.Debug("no drive folder configured, skipping upload")
		return
	}

	// Upload .txt output if missing from Drive
	if result.DriveDocURL == "" && result.OutputPath != "" {
		if publisher != nil {
			pubReq := delivery.PublishRequest{
				Destination:       delivery.DestinationBook,
				LocalPath:         result.OutputPath,
				Filename:          filepath.Base(result.OutputPath),
				RootFolderOverride: folderID,
			}
			pubResult, err := publisher.Publish(ctx, pubReq)
			if err != nil {
				s.log.Warn("Drive publish failed for .txt",
					zap.String("path", result.OutputPath),
					zap.Error(err))
			} else {
				result.DriveDocURL = pubResult.WebViewLink
				s.log.Info("published .txt to Drive",
					zap.String("path", result.OutputPath),
					zap.String("file_id", pubResult.FileID))
			}
		} else {
			uploadResult, err := driveUpload.UploadFile(ctx, result.OutputPath, folderID, filepath.Base(result.OutputPath))
			if err != nil {
				s.log.Warn("Drive upload failed for .txt",
					zap.String("path", result.OutputPath),
					zap.Error(err))
			} else {
				result.DriveDocURL = uploadResult.WebViewLink
				s.log.Info("uploaded .txt to Drive",
					zap.String("path", result.OutputPath),
					zap.String("file_id", uploadResult.FileID))
			}
		}
	}

	// Upload .pdf output if missing from Drive
	if result.DrivePDFURL == "" && result.PDFPath != "" {
		if publisher != nil {
			pubReq := delivery.PublishRequest{
				Destination:       delivery.DestinationBook,
				LocalPath:         result.PDFPath,
				Filename:          filepath.Base(result.PDFPath),
				RootFolderOverride: folderID,
			}
			pubResult, err := publisher.Publish(ctx, pubReq)
			if err != nil {
				s.log.Warn("Drive publish failed for .pdf",
					zap.String("path", result.PDFPath),
					zap.Error(err))
			} else {
				result.DrivePDFURL = pubResult.WebViewLink
				s.log.Info("published .pdf to Drive",
					zap.String("path", result.PDFPath),
					zap.String("file_id", pubResult.FileID))
			}
		} else {
			uploadResult, err := driveUpload.UploadFile(ctx, result.PDFPath, folderID, filepath.Base(result.PDFPath))
			if err != nil {
				s.log.Warn("Drive upload failed for .pdf",
					zap.String("path", result.PDFPath),
					zap.Error(err))
			} else {
				result.DrivePDFURL = uploadResult.WebViewLink
				s.log.Info("uploaded .pdf to Drive",
					zap.String("path", result.PDFPath),
					zap.String("file_id", uploadResult.FileID))
			}
		}
	}
}
