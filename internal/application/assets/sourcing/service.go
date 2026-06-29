// Package sourcing — thin façade. Post P0-1 (June 2026) the god Service is
// split into 4 use case sub-packages (youtube/, batch/, drivesync/,
// localimport/). The façade holds one handle per sub-service and routes
// the legacy public methods (RegisterFromYouTube, BatchRegisterFromYouTube,
// SyncDriveFolder, LocalToDrive) to the corresponding sub-package service.
//
// P0-1 / commit 4 (this commit): LocalImporter extracted (LocalToDrive now
// delegates). Shared `jobs JobsPort` + `scanner FileScannerPort` fields
// remain on the façade as proxy for the localimport sub-service's deps
// (commit 5 lifts them to the composition root for a 5-arg façade ctor
// with all sub-services + log).
//
// Per AGENTS.md Pattern 8 (API package: thin transport only) the façade
// has no business logic; delegation is one line per method.
package sourcing

import (
	"context"
	"fmt"
)

// Service is the SourcingService façade. After P0-1 / commit 4 the ctor
// takes 7 args: 4 sub-services + JobsPort + FileScannerPort (proxy for
// localimport) + Logger. Commit 5 collapses to 5 args (no jobs/scanner
// in the façade; composition root injects them directly into
// localimport.NewService).
type Service struct {
	// P0-1 / commit 1: YouTube sub-service.
	youtube YouTubeRegistrar

	// P0-1 / commit 2: BatchRegistrar sub-service.
	batch BatchRegistrar

	// P0-1 / commit 3: DriveFolderSynchronizer sub-service.
	drivesync DriveFolderSynchronizer

	// P0-1 / commit 4 (this commit): LocalImporter sub-service.
	localimport LocalImporter

	// Proxy ports for localimport's deps (jobs + scanner). Commit 5
	// drops these from the façade; jobs/scanner move to the
	// composition root where they're injected directly into
	// localimport.NewService.
	jobs    JobsPort
	scanner FileScannerPort

	log Logger
}

// NewService creates a SourcingService façade. After commit 4 NewService
// takes 7 args: youtube + batch + drivesync + localimport sub-services,
// JobsPort + FileScannerPort (still needed as proxy until commit 5),
// Logger.
func NewService(
	yt YouTubeRegistrar,
	batch BatchRegistrar,
	drivesync DriveFolderSynchronizer,
	localimport LocalImporter,
	jobs JobsPort,
	scanner FileScannerPort,
	log Logger,
) *Service {
	return &Service{
		youtube:    yt,
		batch:      batch,
		drivesync:  drivesync,
		localimport: localimport,
		jobs:       jobs,
		scanner:    scanner,
		log:        log,
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

// LocalToDrive delegates to the localimport sub-package service.
// The legacy method body has moved to
// internal/application/assets/sourcing/localimport/service.go::Service.Import.
// Behavior is identical — the façade only changes the lookup direction.
// Nil-svc guard at the façade boundary so test fixtures with a nil
// localimport continue to surface the error message consistently with
// the other 3 delegated methods.
func (s *Service) LocalToDrive(ctx context.Context, cmd LocalToDriveCommand) (*LocalToDriveResult, error) {
	if s == nil || s.localimport == nil {
		return nil, fmt.Errorf("sourcing.LocalToDrive: localimport registrar not wired (compose-time bug — check newAssetRegisterService)")
	}
	return s.localimport.Import(ctx, cmd)
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
