package books

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
)

// HandleJob processes the background job for book summarization
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobs.JobTools) (map[string]any, error) {
	s.log.Info("handling book.process job", zap.String("job_id", job.ID))

	var req ProcessRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting book processing")
	}

	result, err := s.ProcessBook(ctx, &req)
	if err != nil {
		s.log.Error("book processing failed", zap.Error(err))
		return nil, fmt.Errorf("book processing failed: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf(result.Error)
	}

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

// RegisterJobHandler registers the handler for book processing jobs
func (s *Service) RegisterJobHandler(jobsSvc *jobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeBooksProcess, s.HandleJob)
		s.log.Info("registered book.process job handler")
	}
}