// Package sourcing — cross-use-case contract types.
//
// Per AGENTS.md Pattern 0 (port abstraction, June 2026): the SourcingService
// is split into 4 use cases (YouTubeRegistrar in step 1 / commit 1;
// BatchRegistrar / DriveFolderSynchronizer / LocalImporter in the future
// commits of P0-1). Each use case lives in its own sub-package under
// internal/application/assets/sourcing/<use>/.
//
// This file declares the per-use-case INTERFACES the façade routes to.
// Defining them in the parent sourcing package (rather than in the
// sub-package) breaks the import cycle that would otherwise arise from
// the façade NewService taking a YouTubeRegistrar parameter: with the
// interface in the same Go package as the ctor, sourcing never imports
// youtube directly.
//
// The façade (internal/application/assets/sourcing/service.go::Service)
// holds one of each typed interface and delegates the legacy public
// methods (RegisterFromYouTube, BatchRegisterFromYouTube, SyncDriveFolder,
// LocalToDrive) to the corresponding implementation. The composition root
// (internal/app/assets_register_sourcing.go::newAssetRegisterService) wires
// each sub-package's service into the façade on construction.
package assets

import "context"

// YouTubeRegistrar is the per-YouTube-clip orchestrator extracted from the
// historical sourcing.Service.RegisterFromYouTube (P0-1 / commit 1).
// Implemented by internal/application/assets/sourcing/youtube.Service.
type YouTubeRegistrar interface {
	Register(ctx context.Context, cmd RegisterClipCommand) (*RegisterClipResult, error)
}

// BatchRegistrar is the per-batch orchestrator (planned: P0-1 / commit 2).
// Will be extracted into internal/application/assets/sourcing/batch/Service.
type BatchRegistrar interface {
	BatchRegister(ctx context.Context, commands []RegisterClipCommand) *BatchRegisterResult
}

// DriveFolderSynchronizer is the per-folder-sync orchestrator (planned:
// P0-1 / commit 3). Will be extracted into
// internal/application/assets/sourcing/drivesync/Service.
type DriveFolderSynchronizer interface {
	Sync(ctx context.Context, cmd SyncDriveFolderCommand) (*SyncDriveFolderResult, error)
}

// LocalImporter is the per-local-folder orchestrator (planned: P0-1 /
// commit 4). Will be extracted into
// internal/application/assets/sourcing/localimport/Service.
type LocalImporter interface {
	Import(ctx context.Context, cmd LocalToDriveCommand) (*LocalToDriveResult, error)
}
