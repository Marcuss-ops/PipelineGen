package monitor

import (
	"database/sql"
	"time"

	"velox/go-master/internal/config"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/searchqueries"
	"velox/go-master/internal/sources/youtube"

	"go.uber.org/zap"
)

// Priority levels for batch channel scheduling.
const (
	PriorityHot    = 1
	PriorityNormal = 2
	PriorityCold   = 3
)

// ChannelConfig represents a monitored YouTube channel
type ChannelConfig struct {
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

// MonitorConfig holds the full monitor configuration
type MonitorConfig struct {
	CheckInterval   time.Duration   `json:"check_interval"`
	VideoTimeframe  string          `json:"video_timeframe"`
	StockRootID     string          `json:"stock_root_id"`
	YtdlpPath       string          `json:"ytdlp_path"`
	CookiesPath     string          `json:"cookies_path"`
	MaxClipDuration int             `json:"max_clip_duration"`
	PlaylistEnd     int             `json:"playlist_end"`
	MaxFilesize     string          `json:"max_filesize"`
	OllamaURL       string          `json:"ollama_url"`
	Channels        []ChannelConfig `json:"channels"`
}

// DefaultMaxConcurrentChannels is the default limit for concurrent channel checks.
const DefaultMaxConcurrentChannels = 3

// ChannelMonitor handles periodic YouTube channel monitoring
type ChannelMonitor struct {
	cfg               *config.Config
	clipsRepo         *clips.Repository
	log               *zap.Logger
	stopCh            chan struct{}
	youtubeSvc        *youtube.Service
	db                *sql.DB
	searchQueriesRepo *searchqueries.Repository
	ollamaClient      *client.Client
	globalSem         chan struct{}
	searchRateLimiter *tokenBucket
}

// NewChannelMonitor creates a new channel monitor.
func NewChannelMonitor(cfg *config.Config, clipsRepo *clips.Repository, log *zap.Logger, youtubeSvc *youtube.Service, db *sql.DB, ollamaClient *client.Client) *ChannelMonitor {
	return &ChannelMonitor{
		cfg:          cfg,
		clipsRepo:    clipsRepo,
		log:          log,
		stopCh:       make(chan struct{}),
		youtubeSvc:   youtubeSvc,
		db:           db,
		ollamaClient: ollamaClient,
		globalSem:    make(chan struct{}, DefaultMaxConcurrentChannels),
	}
}

func (m *ChannelMonitor) SetSearchQueriesRepo(repo *searchqueries.Repository) {
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
