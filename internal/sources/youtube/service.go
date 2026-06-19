package youtube

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
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
	// assetRepo is the canonical writer (PR12b). Late-bound via SetAssetRepo.
	// When wired, dispatchOrIndex prefers it over the dispatcher and the
	// legacy clipsRepository path: it converts the legacy *models.MediaAsset
	// to *assets.Asset via toAssetDomain and routes through
	// assetrepo.Upsert — which writes both legacy and canonical columns in
	// the same row and emits the asset.upserted outbox event in the same
	// transaction.
	assetRepo assets.Repository
	// assetProcessing tracks clip processing state (download_and_cut step).
	assetProcessing assets.ProcessingRepository
	// assetVersions records file identity on successful processing.
	assetVersions assets.VersionRepository
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
	assetProcRepo assets.ProcessingRepository,
	assetVerRepo assets.VersionRepository,
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
// In tests, assetVer can be nil/mocked if version tracking is disabled.
func (s *Service) SetAssetRepos(assetProc assets.ProcessingRepository, assetVer assets.VersionRepository) {
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

// SetAssetRepo injects the canonical Repository. Mirrors
// SetDispatcher semantics: late-bound once during composition root, idempotent,
// nil-safe (legacy callers fall through to dispatcher / clipsRepo paths).
// When wired, dispatchOrIndex prefers assetRepo over dispatcher so the
// canonical SQL upsert writes both legacy + canonical columns + outbox row
// in a single transaction.
func (s *Service) SetAssetRepo(r assets.Repository) {
	s.assetRepo = r
}

// dispatchOrIndex writes clip metadata via the canonical pipeline. The
// preference order is fully explicit so callers reading the function can
// reason about which path is active:
//
//   1. assetRepo wired (PR12b): convert via toAssetDomain and call
//      assetrepo.Upsert. Writes legacy + canonical columns in one row, emits
//      the asset.upserted outbox event in the same transaction, and DOES NOT
//      touch the legacy indexer (the fresh-media Qdrant side-effect is now
//      owned by outboxhandlers reading the event).
//
//   2. dispatcher wired (PR3-5b.4): EnqueueAndIndex does UpsertClip +
//      IndexClip atomically through the outbox_events. The carry-over from
//      before PR12b.
//
//   3. legacy fallback: clipsRepo.Upsert. No outbox, no Qdrant side-effect.
//      Only reached when neither assetRepo nor dispatcher is wired (which is
//      the case for tests that don't construct the full composition root).
//
// Used by enrichment.go + segment.go so both call sites share the same
// crash-safety contract.
func (s *Service) dispatchOrIndex(ctx context.Context, clip *assets.Asset, hash string) error {
	if s.assetRepo != nil {
		return s.assetRepo.Upsert(ctx, clip)
	}
	if s.dispatcher != nil {
		return s.dispatcher.EnqueueAndIndex(ctx, clip, hash)
	}
	if s.clipsRepo == nil {
		return nil
	}
	return s.clipsRepo.Upsert(ctx, clip)
}

// RegisterHandler registers this service as a handler for youtube_clip.extract jobs
// and youtube.rebuild_search_text jobs.
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(jobservice.JobTypeYouTubeClipExtract, s.HandleJob)
		s.log.Info("registered youtube_clip.extract job handler", zap.String("type", jobservice.JobTypeYouTubeClipExtract))

		jobsSvc.RegisterHandler(jobservice.JobTypeYouTubeRebuildST, s.HandleRebuildSearchTextJob)
		s.log.Info("registered youtube.rebuild_search_text job handler", zap.String("type", jobservice.JobTypeYouTubeRebuildST))
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
