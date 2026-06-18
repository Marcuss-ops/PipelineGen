package youtube

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/core/processor"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/foldermemory"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/videomuscles"
	"velox/go-master/internal/ml/ollama/client"
	assetprocessing "velox/go-master/internal/repository/assetprocessing"
	assetversions "velox/go-master/internal/repository/assetversions"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/outbox"
	"velox/go-master/internal/upload/drive"
)

type VideoPipeline interface {
	DownloadAndCutYouTubeVideo(ctx context.Context, req videomuscles.YouTubeCutRequest) (*videomuscles.YouTubeCutResult, error)
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
	ollamaClient      *client.Client
	searchL1          sync.Map
	metadataL1        sync.Map
	// dispatcher routes UpsertClip + IndexClip atomically through the
	// outbox_events. When set, enrichment/segment write the clip
	// via dispatcher.EnqueueAndIndex and skip the standalone indexer
	// call. Admin/reindex paths (indexing.go, rebuild_job.go) keep the
	// direct indexer so operator overrides bypass the queue.
	dispatcher *outbox.Dispatcher
	// assetProcessing tracks clip processing state (download_and_cut step).
	assetProcessing *assetprocessing.Repository
	// assetVersions records file identity on successful processing.
	assetVersions *assetversions.Repository
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
	assetDestResolver destination.Resolver,
	ollamaClient *client.Client,
	assetProcRepo *assetprocessing.Repository,
	assetVerRepo *assetversions.Repository,
) *Service {
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
		ollamaClient:      ollamaClient,
		assetProcessing:   assetProcRepo,
		assetVersions:     assetVerRepo,
	}
}

// SetAssetRepos injects the asset lifecycle repositories (late-binding).
// Called from composeIntegration after the repos are constructed.
func (s *Service) SetAssetRepos(assetProc *assetprocessing.Repository, assetVer *assetversions.Repository) {
	s.assetProcessing = assetProc
	s.assetVersions = assetVer
}

// SetDispatcher injects the canonical outbox_events dispatcher.
// Called once during composition root, before any HTTP handler is
// registered. A nil dispatcher (legacy partial wiring) is tolerated at
// runtime so enrichment/segment fall back to the legacy indexer path —
// but production should always pass a non-nil dispatcher to keep the
// ingestion crash-safe under crashes between clip write and Qdrant upsert.
//
// The setter is intentionally NOT safe for mid-flight replacement: the
// invariant "the same dispatcher is used for the whole ingestion session"
// is held by WireServices compositing both ends of the dependency.
func (s *Service) SetDispatcher(d *outbox.Dispatcher) {
	s.dispatcher = d
}

// dispatchOrIndex writes clip metadata via the canonical dispatcher when
// wired (atomic UpsertClip + IndexClip + outbox row), otherwise falls back
// to plain clipsRepo.UpsertClip. Used by enrichment.go + segment.go so
// that both call sites share the same crash-safety contract.
func (s *Service) dispatchOrIndex(ctx context.Context, clip *models.MediaAsset, hash string) error {
	if s.dispatcher != nil {
		return s.dispatcher.EnqueueAndIndex(ctx, clip, hash)
	}
	if s.clipsRepo == nil {
		return nil
	}
	return s.clipsRepo.UpsertClip(ctx, clip)
}

// RegisterHandler registers this service as a handler for youtube_clip.extract jobs
// and youtube.rebuild_search_text jobs.
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeYouTubeClipExtract, s.HandleJob)
		s.log.Info("registered youtube_clip.extract job handler", zap.String("type", string(models.JobTypeYouTubeClipExtract)))

		jobsSvc.RegisterHandler(models.JobTypeYouTubeRebuildST, s.HandleRebuildSearchTextJob)
		s.log.Info("registered youtube.rebuild_search_text job handler", zap.String("type", string(models.JobTypeYouTubeRebuildST)))
	}
}

// GetOrCreateChannelFolder creates (or finds) a per-channel subfolder on Drive
// inside the given parent folder. Used by the channel monitor to organize clips
// into per-channel folders (e.g. "Comedy Root/ziwe/", "Comedy Root/TeamCoco/").
func (s *Service) GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if s.driveClient == nil {
		return parentFolderID, nil // no Drive client, return parent
	}
	uploader := &drive.Uploader{
		Service: s.driveClient,
		Log:     s.log,
	}
	folderID, err := uploader.GetOrCreateFolder(ctx, channelName, parentFolderID)
	if err != nil {
		return parentFolderID, fmt.Errorf("failed to get/create channel folder %q: %w", channelName, err)
	}
	s.log.Info("channel folder resolved",
		zap.String("channel", channelName),
		zap.String("folder_id", folderID),
		zap.String("parent", parentFolderID))
	return folderID, nil
}

// Config returns the service configuration. Used by diagnostics endpoint.
func (s *Service) Config() *config.Config {
	return s.cfg
}
