// Package app owns the complete clips composition pipeline. All application
// use cases are built here before the HTTP module is constructed.
package app

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clips/aistock"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ClipsRepositoryDeps groups the repository ports consumed by clips.
type ClipsRepositoryDeps struct {
	ClipsRepo     *sqassets.ClipsRepository
	VoiceoverRepo *sqassets.VoiceoversRepository
	ImageRepo     *imagesrepo.ImagesRepository
	AssetRepo     asset.Repository
}

// ClipsCapabilityDeps contains only the concrete ports consumed by the clips
// capability builder. AssetsModuleDeps is projected into this bundle at the
// WireAssets boundary and does not cross into the builder.
type ClipsCapabilityDeps struct {
	Repositories       ClipsRepositoryDeps
	ArtifactService    *artifacts.Service
	AssetTreeService   *assettree.Service
	MediaProcessor     asset.Processor
	Publisher          delivery.Publisher
	ClipIndexerService *clipindexer.Service
}

type buildClipsParams struct {
	Cfg           *config.Config
	Log           *zap.Logger
	Clips         ClipsCapabilityDeps
	Jobs          *wiring.JobsBundle
	Dispatcher    *outbox.Dispatcher
	DriveUploader *driveutil.Uploader
	MetaWriter    semantic.MetadataWriterPort
	DeletionSvc   *deletion.DeletionService
	IdemHandler   gin.HandlerFunc
}

