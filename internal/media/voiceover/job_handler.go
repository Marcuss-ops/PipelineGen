package voiceover

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
)

// HandleJob processes a voiceover job from the queue.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("processing voiceover batch job",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)))

	var req BatchRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	// Generate batch voiceover
	resp, err := s.GenerateBatch(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("failed to generate batch voiceover: %w", err)
	}

	// Return response as result
	resultJSON, _ := json.Marshal(resp)
	var result map[string]interface{}
	json.Unmarshal(resultJSON, &result)

	if !resp.OK {
		return result, fmt.Errorf("some batch items failed")
	}

	return result, nil
}
