// Package app — compile-time drift assertions for the sourcing adapter
// stack. Individual adapter types were split into per-capability files:
//
//   - youtube_dispatcher_adapter.go (ytadapters.YoutubeIndexDispatcherAdapter)
//   - youtube_enrichment_adapter.go (ytadapters.YoutubeEnrichmentAdapter)
//   - youtube_fetch_adapter.go (ytadapters.SourcingFetchAdapter)
//   - youtube_drive_legacy_adapter.go (sourcingDriveAdapter + ytadapters.SourcingClipStoreAdapter)
//   - youtube_metadata_adapter.go (ytadapters.SourcingMetadataAdapter + ytadapters.SourcingEnrichmentAdapter +
//     ytadapters.SourcingConfigAdapter + ytadapters.SourcingTranscriberAdapter + ytadapters.SourcingSearchAdapter +
//     sourcingHashAdapter + ytadapters.ZapSourcingLogger)
//   - youtube_publisher_adapter.go (ytadapters.SourcingPublisherAdapter + ytadapters.SourcingDispatcherAdapter)
//   - youtube_asset_mapper.go (fromExistingClip + toExistingClip)
//
// Split extracted in PR-GODOBJ-8 (July 2026) per the god-object
// decomposition plan.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/drivesync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/localimport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
)

// newAssetRegisterService builds the SourcingService façade. After P0-1 /
// commit 1 (June 2026) it first constructs the YouTubeRegistrar sub-service
// (with v2 adapters that wrap legacy ports) and then injects that, plus the
// remaining JobsPort/FileScannerPort needed by SyncDriveFolder + LocalToDrive
// (not yet extracted, planned in commits 3 and 4 of P0-1), into the slim
// sourcing.NewService ctor (now 4 args, was 14 historically).

var (
	_ youtube.IndexDispatcherPort = (*ytadapters.YoutubeIndexDispatcherAdapter)(nil)
	_ youtube.EnrichmentPort      = (*ytadapters.YoutubeEnrichmentAdapter)(nil)
	// Drift guard: youtube.Service implements sourcing.YouTubeRegistrar.
	// This assertion lives at the composition root (rather than in
	// sourcing/service.go) because the latter would re-introduce the
	// import cycle that P0-1 / commit 1 broke (sourcing imports youtube
	// for the (*youtube.Service) reference; youtube imports sourcing
	// for shared types like RegisterClipCommand — cycle).
	_ sourcing.YouTubeRegistrar = (*youtube.Service)(nil)
	// P0-1 / commit 2: batch.Service implements sourcing.BatchRegistrar.
	// Same drift-guard rationale as the YouTube assertion above; the
	// composition root can transitively import both sourcing and batch
	// without re-introducing the cycle (batch is a sub-package of
	// sourcing; sourcing does not import batch).
	_ sourcing.BatchRegistrar = (*batch.Service)(nil)
	// P0-1 / commit 3 (this commit): drivesync.Service implements
	// sourcing.DriveFolderSynchronizer. Same drift-guard rationale;
	// composition root is the only place where both sourcing and
	// drivesync are reachable without a cycle.
	_ sourcing.DriveFolderSynchronizer = (*drivesync.Service)(nil)
	// P0-1 / commit 4 (this commit): localimport.Service implements
	// sourcing.LocalImporter. Composition root transitively imports
	// both sourcing and localimport (the latter is a sub-package of
	// the former; sourcing itself never imports localimport, so no
	// cycle).
	_ sourcing.LocalImporter = (*localimport.Service)(nil)
	// FASE 5: ytadapters.SourcingPublisherAdapter satisfies sourcing.PublisherPort.
	_ sourcing.PublisherPort = (*ytadapters.SourcingPublisherAdapter)(nil)
)
