package artlist

import (
	"context"

	"velox/go-master/pkg/models"
)

// GetRunTag returns the tracked status for a background artlist run.
// This is the only runtime function kept from the old run management system.
func (s *Service) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
	job, err := s.GetJobByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	return jobToResponse(job), nil
}

// getIntFromResult extracts an int from a result map, handling both int and float64 types
func getIntFromResult(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	default:
		return 0
	}
}

// jobToResponse converts a models.Job to RunTagResponse using the codec.
// This is kept as a wrapper for backwards compatibility.
func jobToResponse(job *models.Job) *RunTagResponse {
	if job == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "job not found"}
	}
	return jobCodec.ResponseFromJob(job)
}

// JobToRunTagResponse converts a models.Job to RunTagResponse using the codec.
// This is the public wrapper used by API handlers.
func JobToRunTagResponse(job *models.Job) *RunTagResponse {
	return jobCodec.ResponseFromJob(job)
}
