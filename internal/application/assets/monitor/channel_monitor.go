package monitor

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"

	"go.uber.org/zap"
)

// Priority levels for batch channel scheduling.
const (
	PriorityHot    = 1
	PriorityNormal = 2
	PriorityCold   = 3
)

// ChannelConfig represents a monitored YouTube channel.
// PR 2 (June 2026): the JSON list of channels is removed; channels are now
// loaded exclusively from category_channels via channels.Service.ListEnabled().
// This struct remains the runtime config the monitor uses per channel.
type ChannelConfig struct {
	ID               string        `json:"id"`
	URL              string        `json:"url"`
	Category         string        `json:"category"`
	Keywords         []string      `json:"keywords"`
	MinViews         int           `json:"min_views"`
	MaxClipDuration  int           `json:"max_clip_duration"`
	DriveFolderID    string        `json:"drive_folder_id,omitempty"`
	PlaylistEnd      int           `json:"playlist_end,omitempty"`
	SemanticKeywords []string      `json:"semantic_keywords,omitempty"`
	MinSemanticScore int           `json:"min_semantic_score,omitempty"`
	CheckInterval    time.Duration `json:"check_interval,omitempty"`
	MaxVideosPerRun  int           `json:"max_videos_per_run,omitempty"`
	LookbackDays     int           `json:"lookback_days,omitempty"`
	MaxSegments      int           `json:"max_segments,omitempty"`
	SegmentPrompt    string        `json:"segment_prompt,omitempty"`
	Priority         int           `json:"priority,omitempty"`
}

// EffectivePriority returns the channel's priority, defaulting to normal.
func (c *ChannelConfig) EffectivePriority() int {
	if c.Priority > 0 {
		return c.Priority
	}
	return PriorityNormal
}

// MonitorConfig holds global monitor configuration (yt-dlp path, cookies, etc.).
// PR 2 (June 2026): the Channels field is removed — channels come exclusively
// from category_channels via channels.Service. This struct only holds global
// technical defaults.
type MonitorConfig struct {
	CheckInterval   time.Duration `json:"check_interval"`
	YtdlpPath       string        `json:"ytdlp_path"`
	CookiesPath     string        `json:"cookies_path"`
	MaxClipDuration int           `json:"max_clip_duration"`
	PlaylistEnd     int           `json:"playlist_end"`
	MaxFilesize     string        `json:"max_filesize"`
	OllamaURL       string        `json:"ollama_url"`
}

// ChannelMonitor handles periodic YouTube channel monitoring.
// PR 2 (June 2026): removed db *sql.DB; channels are loaded from
// channels.Service, which is the single canonical source.
// DefaultPlaylistEnd is the global default for how many videos to
// scan per channel check when no channel-level override is set.
const DefaultPlaylistEnd = 50

type ChannelMonitor struct {
	cfg               *config.Config
	clipsRepo         *assets.ClipsRepository
	channelsSvc       *channels.Service
	log               *zap.Logger
	stopCh            chan struct{}
	ytdlp             *downloader.YTDLPDownloader
	youtubeSvc        *youtube.Service
	searchQueriesRepo *assets.SearchQueriesRepository
	ollamaClient      *client.Client
	jobsSvc           *jobtools.Service
	globalSem         chan struct{}
	searchRateLimiter *tokenBucket
	ytdlp             *downloader.YTDLPDownloader
	jobsSvc           *appjobs.Service
}

// NewChannelMonitor creates a new channel monitor.
// PR 2 (June 2026): channelsSvc replaces raw *sql.DB as the single source
// for channel configuration.
func NewChannelMonitor(cfg *config.Config, clipsRepo *assets.ClipsRepository, channelsSvc *channels.Service, log *zap.Logger, youtubeSvc *youtube.Service, ollamaClient *client.Client, ytdlp *downloader.YTDLPDownloader) *ChannelMonitor {
	maxChannels := cfg.Concurrency.MaxConcurrentChannelChecks
	if maxChannels <= 0 {
		maxChannels = 1
	}
	return &ChannelMonitor{
		cfg:          cfg,
		clipsRepo:    clipsRepo,
		channelsSvc:  channelsSvc,
		log:          log,
		stopCh:       make(chan struct{}),
		ytdlp:        downloader.NewYTDLP(cfg),
		youtubeSvc:   youtubeSvc,
		ollamaClient: ollamaClient,
		globalSem:    make(chan struct{}, maxChannels),
		ytdlp:        ytdlp,
	}
}

// SetJobsService wires the jobs service for async clip extraction.
func (m *ChannelMonitor) SetJobsService(svc *jobtools.Service) {
	m.jobsSvc = svc
}

func (m *ChannelMonitor) SetSearchQueriesRepo(repo *assets.SearchQueriesRepository) {
	m.searchQueriesRepo = repo
}

func (m *ChannelMonitor) SetSearchRateLimit(maxPerHour int) {
	if maxPerHour <= 0 {
		m.searchRateLimiter = nil
		return
	}
	m.searchRateLimiter = newTokenBucket(maxPerHour, time.Hour)
	m.log.Info("YouTube search rate limiter configured", zap.Int("max_per_hour", maxPerHour))
}

func (m *ChannelMonitor) Stop() {
	close(m.stopCh)
}

// containsAny checks if a string contains any of the keywords, case-insensitively.
func containsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if len(kw) > 0 && len(text) > 0 {
			textLower := text
			kwLower := kw
			for i := 0; i < len(textLower)-len(kwLower)+1; i++ {
				if textLower[i:i+len(kwLower)] == kwLower {
					return true
				}
			}
		}
	}
	return false
}
