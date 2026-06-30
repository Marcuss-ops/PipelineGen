package app

import (
	"context"
	"fmt"
	"strings"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
	imgapp "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	voapp "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// storageDriveAdapter adapts drive.Uploader to storage.DrivePort.
type storageDriveAdapter struct {
	up *driveutil.Uploader
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
	return a.up.RenameFile(ctx, fileID, newName)
}

// zapLogAdapter adapts *zap.Logger to storage.Logger.
type zapLogAdapter struct{ log *zap.Logger }

func (a *zapLogAdapter) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapLogAdapter) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapLogAdapter) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapLogAdapter) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}

// MediaIngestBundle is the capability bundle for the media-ingest module.
type MediaIngestBundle struct {
	DB                *storage.SQLiteDB
	Assets            *asset.Service
	DriveUploader     *driveutil.Uploader
	ImageRepo         *sqassets.ImagesRepository
	VoiceoverRepo     *sqassets.VoiceoversRepository
	ClipsRepo         *sqassets.ClipsRepository
	AssetIndexService *assetindex.Service
	PrebuiltService   *ingest.Service
	Dispatcher        *outbox.Dispatcher
}

// MediaIngestWiring holds the Mediaingest module wiring.
type MediaIngestWiring struct {
	Handler *assetsapi.MediaingestHandler
	Module  module.Module
	Service *ingest.Service
}

// WireMediaIngest creates the Mediaingest handler and module.
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
		imagesRegistry := imgapp.NewRegistryAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath(), log)
		imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveAdmin: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewImageStoreAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath())}, log)
		voiceoverRegistry := voapp.NewVoiceoverRegistryAdapter(bundle.VoiceoverRepo)
		voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveAdmin: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(bundle.VoiceoverRepo)}, log)
		clipRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)
		clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveAdmin: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)}, log)
		stockRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)
		stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveAdmin: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)}, log)
		svc = ingest.NewService(cfg, log, bundle.DriveUploader.Admin(), map[ingest.Kind]*ingest.Pipeline{
			ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
			ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
			ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
			ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
		})
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
	switch strings.ToLower(strings.TrimSpace(req.Source)) {
	case "google-vids", "google-vids-image", "google-slides", "google-flow", "nvidia", "nvidia-local", "local-nim", "flux-1-dev", "flux-1-schnell", "flux.1-schnell", "flux1-schnell", "flux-2-klein", "flux.2-klein-4b", "flux-2-klein-4b":
		return true
	default:
		return false
	}
}
