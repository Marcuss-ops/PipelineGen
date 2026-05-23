package youtube

import (
	"context"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/foldermemory"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/videomuscles"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/storage/assetdestination"
	jobservice "velox/go-master/internal/jobs"
)

type VideoPipeline interface {
	DownloadAndCutYouTubeVideo(ctx context.Context, req videomuscles.YouTubeCutRequest) (string, error)
}

type Service struct {
	cfg               *config.Config
	log               *zap.Logger
	clipsRepo         *clips.Repository
	monitoredRepo     *monitors.Repository
	driveClient       *driveapi.Service
	assetDestResolver destination.Resolver
	mediaProcessor    processor.Processor
	videoPipeline     VideoPipeline
	folderMemory      *foldermemory.Service
	lifecycleService  *lifecycle.Service
	indexer           *clipindexer.Service
}

func NewService(
	cfg *config.Config,
	log *zap.Logger,
	clipsRepo *clips.Repository,
	monitoredRepo *monitors.Repository,
	driveClient *driveapi.Service,
	mediaProcessor processor.Processor,
	videoPipeline VideoPipeline,
	lifecycleService *lifecycle.Service,
	indexer *clipindexer.Service,
) *Service {
	// Create asset destination resolver for unified destination resolution
	assetDestResolver := assetdestination.ToCoreResolver(assetdestination.NewResolver(cfg, log, driveClient))

	// Create folder memory service
	folderMemory := foldermemory.NewService(log, clipsRepo)

	return &Service{
		cfg:               cfg,
		log:               log,
		clipsRepo:         clipsRepo,
		monitoredRepo:     monitoredRepo,
		driveClient:       driveClient,
		assetDestResolver: assetDestResolver,
		mediaProcessor:    mediaProcessor,
		videoPipeline:     videoPipeline,
		folderMemory:      folderMemory,
		lifecycleService:  lifecycleService,
		indexer:           indexer,
	}
}

// RegisterHandler registers this service as a handler for youtube_clip.extract jobs
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeYouTubeClipExtract, s.HandleJob)
		s.log.Info("registered youtube_clip.extract job handler", zap.String("type", string(models.JobTypeYouTubeClipExtract)))
	}
}
