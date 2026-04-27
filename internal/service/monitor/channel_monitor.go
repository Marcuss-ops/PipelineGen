package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/config"
)

// ChannelConfig represents a monitored YouTube channel
type ChannelConfig struct {
	URL            string   `json:"url"`
	Category       string   `json:"category"`
	Keywords       []string `json:"keywords"`
	MinViews       int      `json:"min_views"`
	MaxClipDuration int     `json:"max_clip_duration"`
}

// MonitorConfig holds the full monitor configuration
type MonitorConfig struct {
	CheckInterval    time.Duration `json:"check_interval"`
	VideoTimeframe  string        `json:"video_timeframe"`
	StockRootID     string        `json:"stock_root_id"`
	YtdlpPath       string        `json:"ytdlp_path"`
	CookiesPath     string        `json:"cookies_path"`
	MaxClipDuration int           `json:"max_clip_duration"`
	OllamaURL       string        `json:"ollama_url"`
	Channels        []ChannelConfig `json:"channels"`
}

// ChannelMonitor handles periodic YouTube channel monitoring
type ChannelMonitor struct {
	cfg       *config.Config
	clipsRepo *clips.Repository
	log       *zap.Logger
	stopCh    chan struct{}
}

// NewChannelMonitor creates a new channel monitor
func NewChannelMonitor(cfg *config.Config, clipsRepo *clips.Repository, log *zap.Logger) *ChannelMonitor {
	return &ChannelMonitor{
		cfg:       cfg,
		clipsRepo: clipsRepo,
		log:       log,
		stopCh:    make(chan struct{}),
	}
}

// Start begins the channel monitoring process
func (m *ChannelMonitor) Start(ctx context.Context) {
	m.log.Info("Starting channel monitor")

	// Load config
	monitorCfg, err := m.loadConfig()
	if err != nil {
		m.log.Error("Failed to load monitor config", zap.Error(err))
		return
	}

	if len(monitorCfg.Channels) == 0 {
		m.log.Info("No channels configured for monitoring")
		return
	}

	m.log.Info("Channel monitor started", zap.Int("channels", len(monitorCfg.Channels)))

	// Check interval
	interval := monitorCfg.CheckInterval
	if interval == 0 {
		interval = 24 * time.Hour // default to daily
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run initial check
	m.checkAllChannels(ctx, monitorCfg)

	for {
		select {
		case <-ticker.C:
			m.checkAllChannels(ctx, monitorCfg)
		case <-m.stopCh:
			m.log.Info("Channel monitor stopped")
			return
		case <-ctx.Done():
			m.log.Info("Channel monitor stopped via context")
			return
		}
	}
}

// Stop stops the channel monitor
func (m *ChannelMonitor) Stop() {
	close(m.stopCh)
}

// checkAllChannels checks all configured channels for new videos
func (m *ChannelMonitor) checkAllChannels(ctx context.Context, cfg *MonitorConfig) {
	for _, channel := range cfg.Channels {
		m.log.Info("Checking channel", zap.String("url", channel.URL), zap.String("category", channel.Category))
		go m.checkChannel(ctx, channel, cfg)
	}
}

// checkChannel checks a single channel for new videos
func (m *ChannelMonitor) checkChannel(ctx context.Context, channel ChannelConfig, cfg *MonitorConfig) {
	// Use yt-dlp to get channel videos
	args := []string{
		"--flat-playlist",
		"--print", "%(id)s %(title)s %(view_count)s %(duration)s",
		"--playlist-end", "20",
	}

	if channel.MinViews > 0 {
		// yt-dlp doesn't support min-views in flat-playlist mode, we'll filter later
	}

	args = append(args, channel.URL)

	cmd := exec.Command(cfg.YtdlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		m.log.Error("Failed to fetch channel videos", zap.String("url", channel.URL), zap.Error(err))
		return
	}

	// Parse output and process videos
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		m.processVideoLine(ctx, line, channel, cfg)
	}
}

// processVideoLine processes a single video line from yt-dlp output
func (m *ChannelMonitor) processVideoLine(ctx context.Context, line string, channel ChannelConfig, cfg *MonitorConfig) {
	// Parse: video_id title view_count duration
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return
	}

	videoID := parts[0]
	// title is parts[1:-2], view_count is second to last, duration is last
	// This is simplified - actual parsing would be more robust

	m.log.Debug("Found video", zap.String("video_id", videoID))

	// Check if video matches keywords
	if len(channel.Keywords) > 0 {
		title := strings.Join(parts[1:len(parts)-2], " ")
		if !containsAny(title, channel.Keywords) {
			return
		}
	}

	// Download clip if it passes filters
	m.downloadClip(ctx, videoID, channel, cfg)
}

// downloadClip downloads a clip from YouTube
func (m *ChannelMonitor) downloadClip(ctx context.Context, videoID string, channel ChannelConfig, cfg *MonitorConfig) {
	downloadDir := filepath.Join(m.cfg.Storage.DataDir, "downloads", channel.Category)

	args := []string{
		"--output", filepath.Join(downloadDir, "%(title)s.%(ext)s"),
		"--max-filesize", "100M",
	}

	if cfg.CookiesPath != "" {
		args = append(args, "--cookies", cfg.CookiesPath)
	}

	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	args = append(args, videoURL)

	cmd := exec.Command(cfg.YtdlpPath, args...)
	if err := cmd.Run(); err != nil {
		m.log.Error("Failed to download clip", zap.String("video_id", videoID), zap.Error(err))
		return
	}

	m.log.Info("Downloaded clip", zap.String("video_id", videoID), zap.String("category", channel.Category))
}

// loadConfig loads the monitor configuration from file
func (m *ChannelMonitor) loadConfig() (*MonitorConfig, error) {
	configPath := filepath.Join(m.cfg.Storage.DataDir, "channel_monitor_config.json")

	data, err := exec.Command("cat", configPath).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg MonitorConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults
	if cfg.YtdlpPath == "" {
		cfg.YtdlpPath = "yt-dlp"
	}
	if cfg.MaxClipDuration == 0 {
		cfg.MaxClipDuration = 60
	}

	return &cfg, nil
}

// containsAny checks if a string contains any of the keywords
func containsAny(text string, keywords []string) bool {
	lowerText := strings.ToLower(text)
	for _, kw := range keywords {
		if strings.Contains(lowerText, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
