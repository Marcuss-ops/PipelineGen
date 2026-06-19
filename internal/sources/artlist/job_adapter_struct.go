package artlist

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/jobs"
)

// JobAdapter gestisce l'integrazione tra il servizio Artlist e il sistema di job.
type JobAdapter struct {
	service *Service
}

// NewJobAdapter crea una nuova istanza di JobAdapter.
func NewJobAdapter(s *Service) *JobAdapter {
	return &JobAdapter{service: s}
}

// jobToResponse converts a jobs.Job to RunTagResponse using the codec.
func (a *JobAdapter) jobToResponse(j *job.Job) *RunTagResponse {
	if j == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "job not found"}
	}
	return (&JobCodec{}).ResponseFromJob(j)
}

// JobToRunTagResponse converts a jobs.Job to RunTagResponse using the codec.
func JobToRunTagResponse(j *job.Job) *RunTagResponse {
	return (&JobCodec{}).ResponseFromJob(j)
}