func buildClipsBundle(params buildClipsParams) (*clipsapi.ClipsModule, appclips.ClipEnricher, error) {
	var clipsDispatcherPort appclips.ClipIndexDispatcherPort
	if params.Dispatcher != nil {
		clipsDispatcherPort = &clipsDispatcherAdapter{disp: params.Dispatcher}
	}
	mutationsDisp, err := newMutationsDispatcherAdapter(params.Dispatcher)
	if err != nil {
		return nil, nil, fmt.Errorf("clips: mutations dispatcher: %w", err)
	}

	duplicateFinder := duplicates.NewFinder(
		NewClipsRepoDuplicateSource("local", params.Clips.Repositories.ClipsRepo),
	)
	enrichUC, err := appclips.NewEnrichUseCase(
		params.Clips.Repositories.AssetRepo,
		params.MetaWriter,
		mutationsDisp,
		params.Log,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("clips: NewEnrichUseCase: %w", err)
	}

	bulkUploadWorker := appclips.NewBulkUploadWorker(
		params.Clips.Publisher,
		newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
		newClipsHashAdapter(),
		newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		mutationsDisp,
		params.Log,
	)

	uploadUC, err := appupload.NewUseCase(appupload.UseCaseDeps{
		Artifact:      NewArtifactServiceAdapter(params.Clips.ArtifactService),
		Publisher:     params.Clips.Publisher,
		Dispatcher:    clipsDispatcherPort,
		Config:        newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		TreeBuilder:   newClipsAssetTreeAdapter(params.Clips.AssetTreeService),
		JobsSvc:       params.Jobs.Facade,
		ProcessRunner: processRunnerAdapter,
		Log:           params.Log,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("clips: upload.NewUseCase: %w", err)
	}

	aiStockUC, err := aistock.NewUseCase(aistock.UseCaseDeps{
		DriveReader: newAistockDriveReaderAdapter(params.DriveUploader),
		Artifact:    NewArtifactServiceAdapter(params.Clips.ArtifactService),
		Dispatcher:  clipsDispatcherPort,
		Log:         params.Log,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("clips: aistock.NewUseCase: %w", err)
	}

	reuploadFolderRoots := map[string]appclips.ReuploadFolderRoot{
		"clips":   {RootID: params.Cfg.Drive.ClipsFolder(), PathMarker: params.Cfg.Storage.YoutubeClipsPath()},
		"youtube": {RootID: params.Cfg.Drive.ClipsFolder(), PathMarker: params.Cfg.Storage.YoutubeClipsPath()},
		"artlist": {RootID: params.Cfg.Drive.ArtlistFolder(), PathMarker: params.Cfg.Storage.ArtlistPath()},
		"stock":   {RootID: params.Cfg.Drive.StockFolder(), PathMarker: params.Cfg.Storage.FullPath("stock")},
	}
	reuploadUC := appclips.NewReuploadUseCase(
		params.Clips.Repositories.AssetRepo,
		params.Clips.Publisher,
		clipsDispatcherPort,
		reuploadFolderRoots,
		params.Log,
	)

	clipsOpsPorts := buildClipOpsPorts(
		newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
		params.Jobs,
	)
	clipOpsSvc := appclips.NewClipOpsService(
		clipsOpsPorts.clipRepo,
		clipsOpsPorts.voiceoverRepo,
		clipsOpsPorts.imageRepo,
		clipsOpsPorts.driveUploader,
		clipsOpsPorts.jobsPort,
		clipsDispatcherPort,
		params.Log,
	)

	// These use cases previously lived in clips.NewHandler. Building them here
	// makes the API package transport-only and gives NonOps/Actions precisely
	// the operations they execute.
	downloadUC := appclips.NewDownloadUseCase(
		params.Clips.Repositories.AssetRepo,
		params.Clips.Repositories.VoiceoverRepo,
	)
	reprocessUC := appclips.NewReprocessUseCase(
		params.Clips.Repositories.AssetRepo,
		params.Clips.MediaProcessor,
		mutationsDisp,
		params.Cfg.Drive.ClipsFolder(),
	)
	if params.DriveUploader != nil {
		reprocessUC.SetRemoteAssetReader(params.DriveUploader)
	}

	clipsDrive := newClipsDriveAdapter(params.DriveUploader, params.DriveUploader, nil)

	descriptor, err := clipsapi.Build(clipsapi.Dependencies{
		Handlers: clipsapi.Deps{
			Search: clipsapi.SearchDeps{
				ClipsRepo:     newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
				AssetRepo:     params.Clips.Repositories.AssetRepo,
				VoiceoverRepo: newVoiceoverRepoAdapter(params.Clips.Repositories.VoiceoverRepo),
				ImagesRepo:    params.Clips.Repositories.ImageRepo,
			},
			Ingest: clipsapi.IngestDeps{
				Dispatcher:   clipsDispatcherPort,
				AssetTreeSvc: params.Clips.AssetTreeService,
				JobsSvc:      params.Jobs.Facade,
				ClipsRepo:    newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
				EnrichUC:     enrichUC,
				UploadUC:     uploadUC,
				AIStockUC:    aiStockUC,
				Log:          params.Log,
			},
			Operations: clipsapi.OpsDeps{
				ClipOpsService: clipOpsSvc,
				DeletionSvc:    params.DeletionSvc,
				ClipsRepo:      newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
				DriveAdmin:     clipsDrive,
				AssetTreeSvc:   params.Clips.AssetTreeService,
				Log:            params.Log,
			},
			NonOps: nonops.Deps{
				ReprocessUC:      reprocessUC,
				EnrichUC:         enrichUC,
				JobsSvc:          params.Jobs.Facade,
				BulkUploadWorker: bulkUploadWorker,
				Log:              params.Log,
			},
			Bulk: clipsapi.BulkTransportDeps{
				JobsSvc:          params.Jobs.Facade,
				MediaPath:        params.Cfg.Storage.MediaPath(),
				TempPath:         params.Cfg.Storage.TempPath(),
				DataDir:          params.Cfg.Storage.AbsDataDir(),
				BulkUploadWorker: bulkUploadWorker,
				Log:              params.Log,
			},
			Actions: clipsapi.ActionDeps{
				AssetRepo:       params.Clips.Repositories.AssetRepo,
				DriveAdmin:      clipsDrive,
				DuplicateFinder: duplicateFinder,
				DownloadUC:      downloadUC,
				ReuploadUC:      reuploadUC,
				Log:             params.Log,
			},
		},
		Transport: clipsapi.TransportDeps{
			Idempotency: params.IdemHandler,
			EnabledFunc: func() bool { return true },
			Logger:      params.Log,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	if err := ClassifyDepGet(
		"WireAssets: clips: *clipsapi.ClipsModule is nil",
		descriptor == nil,
		DepRequired,
		params.Log,
	); err != nil {
		return nil, nil, err
	}
	return descriptor, enrichUC, nil
}
