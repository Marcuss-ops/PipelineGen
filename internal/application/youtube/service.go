package youtube

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// VideoCutRequest contains all parameters for downloading and cutting a video segment.
// Application-layer DTO that replaces videomuscles.YouTubeCutRequest.
type VideoCutRequest struct {
	URL               string
	VideoID           string
	Start             float64
	Duration          float64
	OutputName        string
	ForceKeyframes    bool
	KeepAudio         bool
	Normalize         bool
	Strategy          string
	OutputDir         string
	PreDownloadedPath string
}

// VideoCutResult wraps the output of a video cut operation with the local file path
// and the full video metadata captured from yt-dlp.
// Application-layer DTO that replaces videomuscles.YouTubeCutResult.
type VideoCutResult struct {
	LocalPath string
	Metadata  *YouTubeMetadataPort
}

// VideoPipeline is the port for downloading + cutting YouTube video segments.
type VideoPipeline interface {
	DownloadAndCutYouTubeVideo(ctx context.Context, req VideoCutRequest) (*VideoCutResult, error)
}

type Service struct {
	cfg               *config.Config
	log               *zap.Logger
	mediaProcessor    asset.Processor
	videoPipeline     VideoPipeline
	lifecycleService  *lifecycle.Service
	assetDestResolver asset.Resolver
	searchL1          sync.Map
	metadataL1        sync.Map
	assetRepo         asset.Repository
	assetProcessing   asset.ProcessingRepository
	assetVersions     asset.VersionRepository

	// PR1 port-based dependencies — injected via setters from composition root.
	searchRunner    SearchRunnerPort
	subtitleFetcher SubtitleFetcherPort
	whisper         WhisperTranscriberPort
	clipFiles       ClipFilesPort
	metaFetcher     VideoMetadataFetcherPort
	driveFolderMgr  DriveFolderManagerPort
	hashSvc         HashServicePort
	tempFiles       TempFileManagerPort

	// PR1.5 port-based dependencies — replace concrete infrastructure imports.
	clips        ClipStorePort
	monitors     MonitorsStorePort
	cacheStore   YouTubeCacheStorePort
	indexer      ClipIndexerPort
	folderMemory FolderMemoryPort
	ollamaClient OllamaClientPort
	disp         DispatcherPort
	driveClient  DriveClientPort
	classifier   CategoryClassifierPort
}

func NewService(
	cfg *config.Config,
	log *zap.Logger,
	mediaProcessor asset.Processor,
	videoPipeline VideoPipeline,
	lifecycleService *lifecycle.Service,
	assetDestResolver asset.Resolver,
) *Service {
	return &Service{
		cfg:               cfg,
		log:               log,
		mediaProcessor:    mediaProcessor,
		videoPipeline:     videoPipeline,
		lifecycleService:  lifecycleService,
		assetDestResolver: assetDestResolver,
	}
}

// ── Port setters (composition root calls after construction) ───────────

func (s *Service) SetSearchRunner(r SearchRunnerPort)           { s.searchRunner = r }
func (s *Service) SetSubtitleFetcher(f SubtitleFetcherPort)     { s.subtitleFetcher = f }
func (s *Service) SetWhisper(w WhisperTranscriberPort)          { s.whisper = w }
func (s *Service) SetClipFiles(f ClipFilesPort)                 { s.clipFiles = f }
func (s *Service) SetMetadataFetcher(f VideoMetadataFetcherPort) { s.metaFetcher = f }
func (s *Service) SetDriveFolderMgr(m DriveFolderManagerPort)   { s.driveFolderMgr = m }
func (s *Service) SetHashSvc(h HashServicePort)                 { s.hashSvc = h }
func (s *Service) SetTempFiles(t TempFileManagerPort)           { s.tempFiles = t }
func (s *Service) SetClipStore(c ClipStorePort)                 { s.clips = c }
func (s *Service) SetMonitorsStore(m MonitorsStorePort)         { s.monitors = m }
func (s *Service) SetCacheStore(c YouTubeCacheStorePort)        { s.cacheStore = c }
func (s *Service) SetIndexer(i ClipIndexerPort)                 { s.indexer = i }
func (s *Service) SetFolderMemory(f FolderMemoryPort)           { s.folderMemory = f }
func (s *Service) SetOllamaClient(o OllamaClientPort)           { s.ollamaClient = o }
func (s *Service) SetDispatcher(d DispatcherPort)               { s.disp = d }
func (s *Service) SetDriveClient(d DriveClientPort)             { s.driveClient = d }
func (s *Service) SetClassifier(c CategoryClassifierPort)       { s.classifier = c }

