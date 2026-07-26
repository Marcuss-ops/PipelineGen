// Package app owns the complete clips composition pipeline. All application
// use cases are built here before the HTTP module is constructed.
package app

import (
	"fmt"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clips/aistock"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type buildClipsParams struct {
	Cfg              *config.Config
	Log              *zap.Logger
	Deps             *AssetsModuleDeps
	Jobs             *JobsBundle
	Dispatcher       *outbox.Dispatcher
	DriveUploader    *driveutil.Uploader
	AssetRepo        asset.Repository
	SearchAggregator *search.Aggregator
	MetaWriter       semantic.MetadataWriterPort
	FolderMemSvc     *foldermemory.Service
	DeletionSvc      *deletion.DeletionService
	IdemHandler      gin.HandlerFunc
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
		NewClipsRepoDuplicateSource("local", params.Deps.Core.Repositories.ClipsRepo),
	)
	enrichUC, err := appclips.NewEnrichUseCase(
		params.AssetRepo,
		params.MetaWriter,
		mutationsDisp,
		params.Log,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("clips: NewEnrichUseCase: %w", err)
	}

	bulkUploadWorker := appclips.NewBulkUploadWorker(
		params.Deps.Delivery.Publisher,
		newClipsRepoAdapter(params.Deps.Core.Repositories.ClipsRepo),
		newClipsHashAdapter(),
		newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		mutationsDisp,
		params.Log,
	)

	uploadUC, err := appupload.NewUseCase(appupload.UseCaseDeps{
		Artifact:      NewArtifactServiceAdapter(params.Deps.Core.Services.ArtifactService),
		Publisher:     params.Deps.Delivery.Publisher,
		Dispatcher:    clipsDispatcherPort,
		Config:        newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		TreeBuilder:   newClipsAssetTreeAdapter(params.Deps.Core.Services.AssetTreeService),
		JobsSvc:       params.Jobs.Facade,
		ProcessRunner: processRunnerAdapter,
		Log:           params.Log,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("clips: upload.NewUseCase: %w", err)
	}

	aiStockUC, err := aistock.NewUseCase(aistock.UseCaseDeps{
		DriveReader: newAistockDriveReaderAdapter(params.DriveUploader),
		Artifact:    NewArtifactServiceAdapter(params.Deps.Core.Services.ArtifactService),
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
		params.AssetRepo,
		params.Deps.Delivery.Publisher,
		clipsDispatcherPort,
		reuploadFolderRoots,
		params.Log,
	)

	clipsOpsPorts := buildClipOpsPorts(
		newClipsRepoAdapter(params.Deps.Core.Repositories.ClipsRepo),
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
	bulkTagsUC := appclips.NewBulkTagsUseCase(
		params.Deps.Core.Repositories.ClipsRepo,
		params.Deps.Core.Services.AssetTreeService,
	)
	downloadUC := appclips.NewDownloadUseCase(
		params.AssetRepo,
		params.Deps.Core.Repositories.VoiceoverRepo,
	)
	reprocessUC := appclips.NewReprocessUseCase(
		params.AssetRepo,
		params.Deps.Core.Services.MediaProcessor,
		nil,
	)

	clipsDrive := newClipsDriveAdapter(params.DriveUploader, params.DriveUploader, nil)
	repoForSource := func(source string) *sqliteassets.ClipsRepository {
		if !artifacts.IsClipsSource(source) {
			return nil
		}
		return params.Deps.Core.Repositories.ClipsRepo
	}

	descriptor, err := clipsapi.Build(clipsapi.Dependencies{
		Handlers: clipsapi.Deps{
			Search: clipsapi.SearchDeps{
				ClipsRepo:     params.Deps.Core.Repositories.ClipsRepo,
				AssetRepo:     params.AssetRepo,
				VoiceoverRepo: params.Deps.Core.Repositories.VoiceoverRepo,
				ImagesRepo:    params.Deps.Core.Repositories.ImageRepo,
			},
			Ingest: clipsapi.IngestDeps{
				Dispatcher:   clipsDispatcherPort,
				AssetTreeSvc: params.Deps.Core.Services.AssetTreeService,
				JobsSvc:      params.Jobs.Facade,
				ClipsRepo:    params.Deps.Core.Repositories.ClipsRepo,
				EnrichUC:     enrichUC,
				UploadUC:     uploadUC,
				AIStockUC:    aiStockUC,
				Log:          params.Log,
			},
			Operations: clipsapi.OpsDeps{
				ClipOpsService: clipOpsSvc,
				DeletionSvc:    params.DeletionSvc,
				FolderMemSvc:   params.FolderMemSvc,
				ClipsRepo:      params.Deps.Core.Repositories.ClipsRepo,
				DriveAdmin:     clipsDrive,
				AssetTreeSvc:   params.Deps.Core.Services.AssetTreeService,
				Log:            params.Log,
			},
			NonOps: nonops.Deps{
				BulkTagsUC:       bulkTagsUC,
				ReprocessUC:      reprocessUC,
				EnrichUC:         enrichUC,
				ClipIndexer:      params.Deps.Search.ClipIndexerService,
				JobsSvc:          params.Jobs.Facade,
				BulkUploadWorker: bulkUploadWorker,
				RepoForSource:    repoForSource,
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
				AssetRepo:       params.AssetRepo,
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
