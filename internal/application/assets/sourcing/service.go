// Package sourcing — thin façade. Post P0-1 (June 2026) the god Service is
// split into 4 use case sub-packages (youtube/, batch/, drivesync/,
// localimport/). The façade holds one handle per sub-service and routes
// the legacy public methods (RegisterFromYouTube, BatchRegisterFromYouTube,
// SyncDriveFolder, LocalToDrive) to the corresponding sub-package service.
//
// P0-1 / commit 1 (this commit): only YouTube is extracted. BatchRegister
// loops through the YouTube sub-service (no new sub-package yet).
// SyncDriveFolder + LocalToDrive stay inline because their sub-package
// extractions are commits 3+4 of P0-1. The façade ctor has 4 args until
// commit 5 reduces it to 4 ALL-sub-service handles.
//
// Per AGENTS.md Pattern 8 (API package: thin transport only) the façade
// has no business logic; delegation is one line per method.
package sourcing

import (
	"context"
	"fmt"
	"strings"
)

// Service is the SourcingService façade. After P0-1 / commit 2 the ctor
// takes 5 args; the YouTube sub-service (commit 1) and the new Batch
// sub-service (this commit) are injected as interfaces, while jobs +
// scanner stay on the façade until commits 3-4 lift them to drivesync
// and localimport sub-packages. Commit 5 (P0-1 last) reduces the
// signature to all sub-services + log = 5 args.
type Service struct {
	// P0-1 / commit 1: YouTube sub-service is built externally by the
	// composition root and injected as a YouTubeRegistrar interface.
	youtube YouTubeRegistrar

	// P0-1 / commit 2: BatchRegistrar sub-service (this commit).
	batch BatchRegistrar

	// Shared ports consumed by the not-yet-extracted sub-cases. Will be
	// removed when Sync (commit 3) and LocalImport (commit 4) move to
	// their own sub-packages; until then the façade keeps them so the
	// SyncDriveFolder + LocalToDrive methods still work.
	jobs    JobsPort
	scanner FileScannerPort

	log Logger
}

// NewService creates a SourcingService façade. After commit 2 NewService
// takes 5 args: youtube + batch sub-services, JobsPort (for Sync/Local
// until commits 3-4 lift them), FileScannerPort (Local only), Logger.
func NewService(
	yt YouTubeRegistrar,
	batch BatchRegistrar,
	jobs JobsPort,
	scanner FileScannerPort,
	log Logger,
) *Service {
	return &Service{
		youtube: yt,
		batch:   batch,
		jobs:    jobs,
		scanner: scanner,
		log:     log,
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

// SyncDriveFolder enqueues a catalog sync job for the given Drive folder.
// Will move to internal/application/assets/sourcing/drivesync/Service in P0-1 /
// commit 3. Until then stays inline because no sub-package exists yet.
func (s *Service) SyncDriveFolder(ctx context.Context, cmd SyncDriveFolderCommand) (*SyncDriveFolderResult, error) {
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

	job, err := s.jobs.Enqueue(ctx, EnqueueRequest{
		Type:       "drive.folder.sync",
		MaxRetries: 2,
		Payload: JobPayload{
			"drive_folder_id": folderID,
			"source":          source,
			"name":            cmd.Name,
			"media_type":      mediaType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue sync job: %w", err)
	}

	return &SyncDriveFolderResult{
		OK: true, JobID: job.ID, DriveFolderID: folderID,
		Source: source, Name: cmd.Name,
		Message: "Drive folder sync dispatched. Poll GET /api/jobs/" + job.ID + " for status.",
	}, nil
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
