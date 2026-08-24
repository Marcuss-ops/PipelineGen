package clips

import (
	"context"
	"fmt"
	"strings"

	jobsystem "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Cleanup orchestrates orphan-record cleanup.
//
// S1b (June 2026): removed the synchronous 10000-record inline
// pagination path. Cleanup always enqueues a job through
// JobsServicePort — the worker does the actual orphan scan,
// Drive-files.Get, and physical delete from the broker pool.
//
// Wave 22 PR-5 polish (June 2026): Cleanup returns the typed
// sentinel ErrJobsUnavailable when s.jobs is nil (test fixtures
// or partial deployments) so the api handler can map it to 503
// via errors.Is.
func (s *ClipOpsService) Cleanup(ctx context.Context, in CleanupInput) (*CleanupReport, error) {
	src := strings.ToLower(strings.TrimSpace(in.Source))
	if !s.isKnownCleanupSource(src) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidSource, in.Source)
	}
	if s.jobs == nil {
		return nil, ErrJobsUnavailable
	}

	deep := in.Deep
	report := &CleanupReport{
		OK:         true,
		Source:     src,
		DryRun:     in.DryRun,
		CheckDrive: in.CheckDrive,
		Items:      []CleanupItem{},
	}

	activeKey := "system_maintenance_manual"
	if in.DryRun {
		activeKey += "_dry"
	}
	if deep {
		activeKey += "_deep"
	}

	job, err := s.jobs.Enqueue(ctx, JobsEnqueueRequest{
		Type: jobsystem.TypeSystemCleanup,
		Payload: map[string]any{
			"deep":        deep,
			"dry_run":     in.DryRun,
			"check_drive": in.CheckDrive,
			"source":      src,
		},
		Priority:  10,
		ActiveKey: activeKey,
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue cleanup job: %w", err)
	}
	report.JobID = job.ID
	report.Message = fmt.Sprintf("system cleanup job enqueued; poll job_id=%s for results", job.ID)
	report.Summary = fmt.Sprintf("enqueued (job_id=%s)", job.ID)
	report.Items = []CleanupItem{}
	return report, nil
}
