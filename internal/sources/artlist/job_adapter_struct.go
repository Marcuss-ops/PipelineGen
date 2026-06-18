package artlist

import (
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
)

// JobAdapter gestisce l'integrazione tra il servizio Artlist e il sistema di job.
type JobAdapter struct {
	service *Service
}

// NewJobAdapter crea una nuova istanza di JobAdapter.
func NewJobAdapter(s *Service) *JobAdapter {
	return &JobAdapter{service: s}
}

// jobToResponse converts a domain job.Job to RunTagResponse using the codec.
func (a *JobAdapter) jobToResponse(job *domainjob.Job) *RunTagResponse {
	if job == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "job not found"}
	}
	return jobCodec.ResponseFromJob(job)
}

// JobToRunTagResponse converts a domain job.Job to RunTagResponse using the codec.
func JobToRunTagResponse(job *domainjob.Job) *RunTagResponse {
	return jobCodec.ResponseFromJob(job)
}
