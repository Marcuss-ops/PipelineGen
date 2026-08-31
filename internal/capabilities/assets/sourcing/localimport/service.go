// Package localimport — the LocalImporter use case.
//
// PR-CLIPS-ENQUEUE-ONLY (July 2026): the LocalImporter is now a pure
// enqueue wrapper. It validates the minimal payload, enqueues a
// bulk_upload_youtube_clips job, and returns the job_id. The worker
// (internal/capabilities/clips/bulk_upload_worker.go) is the SOLE owner
// of filesystem scanning — no pre-scan happens here.
//
// Per AGENTS.md Pattern 0 (port abstraction) + Pattern 5 (one concept per
// file): the LocalImporter owns the enqueue flow as a focused service
// with 2 narrow deps (JobsPort + Logger).
package localimport

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
)

// Service is the LocalImporter implementation. 2-port budget per
// architecture/policy.yaml::max_constructor_deps (well under the 8 cap).
type Service struct {
	jobs sourcing.JobsPort
	log  sourcing.Logger
}

// NewService creates a LocalImporter service. jobs is REQUIRED for
// any invocation; nil returns "jobs port not configured" at call time.
func NewService(jobs sourcing.JobsPort, log sourcing.Logger) *Service {
	return &Service{jobs: jobs, log: log}
}

// Import enqueues a bulk-upload Drive job for the given local folder.
// On success the returned result carries the job_id so the caller
// (the HTTP handler) can return 202 Accepted with the id.
//
// PR-CLIPS-ENQUEUE-ONLY (July 2026): the call short-circuits after
// validation + enqueue. No filesystem scan, no DryRun. The worker
// emits the scan results when the job runs.
//
// Error order:
//  1. nil service → "service is nil"
//  2. empty LocalFolder → "local_folder is required"
//  3. empty DriveFolderID → "drive_folder_id is required"
//  4. nil jobs → "jobs port not configured"
//  5. Enqueue failure → wrapped as "enqueue: %w"
//
// Default-coalesce behaviour:
//   - Source defaults to "youtube-local" when empty
//   - Category is optional (empty = no category override)
//   - Recursive + Concurrency are optional wire-shape overrides;
//     the worker uses its server-config defaults when caller omits them.
func (s *Service) Import(ctx context.Context, cmd sourcing.LocalToDriveCommand) (*sourcing.LocalToDriveResult, error) {
	if s == nil {
		return nil, fmt.Errorf("localimport.Import: service is nil")
	}
	if strings.TrimSpace(cmd.LocalFolder) == "" {
		return nil, fmt.Errorf("local_folder is required")
	}
	if strings.TrimSpace(cmd.DriveFolderID) == "" {
		return nil, fmt.Errorf("drive_folder_id is required")
	}
	if s.jobs == nil {
		return nil, fmt.Errorf("jobs port not configured")
	}

	source := cmd.Source
	if source == "" {
		source = "youtube-local"
	}

	s.log.Info("bulk-upload-youtube-clips: enqueue",
		"local_folder", cmd.LocalFolder,
		"drive_folder_id", cmd.DriveFolderID,
		"source", source,
		"category", cmd.Category,
		"recursive", cmd.Recursive,
		"concurrency", cmd.Concurrency)

	job, err := s.jobs.Enqueue(ctx, sourcing.EnqueueRequest{
		Type:    "bulk_upload_youtube_clips",
		Project: "media",
		Payload: sourcing.JobPayload{
			"local_folder":    cmd.LocalFolder,
			"drive_folder_id": strings.TrimSpace(cmd.DriveFolderID),
			"source":          source,
			"category":        cmd.Category,
			"recursive":       cmd.Recursive,
			"concurrency":     cmd.Concurrency,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue: %w", err)
	}

	return &sourcing.LocalToDriveResult{
		OK:      true,
		JobID:   job.ID,
		Message: "Job enqueued.",
	}, nil
}
