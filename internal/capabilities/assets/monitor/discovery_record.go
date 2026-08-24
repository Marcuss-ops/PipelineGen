package assets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
)

// ── processVideo: per-video dispatch (cheap lexical + AI gate + ledger) ─
//
// Extracted from discovery.go per AGENTS.md Pattern 5 (PR-DISCOVERY-SPLIT, July 2026).

func (m *ChannelMonitor) processVideo(ctx context.Context, info VideoInfo, channel channels.Channel, outcomes *outcomeCounters, cycleNow string) {
	videoID := info.ID
	title := info.Title
	m.log.Debug("Found video", zap.String("video_id", videoID), zap.String("title", title))

	// ── Cheap lexical filters ────────────────────────────────────────
	if channel.MinViews > 0 && info.Views < int64(channel.MinViews) {
		m.log.Debug("video below min_views, skipping",
			zap.String("video_id", videoID),
			zap.Int64("views", info.Views),
			zap.Int("min_views", channel.MinViews))
		return
	}

	if channel.MaxClipDuration > 0 && info.Duration > float64(channel.MaxClipDuration) {
		m.log.Debug("video exceeds max_clip_duration, skipping",
			zap.String("video_id", videoID),
			zap.Float64("duration_sec", info.Duration),
			zap.Int("max_duration", channel.MaxClipDuration))
		return
	}

	keywords, decodeErr := decodeJSONStrings(channel.Keywords)
	if decodeErr != nil {
		m.log.Warn("decodeJSONStrings: channel.Keywords is malformed JSON; treating channel as keyword-less for this cycle",
			zap.String("channel_id", channel.ID),
			zap.String("raw_keywords", channel.Keywords),
			zap.Error(decodeErr))
		keywords = nil
	}
	if len(keywords) > 0 {
		if !containsAny(title, keywords) {
			m.log.Debug("title keyword no match, skipping",
				zap.String("video_id", videoID),
				zap.Strings("keywords", keywords))
			return
		}
		m.log.Debug("title keyword match", zap.String("video_id", videoID))
	}

	// ── Semantic gate ─────────────────────────────────────────────────
	semanticKeywords, decodeErr := decodeJSONStrings(channel.SemanticKeywords)
	if decodeErr != nil {
		m.log.Warn("decodeJSONStrings: channel.SemanticKeywords is malformed JSON; treating channel as semantically-keyword-less for this cycle",
			zap.String("channel_id", channel.ID),
			zap.String("raw_semantic_keywords", channel.SemanticKeywords),
			zap.Error(decodeErr))
		semanticKeywords = nil
	}
	analysis, analyzeErr := m.analyzeVideo(ctx, info, channel, semanticKeywords)
	if analyzeErr != nil {
		m.log.Warn("analyzeVideo failed, skipping video",
			zap.String("video_id", videoID),
			zap.Error(analyzeErr))
		outcomes.infraFailures.Add(1)
		return
	}
	if len(analysis.Segments) == 0 {
		return
	}

	// ── MaxVideosPerRun budget reserve ────────────────────────────────
	if outcomes != nil && channel.MaxVideosPerRun > 0 {
		if !tryReserve(&outcomes.budgetUsed, channel.MaxVideosPerRun) {
			m.log.Debug("max_videos_per_run reached, skipping",
				zap.String("video_id", videoID),
				zap.Int("max", channel.MaxVideosPerRun))
			return
		}
	}

	channelHandle := extractChannelHandle(channel.ChannelURL)
	if channelHandle == "" {
		channelHandle = "unknown"
	}
	m.metrics.IncVideosChecked(channelHandle)

	// ── Leader-election INSERT + broker-side dispatch ────────────────
	outcome, ledgerID := m.recordDiscoveryAndClassify(ctx, info, channel, analysis, cycleNow)
	switch outcome {
	case OutcomeAlreadyScheduled:
		outcomes.alreadyScheduled.Add(1)
		outcomes.budgetUsed.Add(-1)
		m.log.Debug("dedupe loss: video already scheduled in a previous cycle; no broker record this cycle",
			zap.String("video_id", videoID),
			zap.String("channel_id", channel.ID))
		return
	case OutcomeRejected:
		outcomes.budgetUsed.Add(-1)
		outcomes.rejected.Add(1)
		outcomes.infraFailures.Add(1)
		m.log.Debug("video rejected post-INSERT", zap.String("video_id", videoID))
		return
	case OutcomeInfraFailure:
		outcomes.budgetUsed.Add(-1)
		outcomes.rejected.Add(1)
		outcomes.infraFailures.Add(1)
		m.log.Warn("video infra failure (ledger unavailable)", zap.String("video_id", videoID))
		return
	case OutcomeEnqueued:
		outcomes.enqueued.Add(1)
		m.log.Info("video enqueued",
			zap.String("video_id", videoID),
			zap.String("title", title),
			zap.Int("segments", len(analysis.Segments)),
			zap.String("ledger_id", ledgerID))
		return
	}
}

// recordDiscoveryAndClassify runs the leader-election TryReserve dance
// and (when won) delegates the canonical broker-side dance to
// enqueue.go (`m.enqueueFromAnalysis`).
//
// Extracted from discovery.go per AGENTS.md Pattern 5.
func (m *ChannelMonitor) recordDiscoveryAndClassify(ctx context.Context, info VideoInfo, channel channels.Channel, analysis Analysis, cycleNow string) (EnqueueOutcome, string) {
	videoID := info.ID
	title := info.Title

	if m.discoveries == nil {
		m.log.Warn("recordDiscoveryAndClassify: discoveries port not wired, classifying as already_scheduled (no dedupe)",
			zap.String("video_id", videoID))
		return OutcomeAlreadyScheduled, ""
	}

	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	id, won, _, err := m.discoveries.TryReserve(ctx, channel.ID, videoID, ChannelMonitorPolicyVersion, videoURL, title, cycleNow)
	if err != nil {
		m.log.Error("recordDiscoveryAndClassify: TryReserve failed, classifying as infra_failure (ledger error)",
			zap.String("video_id", videoID),
			zap.Error(err))
		return OutcomeInfraFailure, ""
	}
	if !won {
		return OutcomeAlreadyScheduled, id
	}

	err = m.enqueueFromAnalysis(ctx, info, channel, analysis, id)
	if err != nil {
		m.log.Warn("recordDiscoveryAndClassify: enqueueFromAnalysis failed",
			zap.String("video_id", videoID),
			zap.String("ledger_id", id),
			zap.Error(err))
		return OutcomeRejected, id
	}
	return OutcomeEnqueued, id
}

// decodeJSONStrings decodes a JSON-encoded string array into a Go []string.
//
// Extracted from discovery.go per AGENTS.md Pattern 5.
func decodeJSONStrings(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		sample := s
		if len(sample) > 200 {
			sample = sample[:200] + "…"
		}
		return nil, fmt.Errorf("decodeJSONStrings: malformed JSON %q: %w", sample, err)
	}
	return out, nil
}

// containsAny checks if a string contains any of the keywords (case-insensitive).
//
// Extracted from discovery.go per AGENTS.md Pattern 5.
func containsAny(text string, keywords []string) bool {
	if len(text) == 0 || len(keywords) == 0 {
		return false
	}
	lowerText := strings.ToLower(text)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" {
			continue
		}
		if strings.Contains(lowerText, kw) {
			return true
		}
	}
	return false
}
