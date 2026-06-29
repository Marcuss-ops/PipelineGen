// Package sourcing — thin façade. Post P0-1 (June 2026) the god Service is
// split into 4 use case sub-packages (youtube/, batch/, drivesync/,
// localimport/). The façade holds one handle per sub-service and routes
// the legacy public methods (RegisterFromYouTube, BatchRegisterFromYouTube,
// SyncDriveFolder, LocalToDrive) to the corresponding sub-package service.
//
// P0-1 / commit 3 (this commit): DriveFolderSynchronizer extracted
// (SyncDriveFolder now delegates). The façade's `jobs JobsPort` field
// is gone — SyncDriveFolder consumed it inline before this commit and
// now the dep lives on the drivesync sub-service. LocalToDrive still
// consumes both `jobs` and `scanner`; commit 4 lifts them into the
// localimport sub-package, after which commit 5 collapses the ctor to
// 4 sub-service handles + log (5 args).
//
// Per AGENTS.md Pattern 8 (API package: thin transport only) the façade
// has no business logic; delegation is one line per method.
package sourcing

import (
	"context"
	"fmt"
	"strings"
)

// Service is the SourcingService façade. After P0-1 / commit 3 the ctor
// takes 6 args: youtube + batch + drivesync sub-services, JobsPort +
// FileScannerPort (still on the façade because LocalToDrive consumes
// them inline until commit 4 lifts them to localimport), Logger.
type Service struct {
	// P0-1 / commit 1: YouTube sub-service is built externally by the
	// composition root and injected as a YouTubeRegistrar interface.
	youtube YouTubeRegistrar

	// P0-1 / commit 2: BatchRegistrar sub-service.
	batch BatchRegistrar

	// P0-1 / commit 3 (this commit): DriveFolderSynchronizer sub-service.
	drivesync DriveFolderSynchronizer

	// Shared ports consumed by the not-yet-extracted LocalToDrive
	// sub-case. Will be removed when LocalImport (commit 4) moves to
	// its own sub-package.
	jobs    JobsPort
	scanner FileScannerPort

	log Logger
}

// NewService creates a SourcingService façade. After commit 3 NewService
// takes 6 args: youtube/batch/drivesync sub-services, JobsPort + FileScannerPort
// (still needed by the inline LocalToDrive method until commit 4 lifts them),
// Logger.
func NewService(
	yt YouTubeRegistrar,
	batch BatchRegistrar,
	drivesync DriveFolderSynchronizer,
	jobs JobsPort,
	scanner FileScannerPort,
	log Logger,
) *Service {
	return &Service{
		youtube:   yt,
		batch:     batch,
		drivesync: drivesync,
		jobs:      jobs,
		scanner:   scanner,
		log:       log,
	}
}

// RegisterFromYouTube delegates to the YouTube sub-package service.
// The legacy method body has moved to
// internal/application/assets/sourcing/youtube/service.go::Service.Register.
// Behavior is identical — the façade only changes the lookup direction.
func (s *Service) RegisterFromYouTube(ctx context.Context, cmd RegisterClipCommand) (*RegisterClipResult, error) {
	if s == nil || s.youtube == nil {
		return nil, fmt.Errorf("sourcing.RegisterFromYouTube: youtube registrar not wired (compose-time bug — check newAssetRegisterService)")
	}
	return s.youtube.Register(ctx, cmd)
}

// BatchRegisterFromYouTube processes a batch of clip registration
// commands sequentially, delegating to the batch sub-package service
// (P0-1 / commit 2). The legacy inline loop has moved to
// internal/application/assets/sourcing/batch/service.go::Service.BatchRegister.
func (s *Service) BatchRegisterFromYouTube(ctx context.Context, commands []RegisterClipCommand) *BatchRegisterResult {
	if s == nil || s.batch == nil {
		return &BatchRegisterResult{
			OK:      false,
			Total:   len(commands),
			Failed:  len(commands),
			Results: make([]BatchClipResult, len(commands)),
		}
	}
	return s.batch.BatchRegister(ctx, commands)
}

// SyncDriveFolder delegates to the drivesync sub-package service.
// The legacy method body has moved to
// internal/application/assets/sourcing/drivesync/service.go::Service.Sync.
// Behavior is identical — the façade only changes the lookup direction.
// Nil-svc guard preserved at the façade boundary so test fixtures that
// construct sourcing.NewService with a nil drvSvc continue to surface
// the error as `drive_folder_id is required` (drivesync.Sync's own
// first-line validation order matches the historical god method).
func (s *Service) SyncDriveFolder(ctx context.Context, cmd SyncDriveFolderCommand) (*SyncDriveFolderResult, error) {
	if s == nil || s.drivesync == nil {
		return nil, fmt.Errorf("sourcing.SyncDriveFolder: drivesync registrar not wired (compose-time bug — check newAssetRegisterService)")
	}
	return s.drivesync.Sync(ctx, cmd)
}

// LocalToDrive scans a local folder and enqueues a bulk upload job.
// Will move to internal/application/assets/sourcing/localimport/Service in
// P0-1 / commit 4. Until then stays inline because no sub-package exists yet.
func (s *Service) LocalToDrive(ctx context.Context, cmd LocalToDriveCommand) (*LocalToDriveResult, error) {
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

	s.log.Info("scanned local folder", "files", len(files), "groups", len(groups), "dry_run", cmd.DryRun)

	if cmd.DryRun {
		return &LocalToDriveResult{
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

	job, err := s.jobs.Enqueue(ctx, EnqueueRequest{
		Type:    "bulk_upload_youtube_clips",
		Project: "media",
		Payload: JobPayload{
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

	return &LocalToDriveResult{
		OK: true, JobID: job.ID,
		Message:    fmt.Sprintf("job enqueued (%d files, %d groups)", len(files), len(groups)),
		LocalFound: len(files), Groups: groupNames,
	}, nil
}

// Compile-time assertion: the YouTube sub-package's Service must satisfy
// the façade-level YouTubeRegistrar interface. Live in the composition
// root (internal/app/assets_register_sourcing.go) where both packages can
// be transitively imported without creating a cycle. See Go-1 import
// cycle rule: the sourcing package itself cannot import youtube without
// pulling sourcing back through youtube's transitive import of sourcing
// (the cycle breaks via the YouTubeRegistrar interface declared in this
// package's contract.go).
//
// (no assertion here — see internal/app/assets_register_sourcing.go for the
// composition-time assertion that catches drift between *youtube.Service.Register
// and YouTubeRegistrar.Register before the wire.)
