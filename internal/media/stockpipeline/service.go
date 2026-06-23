package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
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
	jobsSvc     *appjobs.Service
	assetIndex  *assetindex.Service
	youtubeSvc  *youtube.Service
	clipIndexer *clipindexer.Service
	metaWriter  *semantic.MetadataWriter
	clipsRepo   *assets.ClipsRepository
	// dispatcher is set after construction via SetDispatcher so the
	// atomic upsert+outbox-enqueue path is used for stock uploads. nil is
	// tolerated for back-compat (tests, partial wiring); Upload falls
	// back to direct repo.UpsertClip + SafeGoFunc(IndexClip) when nil.
	dispatcher *outbox.Dispatcher
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
		ffmpegProc: ffmpeg.NewFromConfig(cfg),
		pcfg: PipelineConfig{
			ChunkDuration:  v.ChunkDuration,
			MaxResults:     v.MaxClipsPerSource,
			EffectInterval: v.EffectInterval,
			EffectsDir:     DefaultPipelineConfig().EffectsDir,
		},
	}
}

// SetClipsRepo injects the clips repository dependency.
func (s *Service) SetClipsRepo(repo *assets.ClipsRepository) {
	s.clipsRepo = repo
}

// SetJobsSvc injects the jobs service dependency.
func (s *Service) SetJobsSvc(jobsSvc *appjobs.Service) {
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

// SetDispatcher injects the canonical media_index_outbox dispatcher so
// run_upload writes the per-chunk media_assets row together with the
// Qdrant upsert as a single atomic tx (crash-safe between write and
// index). Production wiring sets this once at composition time; a nil
// dispatcher falls back to the legacy async clipIndexer.IndexClip.
func (s *Service) SetDispatcher(d *outbox.Dispatcher) {
	s.dispatcher = d
}

// SetMetadataWriter injects the unified metadata writer for semantic enrichment.
// When set, stock chunks get metadata.json uploaded alongside videos on Drive.
func (s *Service) SetMetadataWriter(w *semantic.MetadataWriter) {
	s.metaWriter = w
}

// RegisterHandler registers the stock pipeline job handler with the jobs system.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(appjobs.TypeMediaStock, s.HandleJob)
		s.log.Info("registered media.stock job handler", zap.String("type", appjobs.TypeMediaStock))
	}
}

// HandleJob handles a stock pipeline job from the job queue.
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	var payload StockRunPayload
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
		ClipDuration:  payload.ClipDuration,
		NoAudio:       payload.NoAudio,
		NoEffects:     payload.NoEffects,
		NoTransitions: payload.NoTransitions,
		MaxVideos:     payload.MaxVideos,
		Subfolder:     payload.Subfolder,
		FolderName:    payload.FolderName,
		FolderID:      payload.FolderID,
	}
	if payload.Metadata != nil {
		input.Metadata = &ChunkMetadataInput{
			Title:       payload.Metadata.Title,
			Description: payload.Metadata.Description,
			Tags:        payload.Metadata.Tags,
			Category:    payload.Metadata.Category,
			Author:      payload.Metadata.Author,
			Extra:       payload.Metadata.Extra,
		}
	}

	if tools.Progress != nil {
		input.Progress = tools.Progress
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
		"total_clips":      result.TotalClips,
		"total_chunks":     result.TotalChunks,
		"chunks":           result.Chunks,
		"metadata_link":    result.MetadataLink,
		"metadata_file_id": result.MetadataFileID,
	}, nil
}

// RunInput holds the parameters for a stock pipeline run.
type RunInput struct {
	SearchQueries []string
	DirectURLs    []string
	TotalMinutes  int
	MaxVideos     int
	ChunkDuration int
	ClipDuration  int
	NoAudio       bool
	NoEffects     bool
	NoTransitions bool
	Subfolder     string
	FolderName    string
	FolderID      string
	Metadata      *ChunkMetadataInput
	Progress      func(percent int, message string)
}

// ChunkMetadataInput holds user-provided metadata for chunks.
type ChunkMetadataInput struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// PipelineMetadata is the single metadata JSON uploaded at the end with all chunks.
type PipelineMetadata struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Source      SourceInfo        `json:"source"`
	Pipeline    PipelineInfo      `json:"pipeline"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
	Chunks      []ChunkMeta       `json:"chunks"`
}

// ChunkMeta describes a single chunk within the pipeline metadata.
type ChunkMeta struct {
	Index         int        `json:"index"`
	TimelineStart float64    `json:"timeline_start"`
	TimelineEnd   float64    `json:"timeline_end"`
	DriveLink     string     `json:"drive_link,omitempty"`
	DownloadLink  string     `json:"download_link,omitempty"`
	Clips         []ClipInfo `json:"clips"`
}

// SourceInfo describes the source video.
type SourceInfo struct {
	URL      string  `json:"url"`
	Title    string  `json:"title,omitempty"`
	Duration float64 `json:"duration_sec,omitempty"`
}

// ClipInfo describes a single clip within a chunk.
type ClipInfo struct {
	Index int    `json:"index"`
	Start string `json:"start"`
	End   string `json:"end"`
	Title string `json:"title,omitempty"`
}

// PipelineInfo describes pipeline settings used.
type PipelineInfo struct {
	ClipDuration  int  `json:"clip_duration"`
	ChunkDuration int  `json:"chunk_duration"`
	NoAudio       bool `json:"no_audio"`
	NoEffects     bool `json:"no_effects"`
	NoTransitions bool `json:"no_transitions"`
}

// PipelineResult holds the results of a stock pipeline run.
type PipelineResult struct {
	SearchTerms    []string      `json:"search_terms"`
	TotalClips     int           `json:"total_clips"`
	TotalChunks    int           `json:"total_chunks"`
	Chunks         []ChunkResult `json:"chunks"`
	MetadataLink   string        `json:"metadata_link,omitempty"`
	MetadataFileID string        `json:"metadata_file_id,omitempty"`
}

// ChunkResult represents a single rendered and uploaded video chunk.
type ChunkResult struct {
	Index         int      `json:"index"`
	TimelineStart float64  `json:"timeline_start"`
	TimelineEnd   float64  `json:"timeline_end"`
	LocalPath     string   `json:"local_path"`
	DriveLink     string   `json:"drive_link"`
	DownloadLink  string   `json:"download_link"`
	DriveFileID   string   `json:"drive_file_id"`
	Title         string   `json:"title"`
	SourceIDs     []string `json:"source_ids,omitempty"`
}

// VideoSource represents a single video to be downloaded and processed.
type VideoSource struct {
	URL         string
	Title       string
	Source      string
	DurationSec float64
}
