package voiceover

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"

	"go.uber.org/zap"
)

// HandleJob processes a voiceover.generate job from the queue.
//
// PR 3 (June 2026): voiceover.batch and voiceover.promo have been
// removed from the registry. All voiceover generation now flows
// through a single typed job type with a domain.GenerateVoiceoverCommand
// payload — no more PayloadMap() indirection.
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("processing voiceover job",
		zap.String("job_id", job.ID),
		zap.String("type", job.Type))

	var cmd domain.GenerateVoiceoverCommand
	if err := json.Unmarshal(job.Payload, &cmd); err != nil {
		return nil, fmt.Errorf("voiceover.generate: failed to unmarshal payload: %w", err)
	}

	if err := cmd.Validate(); err != nil {
		return nil, fmt.Errorf("voiceover.generate: invalid command: %w", err)
	}

	uc := s.UseCase()
	if uc == nil {
		return nil, fmt.Errorf("voiceover.generate: use case not initialised")
	}

	result, err := uc.Execute(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("voiceover.generate: execution failed: %w", err)
	}

	// Serialise the typed result for the job store.
	resultJSON, _ := json.Marshal(result)
	var resultMap map[string]any
	_ = json.Unmarshal(resultJSON, &resultMap)

	return resultMap, nil
}
