package artlist

import (
	"velox/go-master/internal/media/models"
)

// JobAdapter gestisce l'integrazione tra il servizio Artlist e il sistema di job.
type JobAdapter struct {
	service *Service
}

// NewJobAdapter crea una nuova istanza di JobAdapter.
func NewJobAdapter(s *Service) *JobAdapter {
	return &JobAdapter{service: s}
}

// jobToResponse converts a models.Job to RunTagResponse using the codec.
func (a *JobAdapter) jobToResponse(job *models.Job) *RunTagResponse {
	if job == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "job not found"}
	}
	return jobCodec.ResponseFromJob(job)
}

// JobToRunTagResponse converts a models.Job to RunTagResponse using the codec.
func JobToRunTagResponse(job *models.Job) *RunTagResponse {
	return jobCodec.ResponseFromJob(job)
}
