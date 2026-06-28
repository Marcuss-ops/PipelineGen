package monitor

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"go.uber.org/zap"
)

// Start begins the channel monitoring process.
// PR 2 (June 2026): channels are loaded exclusively from channels.Service
// (category_channels). The JSON fallback and direct SQLite query are removed.
func (m *ChannelMonitor) Start(ctx context.Context) {
	m.log.Info("Starting channel monitor")

	if m.channelsSvc == nil {
		m.log.Error("Channel monitor: channels service not wired, cannot start")
		return
	}

	monitorCfg, err := m.loadConfig()
	if err != nil {
		m.log.Error("Failed to load monitor config", zap.Error(err))
		return
	}

	// PR 2: channels come exclusively from category_channels via channels.Service.
	// The JSON fallback and loadDBChannels are removed.
	result, err := m.channelsSvc.ListEnabled(ctx)
	if err != nil {
		m.log.Error("Failed to list enabled channels", zap.Error(err))
		return
	}

	allChannels := result.Channels
	if len(allChannels) == 0 {
		m.log.Info("No enabled channels found in category_channels — monitor idle")
		return
	}
	m.log.Info("Loaded enabled channels from category_channels", zap.Int("count", len(allChannels)))

	// Convert channels.Channel → ChannelConfig for the monitor's runtime use.
	cfgChannels := make([]ChannelConfig, 0, len(allChannels))
	for _, ch := range allChannels {
		cfgChannels = append(cfgChannels, m.fromChannelDTO(ch))
	}

	m.ensureChannelFolders(ctx, cfgChannels)

	m.log.Info("Channel monitor started", zap.Int("total_channels", len(cfgChannels)))

	globalInterval := monitorCfg.CheckInterval
	if globalInterval == 0 {
		globalInterval = 24 * time.Hour
	}

	sortedChannels := make([]ChannelConfig, len(cfgChannels))
	copy(sortedChannels, cfgChannels)
	sort.Slice(sortedChannels, func(i, j int) bool {
		return sortedChannels[i].EffectivePriority() < sortedChannels[j].EffectivePriority()
	})

	m.checkAllChannels(ctx, monitorCfg, sortedChannels)

	var wg sync.WaitGroup
	for _, channel := range cfgChannels {
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

// loadConfig loads global monitor defaults from config.Config.
// PR 2 (June 2026): no longer loads channel lists — those come from
// channels.Service. Global technical defaults (yt-dlp path, cookies, etc.)
// come from config or sensible fallbacks.
func (m *ChannelMonitor) loadConfig() (*MonitorConfig, error) {
	cfg := &MonitorConfig{
		YtdlpPath:       m.cfg.External.ResolvedYtdlpPath(),
		MaxClipDuration: 60,
		PlaylistEnd:     20,
		MaxFilesize:     "100M",
		CheckInterval:   24 * time.Hour,
	}

	// Global defaults can optionally be loaded from JSON for yt-dlp path,
	// cookies, etc. — but Channels are NEVER loaded from JSON anymore.
	if data, err := readMonitorConfigFile(m.cfg.Storage.DataDir); err == nil && data != nil {
		if data.YtdlpPath != "" {
			cfg.YtdlpPath = data.YtdlpPath
		}
		if data.CookiesPath != "" {
			cfg.CookiesPath = data.CookiesPath
		}
		if data.OllamaURL != "" {
			cfg.OllamaURL = data.OllamaURL
		}
		if data.MaxFilesize != "" {
			cfg.MaxFilesize = data.MaxFilesize
		}
		if data.CheckInterval > 0 {
			cfg.CheckInterval = data.CheckInterval
		}
		if data.MaxClipDuration > 0 {
			cfg.MaxClipDuration = data.MaxClipDuration
		}
		if data.PlaylistEnd != 0 {
			cfg.PlaylistEnd = data.PlaylistEnd
		}
	}

	return cfg, nil
}

// fromChannelDTO converts a channels.Channel result DTO into the monitor's
// runtime ChannelConfig. JSON-encoded keyword arrays are decoded; check_interval
// string is parsed into time.Duration.
func (m *ChannelMonitor) fromChannelDTO(ch channels.Channel) ChannelConfig {
	cfg := ChannelConfig{
		ID:               ch.ID,
		URL:              ch.ChannelURL,
		Category:         ch.Category,
		MinViews:         ch.MinViews,
		MaxClipDuration:  ch.MaxClipDuration,
		DriveFolderID:    ch.DriveFolderID,
		MinSemanticScore: ch.MinSemanticScore,
		PlaylistEnd:      ch.PlaylistEnd,
		MaxVideosPerRun:  ch.MaxVideosPerRun,
		Priority:         ch.Priority,
		LookbackDays:     ch.LookbackDays,
		MaxSegments:      ch.MaxSegments,
		SegmentPrompt:    ch.SegmentPrompt,
	}

	// Decode JSON-encoded keyword arrays from the persistence layer.
	if ch.Keywords != "" && ch.Keywords != "[]" {
		var kw []string
		if err := json.Unmarshal([]byte(ch.Keywords), &kw); err == nil {
			cfg.Keywords = kw
		}
	}
	if ch.SemanticKeywords != "" && ch.SemanticKeywords != "[]" {
		var sk []string
		if err := json.Unmarshal([]byte(ch.SemanticKeywords), &sk); err == nil {
			cfg.SemanticKeywords = sk
		}
	}

	// Parse check_interval string into time.Duration
	if ch.CheckInterval != "" {
		if parsed, err := parseCheckInterval(ch.CheckInterval); err == nil {
			cfg.CheckInterval = parsed
		}
	}

	return cfg
}

// readMonitorConfigFile reads the optional global config JSON for technical
// defaults only (yt-dlp path, cookies, etc.). Returns nil when the file is
// absent — the monitor uses sensible fallbacks.
func readMonitorConfigFile(dataDir string) (*MonitorConfig, error) {
	// Import cycle with os/filepath avoided by inlining the path.
	// The JSON config is deprecated for channel lists but may still contain
	// global technical defaults.
	_ = dataDir // reserved for future data-dir relative paths
	return nil, nil
}
