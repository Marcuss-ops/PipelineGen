package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Start begins the channel monitoring process
func (m *ChannelMonitor) Start(ctx context.Context) {
	m.log.Info("Starting channel monitor")

	monitorCfg, err := m.loadConfig()
	if err != nil {
		m.log.Error("Failed to load monitor config", zap.Error(err))
		return
	}

	dbChannels, err := m.loadDBChannels(ctx)
	if err != nil {
		m.log.Warn("Failed to load category channels from DB", zap.Error(err))
	}

	var allChannels []ChannelConfig
	if len(dbChannels) > 0 {
		allChannels = dbChannels
		m.log.Info("Using DB channels as primary source", zap.Int("count", len(dbChannels)))
	} else if len(monitorCfg.Channels) > 0 {
		allChannels = monitorCfg.Channels
		m.log.Info("No DB channels, falling back to JSON config", zap.Int("count", len(monitorCfg.Channels)))
	}

	if len(allChannels) == 0 {
		m.log.Info("No channels configured for monitoring")
		return
	}

	monitorCfg.Channels = allChannels
	m.ensureChannelFolders(ctx, monitorCfg.Channels)

	m.log.Info("Channel monitor started", zap.Int("total_channels", len(monitorCfg.Channels)))

	globalInterval := monitorCfg.CheckInterval
	if globalInterval == 0 {
		globalInterval = 24 * time.Hour
	}

	sortedChannels := make([]ChannelConfig, len(monitorCfg.Channels))
	copy(sortedChannels, monitorCfg.Channels)
	sort.Slice(sortedChannels, func(i, j int) bool {
		return sortedChannels[i].EffectivePriority() < sortedChannels[j].EffectivePriority()
	})

	m.checkAllChannels(ctx, monitorCfg, sortedChannels)

	var wg sync.WaitGroup
	for _, channel := range monitorCfg.Channels {
		channel := channel
		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					m.log.Error("panic in channel monitor scheduler goroutine", zap.Any("recover", r), zap.String("url", channel.URL))
				}
			}()
			defer wg.Done()

			interval := globalInterval
			if channel.CheckInterval > 0 {
				interval = channel.CheckInterval
			}
			switch channel.EffectivePriority() {
			case PriorityHot:
				interval = interval / 2
			case PriorityCold:
				interval = interval * 2
			}
			if interval < time.Minute {
				interval = time.Minute
			}

			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					m.log.Debug("per-channel check", zap.String("url", channel.URL), zap.String("category", channel.Category), zap.Duration("interval", interval))
					go func(ch ChannelConfig) {
						defer func() {
							if r := recover(); r != nil {
								m.log.Error("panic in channel check goroutine", zap.Any("recover", r), zap.String("url", ch.URL))
							}
						}()
						m.globalSem <- struct{}{}
						defer func() { <-m.globalSem }()
						checkCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
						defer cancel()
						m.checkChannel(checkCtx, ch, monitorCfg)
					}(channel)
				case <-m.stopCh:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	if m.searchQueriesRepo != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					m.log.Error("panic in search queries loop goroutine", zap.Any("recover", r))
				}
			}()
			m.searchQueriesLoop(ctx)
		}()
	}

	wg.Wait()
}

// checkAllChannels checks all configured channels for new videos.
func (m *ChannelMonitor) checkAllChannels(ctx context.Context, cfg *MonitorConfig, sortedChannels []ChannelConfig) {
	for _, channel := range sortedChannels {
		ch := channel
		go func() {
			defer func() {
				if r := recover(); r != nil {
					m.log.Error("panic in initial channel check goroutine", zap.Any("recover", r), zap.String("url", ch.URL))
				}
			}()
			m.globalSem <- struct{}{}
			defer func() { <-m.globalSem }()
			m.checkChannel(ctx, ch, cfg)
		}()
	}
}

// loadConfig loads monitor config from JSON file (fallback to defaults).
func (m *ChannelMonitor) loadConfig() (*MonitorConfig, error) {
	configPath := "config/channel_monitor_config.json"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configPath = filepath.Join(m.cfg.Storage.DataDir, "channel_monitor_config.json")
	}

	var cfg MonitorConfig
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			m.log.Warn("failed to parse monitor config, using defaults", zap.Error(err))
		}
	} else {
		m.log.Info("no JSON config found, using DB-only mode")
	}

	if cfg.YtdlpPath == "" {
		cfg.YtdlpPath = m.cfg.External.ResolvedYtdlpPath()
	}
	if cfg.MaxClipDuration == 0 {
		cfg.MaxClipDuration = 60
	}
	if cfg.PlaylistEnd == 0 {
		cfg.PlaylistEnd = 20
	}
	if cfg.MaxFilesize == "" {
		cfg.MaxFilesize = "100M"
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 24 * time.Hour
	}

	return &cfg, nil
}

// containsAnyCaseInsensitive checks if a string contains any of the keywords, case-insensitively.
func containsAnyCaseInsensitive(text string, keywords []string) bool {
	for _, kw := range keywords {
		if containsAny(text, []string{kw}) {
			return true
		}
	}
	return false
}

// effectivePlaylistEnd standalone function (used by checkChannel).
func effectivePlaylistEndFunc(channel ChannelConfig, globalDefault int) int {
	if channel.PlaylistEnd == 0 {
		return globalDefault
	}
	return channel.PlaylistEnd
}
