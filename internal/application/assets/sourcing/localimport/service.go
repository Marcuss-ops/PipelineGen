// Package localimport — the LocalImporter use case extracted from the
// historical sourcing.Service.LocalToDrive god method (P0-1 / commit 4,
// June 2026).
//
// Per AGENTS.md Pattern 0 (port abstraction) + Pattern 5 (one concept per
// file): the LocalImporter owns the local-folder-to-Drive ingestion flow
// as a focused service with 3 narrow deps (JobsPort + FileScannerPort +
// Logger). The façade sourcing.Service.LocalToDrive delegates to
// *Service.Import for API stability.
//
// Behaviour flow (mirrors historical sourcing.Service.LocalToDrive):
//  1. Scanner <- Scan(LocalFolder, Limit) → []LocalFileInfo
//  2. group aggregation: count distinct GroupName, default "uncategorized"
//  3. log.Info the scan outcome
//  4. If DryRun: short-circuit return with OK:true, LocalFound, Groups (no
//     jobs port needed)
//  5. Jobs <- Enqueue("bulk_upload_youtube_clips", payload) → jobID
//  6. Return OK + JobID + LocalFound + Groups + message
//
// Sub-package construction is *Service.NewService(jobs, scanner, log) —
// see internal/app/assets_register_sourcing.go for wiring. Both JobsPort
// and FileScannerPort are nil today at the production composition site;
// nil-safe paths preserve historical behaviour:
//   - nil scanner → "file scanner not configured" (matches historical)
//   - nil jobs + DryRun path → proceeds without enqueue (jobs only needed
//     for the non-DryRun path)
//   - nil jobs + non-DryRun → "jobs port not configured" (matches historical)
package localimport

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
)

// Service is the LocalImporter implementation. 3-port budget per
// architecture/policy.yaml::max_constructor_deps (well under the 8 cap).
type Service struct {
	jobs    sourcing.JobsPort
	scanner sourcing.FileScannerPort
	log     sourcing.Logger
}

// NewService creates a LocalImporter service. jobs and scanner are both
// REQUIRED for non-DryRun invocations; nil-tolerant paths preserve the
// historical fail-closed error messages so test fixtures and the dry-run
// CLI path continue to behave the same.
func NewService(jobs sourcing.JobsPort, scanner sourcing.FileScannerPort, log sourcing.Logger) *Service {
	return &Service{jobs: jobs, scanner: scanner, log: log}
}

// Import scans a local folder and enqueues a bulk-upload Drive job for it.
// On DryRun=true the call short-circuits after the scan + group aggregation
// (no jobs port needed). Behaviour mirrors the historical
// sourcing.Service.LocalToDrive.
//
// Error order (matches historical god method validation order):
//  1. nil scanner → "file scanner not configured"
//  2. empty DriveFolderID → "drive_folder_id is required"
//  3. Scanner.Scan failure → wrapped as "scan folder: %w"
//  4. DryRun short-circuit returns OK
//  5. nil jobs (non-DryRun) → "jobs port not configured"
//  6. Enqueue failure → wrapped as "enqueue: %w"
//
// Default-coalesce behaviour:
//   - Source defaults to "youtube-local" when empty
//   - Concurrency defaults to 3 when <= 0
//   - FilePattern hardcoded to ["*.mp4"] (matches historical)
//   - Project always set to "media" on the Job
func (s *Service) Import(ctx context.Context, cmd sourcing.LocalToDriveCommand) (*sourcing.LocalToDriveResult, error) {
	if s == nil {
		return nil, fmt.Errorf("localimport.Import: service is nil")
	}
	if s.scanner == nil {
		return nil, fmt.Errorf("file scanner not configured")
	}
	if strings.TrimSpace(cmd.DriveFolderID) == "" {
		return nil, fmt.Errorf("drive_folder_id is required")
	}

	files, err := s.scanner.Scan(ctx, cmd.LocalFolder, cmd.Limit)
	if err != nil {
		return nil, fmt.Errorf("scan folder: %w", err)
	}

	groups := make(map[string]bool)
	for _, f := range files {
		g := f.GroupName
		if g == "" {
			g = "uncategorized"
		}
		groups[g] = true
	}
	groupNames := make([]string, 0, len(groups))
	for g := range groups {
		groupNames = append(groupNames, g)
	}

	if s.log != nil {
		s.log.Info("scanned local folder", "files", len(files), "groups", len(groups), "dry_run", cmd.DryRun)
	}

	if cmd.DryRun {
		return &sourcing.LocalToDriveResult{
			OK: true, DryRun: true, LocalFound: len(files), Groups: groupNames,
		}, nil
	}

	if s.jobs == nil {
		return nil, fmt.Errorf("jobs port not configured")
	}

	source := cmd.Source
	if source == "" {
		source = "youtube-local"
	}
	conc := cmd.Concurrency
	if conc <= 0 {
		conc = 3
	}

	job, err := s.jobs.Enqueue(ctx, sourcing.EnqueueRequest{
		Type:    "bulk_upload_youtube_clips",
		Project: "media",
		Payload: sourcing.JobPayload{
			"local_folder":           cmd.LocalFolder,
			"drive_folder_id":        strings.TrimSpace(cmd.DriveFolderID),
			"source":                 source,
			"subdir_as_drive_subdir": true,
			"recursive":              true,
			"concurrency":            conc,
			"limit":                  cmd.Limit,
			"file_patterns":          []string{"*.mp4"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue: %w", err)
	}

	return &sourcing.LocalToDriveResult{
		OK:         true,
		JobID:      job.ID,
		Message:    fmt.Sprintf("job enqueued (%d files, %d groups)", len(files), len(groups)),
		LocalFound: len(files),
		Groups:     groupNames,
	}, nil
}
