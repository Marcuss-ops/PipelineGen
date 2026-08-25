package wiring

import (
	"context"
	"fmt"
	"time"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	appstorage "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/storage"
	voapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

// storageDriveAdapter adapts drive.Uploader to storage.DrivePort.
type storageDriveAdapter struct {
	up        *driveutil.Uploader
	lifecycle driveutil.FileLifecycle
}

var _ appstorage.DrivePort = (*storageDriveAdapter)(nil)

func (a *storageDriveAdapter) ListFiles(ctx context.Context, folderID string) ([]appstorage.DriveFile, error) {
	files, err := a.up.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]appstorage.DriveFile, len(files))
	for i, f := range files {
		out[i] = appstorage.DriveFile{ID: f.ID, Name: f.Name, MimeType: f.MimeType}
	}
	return out, nil
}

func (a *storageDriveAdapter) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return a.up.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

func (a *storageDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	return a.up.GetOrCreateFolder(ctx, name, parentID)
}

func (a *storageDriveAdapter) RenameFile(ctx context.Context, fileID, newName string) error {
	if a.lifecycle == nil {
		return fmt.Errorf("storageDriveAdapter: lifecycle not wired (P1-5 CUTOVER requires FileLifecycle)")
	}
	return a.lifecycle.Rename(ctx, fileID, newName)
}

// MediaIngestBundle is the capability bundle for the media-ingest module.
//
// F2.7 (June 2026): Publisher (delivery.Publisher) added. The legacy
// driveUploader.Admin() upload path is dead — ingest lifecycle services
// route Drive writes through Publisher, the canonical Pattern 0 port,
// and use driveUploader only as a drive.Reader for the reconcile /
// verify read surface.
type MediaIngestBundle struct {
	DB                *storage.SQLiteDB
	Assets            *asset.Service
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
	mutationsDisp, err := newMutationsDispatcherAdapter(bundle.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("WireMediaIngest: %w", err)
	}
	svc := bundle.PrebuiltService
	if svc == nil {
		imagesRegistry := imagesregistry.NewRegistryAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath(), log, bundle.Committer)
		imagesLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: imagesRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewImageStoreAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath())}, log)
		voiceoverRegistry := voapp.NewVoiceoverRegistryAdapter(bundle.VoiceoverRepo)
		voiceoverLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: voiceoverRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(bundle.VoiceoverRepo)}, log)
		clipRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), bundle.Committer)
		clipLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: clipRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)}, log)
		stockRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), bundle.Committer)
		stockLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: stockRegistry, Publisher: bundle.Publisher, DriveReader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)}, log)
		var downloader assets.MediaDownloader = downloader.NewMediaDownloader(90 * time.Second)
		// CAS-backed source-aware downloader (August 2026): optional
		// enhancement over the plain HTTP downloader; fall back + log when
		// the CAS layer cannot be wired (media acquisition must never be
		// blocked by the optional cache layer).
		if bundle.DB != nil && bundle.DB.DB != nil {
			if casDL, casErr := buildSourceAwareDownloader(cfg, bundle.DB.DB, log); casErr == nil {
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
	return asset.IsAIImageSource(req.Source)
}
