package channelmonitor

import (
	"encoding/json"
	"os"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// LoadConfigFromFile loads monitor configuration from a JSON file
func LoadConfigFromFile(path string) (*MonitorConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg MonitorConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadConfigWithDefaults loads configuration from file, applying defaults for missing values
func LoadConfigWithDefaults(path string) (*MonitorConfig, error) {
	cfg, err := LoadConfigFromFile(path)
	if err != nil {
		// File doesn't exist or is invalid — return defaults
		logger.Info("Channel monitor config file not found, using defaults",
			zap.String("path", path),
			zap.Error(err),
		)
		return DefaultConfig(), nil
	}

	// Apply defaults for zero values
	applyDefaults(cfg)

	return cfg, nil
}

// DefaultConfig returns a MonitorConfig with sensible defaults
func DefaultConfig() *MonitorConfig {
	return &MonitorConfig{
		CheckInterval:   24 * time.Hour,
		VideoTimeframe:  "month",
		ClipRootID:      "",
		ClipRunDBPath:   "",
		YtDlpPath:       "yt-dlp",
		FFmpegPath:      "ffmpeg",
		CookiesPath:     "",
		MaxClipDuration: 60,
		OllamaURL:       "http://localhost:11434",
		Channels:        defaultChannels(),
	}
}

// defaultChannels returns a set of default channel configurations
func defaultChannels() []ChannelConfig {
	return []ChannelConfig{
		{
			URL:             "https://www.youtube.com/@VladimirTsvetov",
			Category:        "HipHop",
			Keywords:        []string{"hip hop", "rap", "drill"},
			MinViews:        10000,
			MaxClipDuration: 60,
		},
		{
			URL:             "https://www.youtube.com/@TMZ",
			Category:        "Discovery",
			Keywords:        []string{"celebrity", "news", "interview"},
			MinViews:        50000,
			MaxClipDuration: 60,
		},
		{
			URL:             "https://www.youtube.com/@WWE",
			Category:        "Wwe",
			Keywords:        []string{"wrestling", "wwe", "match"},
			MinViews:        100000,
			MaxClipDuration: 60,
		},
	}
}

// applyDefaults fills in zero values with defaults
func applyDefaults(cfg *MonitorConfig) {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 24 * time.Hour
	}
	if cfg.VideoTimeframe == "" {
		cfg.VideoTimeframe = "month"
	}
	if cfg.YtDlpPath == "" {
		cfg.YtDlpPath = "yt-dlp"
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	if cfg.MaxClipDuration == 0 {
		cfg.MaxClipDuration = 60
	}
	if cfg.OllamaURL == "" {
		cfg.OllamaURL = "http://localhost:11434"
	}
	if len(cfg.Channels) == 0 {
		cfg.Channels = defaultChannels()
	}

	// Apply defaults to each channel
	for i := range cfg.Channels {
		if cfg.Channels[i].MaxClipDuration == 0 {
			cfg.Channels[i].MaxClipDuration = cfg.MaxClipDuration
		}
	}
}

// SaveConfig saves the monitor configuration to a JSON file
func SaveConfig(path string, cfg *MonitorConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
