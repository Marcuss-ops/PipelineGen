package harvester

import (
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

func (h *Harvester) AddQuery(query string) {
	h.config.SearchQueries = append(h.config.SearchQueries, query)
	logger.Info("Added query", zap.String("query", query))
}

func (h *Harvester) AddChannel(channel string) {
	h.config.Channels = append(h.config.Channels, channel)
	logger.Info("Added channel", zap.String("channel", channel))
}

func (h *Harvester) RemoveQuery(query string) {
	for i, q := range h.config.SearchQueries {
		if q == query {
			h.config.SearchQueries = append(h.config.SearchQueries[:i], h.config.SearchQueries[i+1:]...)
			logger.Info("Removed query", zap.String("query", query))
			return
		}
	}
}

func (h *Harvester) RemoveChannel(channel string) {
	for i, c := range h.config.Channels {
		if c == channel {
			h.config.Channels = append(h.config.Channels[:i], h.config.Channels[i+1:]...)
			logger.Info("Removed channel", zap.String("channel", channel))
			return
		}
	}
}

func (h *Harvester) BlacklistVideo(videoID, reason string, score float64) {
	h.blacklist = append(h.blacklist, BlacklistRecord{
		VideoID:       videoID,
		Reason:        reason,
		Score:         score,
		BlacklistedAt: time.Now(),
	})
	logger.Info("Video blacklisted", zap.String("video_id", videoID), zap.String("reason", reason))
}

func (h *Harvester) UnblacklistVideo(videoID string) {
	for i, b := range h.blacklist {
		if b.VideoID == videoID {
			h.blacklist = append(h.blacklist[:i], h.blacklist[i+1:]...)
			logger.Info("Video unblacklisted", zap.String("video_id", videoID))
			return
		}
	}
}

func (h *Harvester) isBlacklisted(videoID string) bool {
	for _, b := range h.blacklist {
		if b.VideoID == videoID {
			return true
		}
	}
	return false
}

func (h *Harvester) GetBlacklist() []BlacklistRecord {
	return h.blacklist
}

func (h *Harvester) GetQueries() []string {
	return h.config.SearchQueries
}

func (h *Harvester) GetChannels() []string {
	return h.config.Channels
}

func (h *Harvester) GetResults() <-chan HarvestResult {
	return h.resultCh
}

func (h *Harvester) GetStats() map[string]int {
	return map[string]int{
		"workers":     h.config.MaxConcurrentDls,
		"queries":     len(h.config.SearchQueries),
		"channels":    len(h.config.Channels),
		"blacklist":   len(h.blacklist),
		"downloading": len(h.downloadCh),
	}
}
