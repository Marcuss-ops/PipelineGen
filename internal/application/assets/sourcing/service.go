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

// Service is the SourcingService façade. After P0-1 / commit 1 the ctor
// takes 4 args (was 14 historically); by commit 5 it takes 4 SUB-SERVICE
// handles + Log = 5 args total. The internal struct mirrors the ctor.
type Service struct {
	// P0-1 / commit 1: YouTube sub-service is built externally by the
	// composition root and injected as a YouTubeRegistrar interface.
	youtube YouTubeRegistrar

	// Shared ports consumed by the not-yet-extracted sub-cases. Will be
	// removed when Sync (commit 3) and LocalImport (commit 4) move to
	// their own sub-packages; until then the façade keeps them so the
	// SyncDriveFolder + LocalToDrive methods still work.
	jobs    JobsPort
	scanner FileScannerPort

	log Logger
}

// NewService creates a SourcingService façade. Until P0-1 fully lands
// (commits 2-4) NewService takes 4 args: the YouTubeRegistrar
// sub-service, the JobsPort (shared by Sync/Local), the FileScannerPort
// (Local only), and the Logger. Commit 5 (P0-1 last) reduces the
// signature to (youtube, batch, drivesync, local, log) — i.e. 5 args
// where each arg is a sub-service interface.
func NewService(
	yt YouTubeRegistrar,
	jobs JobsPort,
	scanner FileScannerPort,
	log Logger,
) *Service {
	return &Service{
		youtube: yt,
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
// commands sequentially, delegating each to the YouTube sub-package.
// Behavior matches the historical BatchRegisterFromYouTube. Sub-package
// extraction (P0-1 / commit 2) will lift this into a focused
// batch.Service that wraps the YouTubeRegistrar.
func (s *Service) BatchRegisterFromYouTube(ctx context.Context, commands []RegisterClipCommand) *BatchRegisterResult {
	if s == nil || s.youtube == nil {
		return &BatchRegisterResult{
			OK:      false,
			Total:   len(commands),
			Failed:  len(commands),
			Results: make([]BatchClipResult, len(commands)),
		}
	}

	log := s.log
	results := make([]BatchClipResult, len(commands))
	var succeeded, failed int

	log.Info("starting batch registration", "service", "sourcing", "clips", len(commands))
	for i, cmd := range commands {
		res, err := s.youtube.Register(ctx, cmd)
		br := BatchClipResult{Name: cmd.Name}
		if err != nil {
			br.Error = err.Error()
			results[i] = br
			failed++
			log.Info("batch clip processed",
				"index", i+1,
				"total", len(commands),
				"name", cmd.Name,
				"ok", false,
				"error", err.Error(),
			)
			continue
		}
		if res == nil {
			br.Error = "empty registration result"
			results[i] = br
			failed++
			continue
		}
		br.OK = res.OK
		br.ClipID = res.ClipID
		br.Duplicate = res.Duplicate
		if res.Duplicate {
			br.OK = false
		}
		if !res.OK && res.Message != "" {
			br.Error = res.Message
		}
		results[i] = br
		if br.OK || br.Duplicate {
			succeeded++
		} else {
			failed++
		}
		log.Info("batch clip processed",
			"index", i+1,
			"total", len(commands),
			"name", cmd.Name,
			"ok", br.OK,
			"duplicate", br.Duplicate,
			"error", br.Error,
		)
	}

	log.Info("batch registration completed", "service", "sourcing", "succeeded", succeeded, "failed", failed)
	return &BatchRegisterResult{
		OK:        true,
		Total:     len(commands),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	}
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
