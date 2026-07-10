package voiceover

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	promo "github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
	"go.uber.org/zap"
)

// HandleJob processes a voiceover job from the queue.
// It dispatches to the correct method based on the job type:
//   - voiceover.batch → GenerateBatch
//   - voiceover.promo → GeneratePromo
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("processing voiceover job",
		zap.String("job_id", job.ID),
		zap.String("type", job.Type))

	switch job.Type {
	case appjobs.TypeVoiceoverPromo:
		return s.handlePromoJob(ctx, job)
	default:
		return s.handleBatchJob(ctx, job)
	}
}

func (s *Service) handleBatchJob(ctx context.Context, job *appjobs.Job) (map[string]any, error) {
	var req BatchRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal batch payload: %w", err)
	}

	resp, err := s.GenerateBatch(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("failed to generate batch voiceover: %w", err)
	}

	resultJSON, _ := json.Marshal(resp)
	var result map[string]any
	json.Unmarshal(resultJSON, &result)

	if !resp.OK {
		return result, fmt.Errorf("some batch items failed")
	}

	return result, nil
}

func (s *Service) handlePromoJob(ctx context.Context, job *appjobs.Job) (map[string]any, error) {
	// PR-VOICEOVER-ALIASES-RETIRE Sub-PR B (July 2026): the legacy
	// voiceover.PromoRequest alias was removed; the canonical surface
	// is now promo.Request (owned by internal/application/
	// workflow/promo). Import alias `promo` mirrors promo.go's
	// natural Go-idiomatic convention for the same package.
	var req promo.Request
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal promo payload: %w", err)
	}

	resp, err := s.GeneratePromo(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("failed to generate promo voiceover: %w", err)
	}

	resultJSON, _ := json.Marshal(resp)
	var result map[string]any
	json.Unmarshal(resultJSON, &result)

	if !resp.OK {
		return result, fmt.Errorf("some promo items failed")
	}

	return result, nil
}
