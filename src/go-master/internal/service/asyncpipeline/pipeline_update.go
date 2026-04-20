package asyncpipeline

func (s *AsyncPipelineService) updateJob(jobID string, updateFn func(*PipelineJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[jobID]
	if !exists {
		return
	}

	updateFn(job)
	s.saveJob(job)
}

// saveJob salva un job su disco
