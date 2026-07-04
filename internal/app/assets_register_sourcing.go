package app

import (
	"go.uber.org/zap"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/drivesync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/localimport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// newAssetRegisterService builds the SourcingService façade. After P0-1 /
// commit 1 (June 2026) it first constructs the YouTubeRegistrar sub-service
// (with v2 adapters that wrap legacy ports) and then injects that, plus the
// remaining JobsPort/FileScannerPort needed by SyncDriveFolder + LocalToDrive
// (not yet extracted, planned in commits 3 and 4 of P0-1), into the slim
// sourcing.NewService ctor (now 4 args, was 14 historically).
func newAssetRegisterService(
	cfg *config.Config,
	log *zap.Logger,
	clipsRepo *assetsrepo.ClipsRepository,
	driveUploader *driveutil.Uploader,
	assetTreeSvc *assettree.Service,
	providerRegistry *providers.Registry,
	clipsHandler *clipsapi.Handler,
	dispatcher *outbox.Dispatcher,
	publisher delivery.Publisher,
) *sourcing.Service {
	// Build the YouTube sub-service with v2 adapters (June 2026, P0-1 / commit 1).
	// The 2 v2 adapters absorb 6 legacy ports (IndexDispatcher + AssetTree +
	// Jobs + Search + Config + legacy Enrichment) into the YouTubeService's
	// 8-port budget per architecture/policy.yaml::max_constructor_deps.
	ytIndex := &youtubeIndexDispatcherAdapter{disp: dispatcher, tree: assetTreeSvc}
	ytEnrich := &youtubeEnrichmentAdapter{
		enrichment: &sourcingEnrichmentAdapter{handler: clipsHandler},
		config:     &sourcingConfigAdapter{cfg: cfg},
		search:     &sourcingSearchAdapter{registry: providerRegistry},
		// jobs port intentionally nil today (composition root signature does
		// not yet expose JobsPort; this preserves historical behaviour where
		// SyncDriveFolder + LocalToDrive were also non-functional in this
		// composition site, and matches what the thinker audit suggested as
		// the conservative interpretation).
	}
	// PR-YT-DRIVE-SERVICE-COMMENT-CLEANUP (July 2026): the legacy
	// `&sourcingDriveAdapter{drive: driveUploader}` 3rd positional arg
	// is dropped — the corresponding field on youtube.Service was
	// retired (zero production reads; Publisher is the canonical Drive
	// upload canal since FASE 5). FASE 0.3 (July 2026): the
	// `sourcingDriveAdapter` struct itself + `sourcing.DrivePort`
	// interface are now PHYSICALLY RETIRED via PR-YT-DRIVE-LEGACY-RETIRE
	// (godlike/07 no-fake-availability: zero live concrete remained
	// post-CUTOVER; deleting a rot interface is the canonical hygiene).
	// See architecture/deprecations.yaml#PR-YT-DRIVE-LEGACY-RETIRE
	// + internal/app/youtube_adapters_drive.go for the comment audit-pin.
	ytSvc := youtube.NewService(
		&sourcingFetchAdapter{registry: providerRegistry},
		&sourcingClipStoreAdapter{repo: clipsRepo},
		&sourcingPublisherAdapter{publisher: publisher},
		&sourcingTranscriberAdapter{cfg: cfg, log: log},
		&sourcingMetadataAdapter{cfg: cfg, admin: driveUploader, reader: driveUploader, log: log},
		ytIndex,
		ytEnrich,
		&zapSourcingLogger{log: log},
	)

	// P0-1 / commit 2: BatchRegistrar sub-service wraps YouTubeRegistrar
	// with a 2-dep ctor (yt + log). Composition follows the YouTube
	// sub-package construction.
	batchSvc := batch.NewService(ytSvc, &zapSourcingLogger{log: log})

	// P0-1 / commit 3: DriveFolderSynchronizer sub-service (this commit).
	// 2-dep ctor: jobs (currently nil at this composition site; preserves
	// the historical fail-closed `jobs port not configured` error path)
	// + log. Future composition sites will inject a real JobsPort adapter.
	drvSvc := drivesync.NewService(nil, &zapSourcingLogger{log: log})

	// P0-1 / commit 4 (this commit): LocalImporter sub-service.
	// 3-dep ctor: jobs + scanner + log (all nil at this composition site
	// today; preserves historical behaviour — file-scanner-not-configured
	// and jobs-port-not-configured errors fire when CLI invokes them in
	// dry-run or non-dry-run paths respectively).
	localSvc := localimport.NewService(nil, nil, &zapSourcingLogger{log: log})

	// P0-1 / commit 5 (this commit): façade di pulizia. 5-arg call:
	// 4 sub-services + log. The historic 14-dep ctor collapses to 5
	// typed sub-service handles. Jobs + scanner ports no longer
	// proxied through the façade — they are owned by drivesync /
	// localimport sub-packages directly (composition site passes nil
	// to both today; future sites inject real JobsPort + FileScannerPort
	// adapters into the respective NewService call sites above).
	return sourcing.NewService(ytSvc, batchSvc, drvSvc, localSvc, &zapSourcingLogger{log: log})
}
