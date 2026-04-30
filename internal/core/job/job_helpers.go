package job

import (
	"context"
	"fmt"
	"time"

	"velox/go-master/pkg/models"
)

func (s *Service) workerCanHandleJob(capabilities []models.WorkerCapability, jobType models.JobType) bool {
	required := s.getRequiredCapability(jobType)
	if required == "" {
		return true
	}

	for _, cap := range capabilities {
		if cap == required {
			return true
		}
	}
	return false
}

func (s *Service) getRequiredCapability(jobType models.JobType) models.WorkerCapability {
	switch jobType {
	case models.TypeVideoGeneration:
		return models.CapVideoGeneration
	case models.TypeVoiceover:
		return models.CapTTS
	case models.TypeScriptGen:
		return models.CapVideoGeneration
	case models.TypeStockDownload:
		return models.CapStockDownload
	case models.TypeUpload:
		return models.CapUpload
	default:
		return ""
	}
}

var validStatusTransitions = map[models.JobStatus][]models.JobStatus{
	models.StatusPending:   {models.StatusQueued, models.StatusCancelled},
	models.StatusQueued:    {models.StatusRunning, models.StatusCancelled},
	models.StatusRunning:   {models.StatusCompleted, models.StatusFailed, models.StatusZombie, models.StatusCancelled},
	models.StatusZombie:    {models.StatusQueued, models.StatusFailed, models.StatusCancelled},
	models.StatusFailed:    {models.StatusQueued, models.StatusCancelled},
	models.StatusCompleted: {},
	models.StatusCancelled: {},
	models.StatusRetrying:  {models.StatusQueued, models.StatusCancelled},
}

func (s *Service) isValidStatusTransition(from, to models.JobStatus) bool {
	allowed, exists := validStatusTransitions[from]
	if !exists {
		return false
	}

	for _, status := range allowed {
		if status == to {
			return true
		}
	}
	return false
}

func (s *Service) logEvent(ctx context.Context, jobID, eventType, message string) {
	event := &models.JobEvent{
		ID:        fmt.Sprintf("%s-%d", jobID, time.Now().UnixNano()),
		JobID:     jobID,
		Type:      eventType,
		Message:   message,
		Timestamp: time.Now(),
	}
	s.storage.LogJobEvent(ctx, event)
}
