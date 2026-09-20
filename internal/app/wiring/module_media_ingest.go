package wiring

import (
	"database/sql"
	"fmt"
	"time"

	registrywiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/registry"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	voapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	imagesregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// storageDriveAdapter and its four DrivePort methods were DELETED here on
// 2026-09-20. The adapter was never constructed anywhere in the tree, and
// appstorage.DrivePort (internal/capabilities/assets/storage/ports.go) had
// zero consumers: no struct field, parameter or return value in the module
// was typed by it. Ingest lifecycle services read Drive through the narrower
// ingest.DriveReader port (bundle.DriveUploader), which is the canonical
// surface — DrivePort was a superseded duplicate of driveutil.Uploader's
// ListFiles/MoveFile/GetOrCreateFolder plus FileLifecycle.Rename, so all four
// methods were unreachable. Deleted together: the port + its only witness
// (storage_wiring_test.go's fakeDriveForPortTest), so nothing pins the shape
// back into existence.

// MediaIngestBundle is the capability bundle for the media-ingest module.
//
// F2.7 (June 2026): Publisher (delivery.Publisher) added. The legacy
// driveUploader.Admin() upload path is dead — ingest lifecycle services
// route Drive writes through Publisher, the canonical Pattern 0 port,
// and use driveUploader only as a drive.Reader for the reconcile /
// verify read surface.
type MediaIngestBundle struct {
	DB                *storage.SQLiteDB
	CacheDB           *storage.SQLiteDB
	Assets            *detail.Service
	DriveUploader     *driveutil.Uploader
	Lifecycle         driveutil.FileLifecycle
	Publisher         delivery.Publisher
	ImageRepo         *imagesrepo.ImagesRepository
	VoiceoverRepo     *sqassets.VoiceoversRepository
	ClipsRepo         *sqassets.ClipsRepository
	AssetIndexService *assetindex.Service
	PrebuiltService   *ingest.Service
	Dispatcher        *outbox.Dispatcher
	Committer         assetspersistence.AssetCommitter
}

// MediaIngestWiring holds the Mediaingest module
type MediaIngestWiring struct {
	Handler *assetsapi.MediaingestHandler
	Module  module.Module
	Service *ingest.Service
}

// WireMediaIngest creates the Mediaingest handler and module.
//
// F2.7 (June 2026): ingest lifecycle services receive the canonical
// delivery.Publisher instead of bundle.DriveUploader.Admin() —
// Drive uploads flow through DestinationRegistry + RequireSubpath +
// ConflictPolicy. bundle.DriveUploader stays as the DriveReader
// for the reconcile / verify read surface.
func WireMediaIngest(cfg *config.Config, log *zap.Logger, bundle *MediaIngestBundle, idempotencyMiddleware gin.HandlerFunc) (*MediaIngestWiring, error) {
	if bundle == nil || bundle.DriveUploader == nil {
		return nil, nil
	}
	if bundle.ImageRepo == nil || bundle.VoiceoverRepo == nil || bundle.ClipsRepo == nil || bundle.AssetIndexService == nil {
		return nil, nil
	}
	mutationsDisp, err := registrywiring.NewMutationsDispatcherAdapter(bundle.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("WireMediaIngest: %w", err)
	}
	svc := bundle.PrebuiltService
	if svc == nil {
		imagesRegistry := imagesregistry.NewRegistryAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath(), log, bundle.Committer)
		imagesLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: imagesRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewImageStoreAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath())}, log)
		voiceoverRegistry := voapp.NewVoiceoverRegistryAdapter(bundle.VoiceoverRepo)
		voiceoverLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: voiceoverRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(bundle.VoiceoverRepo)}, log)
		// MEDIA-SSOT P2-9 step 2: hydrate the media record from the canonical
		// committer's engine, never from the operational SQLite mirror.
		mediaDetails := mediaDetailsReaderFromCommitter(bundle.Committer)
		// MEDIA-SSOT write-bridge: retirement resolves from the canonical
		// committer's own engine (see build_bundles_ingest.go).
		mediaRetirer := assetspersistence.CanonicalAssetSoftDeleter(bundle.Committer)
		// MEDIA-SSOT write-bridge: asset_locations + asset_processing are
		// media-authoritative, so both write ports resolve from the canonical
		// committer's engine rather than the operational SQLite store. A nil
		// port means the media plane is closed and the ingest write fails
		// closed instead of landing on an unnamed database.
		mediaLocations := assetspersistence.CanonicalAssetLocationWriter(bundle.Committer)
		mediaProcessing := assetspersistence.CanonicalAssetProcessingWriter(bundle.Committer)
		// MEDIA-SSOT P2-9 Phase 2: the drive-file-id listing is a MEDIA read, so it
		// resolves from the canonical committer's engine. bundle.DB is the
		// operational SQLite store; it holds no committed media rows, so a
		// listing served from it could never see the SSOT.
		mediaDriveFileIDs := mediaDriveFileListerFromCommitter(bundle.Committer)
		clipRegistry := artifacts.NewClipsRegistryWithLogger(mediaDetails, mediaProcessing, bundle.Committer, log)
		clipLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: clipRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, mediaRetirer, mediaDetails, mediaLocations, mediaProcessing, mutationsDisp, mediaDriveFileIDs)}, log)
		stockRegistry := artifacts.NewClipsRegistryWithLogger(mediaDetails, mediaProcessing, bundle.Committer, log)
		stockLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: stockRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, mediaRetirer, mediaDetails, mediaLocations, mediaProcessing, mutationsDisp, mediaDriveFileIDs)}, log)
		var downloader assets.MediaDownloader = downloader.NewMediaDownloader(90 * time.Second)
		// CAS-backed source-aware downloader (August 2026): optional
		// enhancement over the plain HTTP downloader; fall back + log when
		// the CAS layer cannot be wired (media acquisition must never be
		// blocked by the optional cache layer).
		if bundle.DB != nil && bundle.DB.DB != nil {
			var cacheDB *sql.DB
			if bundle.CacheDB != nil {
				cacheDB = bundle.CacheDB.DB
			}
			if casDL, casErr := buildSourceAwareDownloader(cfg, bundle.DB.DB, cacheDB, log); casErr == nil {
				downloader = casDL
			} else {
				log.Warn("CAS-backed downloader not wired — falling back to plain media downloader",
					zap.Error(casErr))
			}
		}
		// PR-WAVE-1-DRIVE-SSOT (July 2026): the legacy
		// `bundle.DriveUploader.Admin()` arg is REMOVED from the
		// canonical NewService ctor (the field was unused; the
		// composition root holds *driveutil.Uploader directly for
		// the lifecycle adapter reads).
		svc = ingest.NewService(cfg, log, downloader, map[ingest.Kind]*ingest.Pipeline{
			ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
			ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
			ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
			ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
			// PR-ENRICHMENT-STATE-MACHINE EXPAND phase: enrichState
			// passed as nil. The ingest service flips PENDING on every
			// freshly-ingested row only when the typed state-machine
			// wrapper is wired. Until the composition root wires the
			// state machine at boot, the VLM 15-min sweeper still
			// recovers via the typed-state filter (backfill path per
			// godlike/07). BACKFILL wave forward-pointer wires the
			// live state-machine here.
		}, nil /* enrichState: nil for EXPAND phase */)
	}
	handler := assetsapi.NewMediaingestHandler(svc, idempotencyMiddleware)
	mod := module.NewRouteModule(
		"media-ingest",
		func() bool { return handler != nil },
		"/media",
		handler,
		log,
	)
	return &MediaIngestWiring{Handler: handler, Module: mod, Service: svc}, nil
}

func isAIImageIngestSource(req *ingest.Request) bool {
	if req == nil {
		return false
	}
	return detail.IsAIImageSource(req.Source)
}