func (s *Service) SetAssetRepos(assetProc asset.ProcessingRepository, assetVer asset.VersionRepository) {
	s.assetProcessing = assetProc
	s.assetVersions = assetVer
}

func (s *Service) SetAssetRepo(r asset.Repository) {
	s.assetRepo = r
}

// ── Legacy compatibility — kept during incremental migration ──────────

func (s *Service) dispatchOrIndex(ctx context.Context, clip *asset.Asset, hash string) error {
	if s.assetRepo != nil {
		return s.assetRepo.Upsert(ctx, clip)
	}
	if s.disp != nil {
		return s.disp.EnqueueAndIndex(ctx, clip, hash)
	}
	if s.clips == nil {
		return nil
	}
	return s.clips.Upsert(ctx, clip)
}

func (s *Service) RegisterHandler(jobsSvc *jobtools.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(jobservice.TypeYouTubeClipExtract, s.HandleJob)
		s.log.Info("registered youtube_clip.extract job handler", zap.String("type", jobservice.TypeYouTubeClipExtract))

		jobsSvc.RegisterHandler(jobservice.TypeYouTubeRebuildST, s.HandleRebuildSearchTextJob)
		s.log.Info("registered youtube.rebuild_search_text job handler", zap.String("type", jobservice.TypeYouTubeRebuildST))
	}
}

func (s *Service) GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if s.driveFolderMgr != nil {
		folderID, err := s.driveFolderMgr.GetOrCreateFolder(ctx, channelName, parentFolderID)
		if err != nil {
			return parentFolderID, fmt.Errorf("failed to get/create channel folder %q: %w", channelName, err)
		}
		s.log.Info("channel folder resolved",
			zap.String("channel", channelName),
			zap.String("folder_id", folderID),
			zap.String("parent", parentFolderID))
		return folderID, nil
	}
	if s.driveClient != nil {
		folderID, err := s.driveClient.GetOrCreateChannelFolder(ctx, channelName, parentFolderID)
		if err != nil {
			return parentFolderID, fmt.Errorf("failed to get/create channel folder %q: %w", channelName, err)
		}
		s.log.Info("channel folder resolved",
			zap.String("channel", channelName),
			zap.String("folder_id", folderID),
			zap.String("parent", parentFolderID))
		return folderID, nil
	}
	return parentFolderID, nil
}

func (s *Service) DownloadAndCut(ctx context.Context, req VideoCutRequest) (*VideoCutResult, error) {
	if s.videoPipeline == nil {
		return nil, fmt.Errorf("youtube: video pipeline not wired")
	}
	return s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, req)
}

func (s *Service) Config() *config.Config {
	return s.cfg
}

// md5String computes an MD5 hex string using the hashSvc port when wired,
// falling back to a simple local implementation for backward compatibility.
func (s *Service) md5String(data string) string {
	if s.hashSvc != nil {
		return s.hashSvc.MD5String(data)
	}
	return fallbackMD5String(data)
}

// md5File computes an MD5 hex string of a file's contents.
func (s *Service) md5File(path string) string {
	if s.hashSvc != nil {
		h, err := s.hashSvc.MD5File(path)
		if err == nil {
			return h
		}
	}
	return fallbackMD5File(path)
}
