package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	corejobs "velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/pkg/media/downloader"
	"velox/go-master/internal/pkg/media/ffmpeg"
	"velox/go-master/internal/sources/youtube"
	driveup "velox/go-master/internal/upload/drive"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

// PipelineConfig holds configuration for the stock pipeline run.
type PipelineConfig struct {
	ChunkDuration  int
	MaxResults     int
	EffectInterval int
	EffectsDir     string
}

// DefaultPipelineConfig returns a PipelineConfig with sensible defaults.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		ChunkDuration:  25,
		MaxResults:     25,
		EffectInterval: 4,
		EffectsDir:     "assets/effects/EffettiVisiv",
	}
}

// Service orchestrates the stock video pipeline: search, download, clip extraction,
// effect overlay, chunk rendering, and Drive upload. All video parameters are read
// from config.Video to ensure consistency with other media pipelines.
type Service struct {
	cfg         *config.Config
	log         *zap.Logger
	driveSvc    *gdrive.Service
	driveUp     *driveup.Uploader
	ytdlp       *downloader.YTDLPDownloader
	ffmpegProc  *ffmpeg.Processor
	pcfg        PipelineConfig
	jobsSvc     *jobservice.Service
	assetIndex  *assetindex.Service
	youtubeSvc  *youtube.Service
	clipIndexer *clipindexer.Service
	metaWriter  *semantic.MetadataWriter
}

// NewService creates a stock pipeline service using the provided config, logger,
// and Google Drive service. Video processing defaults are loaded from cfg.Video.
func NewService(cfg *config.Config, log *zap.Logger, driveSvc *gdrive.Service) *Service {
	v := cfg.Video.WithDefaults()
	return &Service{
		cfg:        cfg,
		log:        log,
		driveSvc:   driveSvc,
		driveUp:    &driveup.Uploader{Service: driveSvc, Log: log},
		ytdlp:      downloader.NewYTDLP(cfg),
		ffmpegProc: ffmpeg.New(cfg),
		pcfg: PipelineConfig{
			ChunkDuration:  v.ChunkDuration,
			MaxResults:     v.MaxClipsPerSource,
			EffectInterval: v.EffectInterval,
			EffectsDir:     "assets/effects/EffettiVisiv",
		},
	}
}

// SetJobsSvc injects the jobs service dependency.
func (s *Service) SetJobsSvc(jobsSvc *jobservice.Service) {
	s.jobsSvc = jobsSvc
}

// SetAssetIndex injects the asset index service dependency.
func (s *Service) SetAssetIndex(ai *assetindex.Service) {
	s.assetIndex = ai
}

// SetYoutubeService injects the YouTube metadata service used to enrich direct URL sources.
func (s *Service) SetYoutubeService(svc *youtube.Service) {
	s.youtubeSvc = svc
}

// SetClipIndexer injects the clip indexer service dependency.
func (s *Service) SetClipIndexer(indexer *clipindexer.Service) {
	s.clipIndexer = indexer
}

// SetMetadataWriter injects the unified metadata writer for semantic enrichment.
// When set, stock chunks get metadata.json uploaded alongside videos on Drive.
func (s *Service) SetMetadataWriter(w *semantic.MetadataWriter) {
	s.metaWriter = w
}

// RegisterHandler registers the stock pipeline job handler with the jobs system.
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeMediaStock, s.HandleJob)
		s.log.Info("registered media.stock job handler", zap.String("type", string(models.JobTypeMediaStock)))
	}
}

// HandleJob handles a stock pipeline job from the job queue.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	var payload corejobs.StockRunPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stock payload: %w", err)
		}
	}

	s.log.Info("stock job payload received",
		zap.String("job_id", job.ID),
		zap.Int("search_queries", len(payload.SearchQueries)),
		zap.Int("direct_urls", len(payload.DirectURLs)),
		zap.Int("total_minutes", payload.TotalMinutes),
		zap.Int("chunk_duration", payload.ChunkDuration),
		zap.String("subfolder", payload.Subfolder),
		zap.String("folder_name", payload.FolderName),
		zap.String("folder_id", payload.FolderID),
	)

	input := &RunInput{
		SearchQueries: payload.SearchQueries,
		DirectURLs:    payload.DirectURLs,
		TotalMinutes:  payload.TotalMinutes,
		ChunkDuration: payload.ChunkDuration,
		MaxVideos:     payload.MaxVideos,
		Subfolder:     payload.Subfolder,
		FolderName:    payload.FolderName,
		FolderID:      payload.FolderID,
	}

	if tools.Progress != nil {
		tools.Progress(5, "Starting stock pipeline")
	}

	result, err := s.Run(ctx, input)
	if err != nil {
		return nil, err
	}

	if tools.Progress != nil {
		tools.Progress(100, "Stock pipeline complete")
	}

	return map[string]any{
		"total_clips":  result.TotalClips,
		"total_chunks": result.TotalChunks,
		"chunks":       result.Chunks,
	}, nil
}

// RunInput holds the parameters for a stock pipeline run.
type RunInput struct {
	SearchQueries []string
	DirectURLs    []string
	TotalMinutes  int
	MaxVideos     int
	ChunkDuration int
	Subfolder     string
	FolderName    string
	FolderID      string
}

// PipelineResult holds the results of a stock pipeline run.
type PipelineResult struct {
	SearchTerms []string      `json:"search_terms"`
	TotalClips  int           `json:"total_clips"`
	TotalChunks int           `json:"total_chunks"`
	Chunks      []ChunkResult `json:"chunks"`
}

// ChunkResult represents a single rendered and uploaded video chunk.
type ChunkResult struct {
	Index         int     `json:"index"`
	TimelineStart float64 `json:"timeline_start"`
	TimelineEnd   float64 `json:"timeline_end"`
	LocalPath     string  `json:"local_path"`
	DriveLink     string  `json:"drive_link"`
	DownloadLink  string  `json:"download_link"`
	DriveFileID   string  `json:"drive_file_id"`
	Title         string  `json:"title"`
}

// VideoSource represents a single video to be downloaded and processed.
type VideoSource struct {
	URL         string
	Title       string
	Source      string
	DurationSec float64
}
