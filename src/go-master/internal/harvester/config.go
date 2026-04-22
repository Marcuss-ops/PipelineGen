package harvester

import (
	"encoding/json"
	"os"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

func LoadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func LoadConfigWithDefaults(path string) (*Config, error) {
	cfg, err := LoadConfigFromFile(path)
	if err != nil {
		logger.Info("Harvester config file not found, using defaults",
			zap.String("path", path),
			zap.Error(err),
		)
		return DefaultConfig(), nil
	}

	applyDefaults(cfg)
	return cfg, nil
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:            true,
		CheckInterval:      1 * time.Hour,
		SearchQueries:      []string{"interview", "highlights", "documentary"},
		Channels:           []string{},
		MaxResultsPerQuery: 20,
		MinViews:           10000,
		Timeframe:          "month",
		MaxConcurrentDls:   3,
		DownloadDir:        "./downloads",
		ProcessClips:       true,
	}
}

func applyDefaults(cfg *Config) {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 1 * time.Hour
	}
	if len(cfg.SearchQueries) == 0 {
		cfg.SearchQueries = []string{"interview", "highlights", "documentary"}
	}
	if cfg.MaxResultsPerQuery == 0 {
		cfg.MaxResultsPerQuery = 20
	}
	if cfg.MinViews == 0 {
		cfg.MinViews = 10000
	}
	if cfg.Timeframe == "" {
		cfg.Timeframe = "month"
	}
	if cfg.MaxConcurrentDls == 0 {
		cfg.MaxConcurrentDls = 3
	}
	if cfg.DownloadDir == "" {
		cfg.DownloadDir = "./downloads"
	}
}

func SaveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}