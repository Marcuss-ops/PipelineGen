package lessons

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// HandleJob processes the background job for lesson generation.
// Unmarshals the LessonRequest, calls GenerateLessonWithProgress with
// the job tools for live progress updates, and returns structured results.
func (s *Service) HandleJob(ctx context.Context, job *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("handling lessons.process job", zap.String("job_id", job.ID))

	var req LessonRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(5, "Starting lesson generation")
	}

	// Use GenerateLessonWithProgress for real-time progress tracking.
	// The Go service emits progress callbacks that we forward to the job system.
	result, err := s.GenerateLessonWithProgress(ctx, &req, func(pct int, msg string) {
		if tools.Progress != nil {
			tools.Progress(pct, msg)
		}
	})
	if err != nil {
		s.log.Error("lesson generation failed", zap.Error(err))
		return nil, fmt.Errorf("lesson generation failed: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("lesson generation failed: %s", result.Error)
	}

	if tools.Progress != nil {
		tools.Progress(100, "Lesson generation completed")
	}

	// Convert chapters to a serializable format
	chapters := make([]map[string]any, 0, len(result.Chapters))
	for _, ch := range result.Chapters {
		chMap := map[string]any{
			"index":      ch.Index,
			"title":      ch.Title,
			"word_count": ch.WordCount,
			"error":      ch.Error,
		}
		if ch.Image != nil {
			chMap["image"] = map[string]any{
				"hash":          ch.Image.Hash,
				"url":           ch.Image.URL,
				"drive_link":    ch.Image.DriveLink,
				"drive_file_id": ch.Image.DriveFileID,
			}
		}
		chapters = append(chapters, chMap)
	}

	return map[string]any{
		"success":       true,
		"title":         result.Title,
		"language":      result.Language,
		"chapters":      chapters,
		"total_words":   result.TotalWords,
		"markdown_path": result.MarkdownPath,
		"pdf_path":      result.PDFPath,
		"generated_at":  result.GeneratedAt,
	}, nil
}
