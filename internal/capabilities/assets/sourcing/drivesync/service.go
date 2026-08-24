// Package drivesync — the DriveFolderSynchronizer use case extracted from
// the historical sourcing.Service.SyncDriveFolder god method (P0-1 / commit 3,
// June 2026). Commit 5 normalised: log nil-guard dropped to match batch +
// historical god-method (panic on nil log; production composition site
// always wires zapSourcingLogger so this is fail-loud, fail-fast, not
// fail-silent).
//
// Per AGENTS.md Pattern 0 (port abstraction) + Pattern 5 (one concept per
// file): the DriveFolderSynchronizer owns the per-folder Drive catalog
// sync flow as a focused service with 2 narrow deps (JobsPort + Logger).
// The façade sourcing.Service.SyncDriveFolder delegates to
// *Service.Sync for API stability.
//
// Sub-package construction is *Service.NewService(jobs, log) — see
// internal/app/assets_register_sourcing.go for wiring. JobsPort is
// currently nil at the production composition site; preserving the
// historical fail-closed `jobs port not configured` error path lets test
// fixtures exercise the synchronous validation without enqueueing anything.
// When the composition root is upgraded to expose a real JobsPort
// (currently `*outbox.Dispatcher` covers the IndexDispatcher family but
// not the legacy Enqueue(ctx, EnqueueRequest) surface), the adapter is
// a one-field struct mapping EnqueueRequest → the outbox API.
package assets

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
)

// Service is the DriveFolderSynchronizer implementation. 2-port budget per
// architecture/policy.yaml::max_constructor_deps (well under the 8 cap).
type Service struct {
	jobs sourcing.JobsPort
	log  sourcing.Logger
}

// NewService creates a DriveFolderSynchronizer service. nil jobs is
// tolerated (matches historical fail-closed behaviour: returns the
// `jobs port not configured` typed error on Sync). log is REQUIRED
// (composition root always wires the zap-backed Logger; nil causes
// panic at first call site — fail-loud posture matches batch + the
// historical god-method, see commit 5 cleanup notes).
func NewService(jobs sourcing.JobsPort, log sourcing.Logger) *Service {
	return &Service{jobs: jobs, log: log}
}

// Sync enqueues a catalog sync job for the given Drive folder. Behaviour
// mirrors the historical sourcing.Service.SyncDriveFolder:
//
//   - empty DriveFolderID → `drive_folder_id is required` error.
//   - nil jobs port → `jobs port not configured` error (fail-closed).
//   - Source defaults to "drive" when empty; MediaType defaults to "clip".
//   - EnqueueRequest.Type = "drive.folder.sync", MaxRetries = 2.
//   - On success: SyncDriveFolderResult with OK:true, JobID, drive_folder_id,
//     source, name, and a hint to poll /api/jobs/<id> for status.
func (s *Service) Sync(ctx context.Context, cmd sourcing.SyncDriveFolderCommand) (*sourcing.SyncDriveFolderResult, error) {
	if s == nil {
		return nil, fmt.Errorf("drivesync.Sync: service is nil")
	}
	folderID := strings.TrimSpace(cmd.DriveFolderID)
	if folderID == "" {
		return nil, fmt.Errorf("drive_folder_id is required")
	}
	if s.jobs == nil {
		return nil, fmt.Errorf("jobs port not configured")
	}

	source := strings.TrimSpace(cmd.Source)
	if source == "" {
		source = "drive"
	}
	mediaType := strings.TrimSpace(cmd.MediaType)
	if mediaType == "" {
		mediaType = "clip"
	}

	s.log.Info("dispatching Drive folder sync",
		"folder_id", folderID, "source", source, "name", cmd.Name, "media_type", mediaType)

	job, err := s.jobs.Enqueue(ctx, sourcing.EnqueueRequest{
		Type:       "drive.folder.sync",
		MaxRetries: 2,
		Payload: sourcing.JobPayload{
			"drive_folder_id": folderID,
			"source":          source,
			"name":            cmd.Name,
			"media_type":      mediaType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue sync job: %w", err)
	}

	return &sourcing.SyncDriveFolderResult{
		OK:            true,
		JobID:         job.ID,
		DriveFolderID: folderID,
		Source:        source,
		Name:          cmd.Name,
		Message:       "Drive folder sync dispatched. Poll GET /api/jobs/" + job.ID + " for status.",
	}, nil
}
