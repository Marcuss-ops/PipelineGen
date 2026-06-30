// Package monitor — discovery.go: cheap per-video discovery + filter chain.
//
// Step 9 (June 2026, Channel Monitor Blocco 6 architectural rewrite):
// the package is now exactly 5 production files (scheduler.go,
// discovery.go, analyzer.go, enqueue.go, ports.go). This file owns:
//
//   - discoverChannelVideos: a thin wrapper over the MonitorDownloaderPort
//     that decides the per-channel PlaylistEnd (DefaultPlaylistEnd fallback).
//   - discoverSearchQueries: a STUB that returns nil. The pre-Step-9
//     search_queries.go was deleted earlier in the working tree; the
//     signature is preserved here for type stability across future P1
//     search-query-driven re-introduction.
//   - checkChannel: orchestrates the per-cycle per-channel check. List
//     videos → fan out bounded goroutines → per-video processVideo runs
//     the cheap lexical filter chain. Failure here drives the
//     exponential backoff curve in scheduler.nextCheckTime.
//   - processVideo: per-video dispatch. Cheap lexical filters (MinViews /
//     MaxClipDuration / title-keyword) live here; the AI gate (analyzer.go)
//     and the durable-job emission (enqueue.go) live elsewhere.
//
// This file NEVER imports os/exec, the OllamaClient, or VTT regex
// helpers — those concerns moved out under the TranscriptProvider /
// VideoAnalyzer / JobEnqueuer ports.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// effectivePlaylistEnd picks the playlist-end limit for the caller's
// yt-dlp invocation: per-channel override > global default > 0 (which
// means "no limit").
func effectivePlaylistEnd(channel channels.Channel, globalDefault int) int {
	if channel.PlaylistEnd > 0 {
		return channel.PlaylistEnd
	}
	if channel.PlaylistEnd == 0 {
		return 0
	}
	return globalDefault
}

// discoverChannelVideos returns the unfiltered list of videos for a
// channel. Pure read call (no AI, no subprocess); cheap lexical filtering
// happens per-video in checkChannel's worker loop. Returns (videos, err)
// where err is non-nil ONLY for yt-dlp infra failures (subprocess / parse).
//
// A nil ytdlp port surfaces as a loud error here rather than a panic
// because nothing further down can succeed without a real ListChannel
// result — failing fast at the discovery layer avoids silent zero-result
// channels that would otherwise be reported as Success=true.
func (m *ChannelMonitor) discoverChannelVideos(ctx context.Context, channel channels.Channel) ([]downloader.VideoInfo, error) {
	if m.ytdlp == nil {
		return nil, fmt.Errorf("discoverChannelVideos: ytdlp port not wired")
	}
	playlistEnd := effectivePlaylistEnd(channel, DefaultPlaylistEnd)
	return m.ytdlp.ListChannel(ctx, channel.ChannelURL, playlistEnd)
}

// QueryResult is the canonical return-type for the (currently stubbed)
// discoverSearchQueries. Defined for cross-Step-9 type stability: any
// future P1 PR re-introducing search-query-driven discovery must
// return QueryResult, not invent a new shape.
type QueryResult struct {
	Query  string
	Videos []downloader.VideoInfo
}

// discoverSearchQueries is a Step-9 STUB. The previous search_queries.go
// (channel-by-search-query discovery) was removed earlier in the working
// tree as part of the Wave A simplification; this stub keeps the signature
// in the type system so a future P1 PR can fill it in without changing
// the public API. Returns (nil, nil) so existing callers (none currently
// — this is purely a future-proofing step) can no-op safely.
func (m *ChannelMonitor) discoverSearchQueries(_ context.Context, _ string) ([]QueryResult, error) {
	m.log.Debug("discoverSearchQueries: stub (search-query-driven discovery not re-introduced in Step 9; see P1 follow-up ticket)")
	return nil, nil
}

// checkChannel runs the per-cycle loop with bounded concurrency.
//
// Returns (ChannelCheckResult, error):
//   - err is non-nil ONLY when the yt-dlp structured listing itself failed
//     (network, parse, subprocess error, etc.). In-process filter rejections
//     (below min_views, title-keyword miss, semantic budget exhaustion,
//     semantic score below threshold) count toward VideosSkipped and
//     produce a nil error: the check itself succeeded; those rejections
//     are policy, not infra. Conflating them would trigger a backoff
//     after every policy rejection, which is wrong.
//   - VideosSkipped = VideosDiscovered - VideosEnqueued: covers early
//     loop breakouts (MaxVideosPerRun reached), in-flight MaxVideosPerRun
//     rejections, and processVideo's filter chain.
//
// **Soft-signal caveat**: VideosEnqueued reads processVideo's
// acceptedCount.Load(), which counts *tryReserve successes* (the
// per-channel MaxVideosPerRun budget slot consumption), not the
// jobs actually posted to the broker. If processVideo's tail
// (enqueueFromAnalysis) later rejects internally — nil enqueuer, zero
// interesting-segments, marshal failure, JobEnqueuer.EnqueueExtract
// error, ActiveKey collision — the slot has been consumed but no job
// was posted. Operators should treat VideosEnqueued as a *\"videos
// passed through the per-channel filter chain and were permitted to
// enter the extraction pipeline\"* signal, not a hard \"jobs accepted
// by the broker\" count. Tightening the contract to wire
// enqueueFromAnalysis's tail success back into acceptedCount is
// tracked for Blocco 7 (job emit unification).
func (m *ChannelMonitor) checkChannel(ctx context.Context, channel channels.Channel) (ChannelCheckResult, error) {
	videos, err := m.discoverChannelVideos(ctx, channel)
	if err != nil {
		m.log.Error("Failed to fetch channel videos", zap.String("url", channel.ChannelURL), zap.Error(err))
		return ChannelCheckResult{}, fmt.Errorf("list channel %q: %w", channel.ChannelURL, err)
	}

	m.log.Info("Fetched channel videos", zap.String("url", channel.ChannelURL), zap.Int("count", len(videos)))

	concurrency := 5
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var acceptedCount atomic.Int32

	for _, video := range videos {
		video := video
		if channel.MaxVideosPerRun > 0 && acceptedCount.Load() >= int32(channel.MaxVideosPerRun) {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if r := recover(); r != nil {
				m.log.Error("panic in video processing worker", zap.Any("recover", r), zap.String("video_id", video.ID))
			}
			if channel.MaxVideosPerRun > 0 && acceptedCount.Load() >= int32(channel.MaxVideosPerRun) {
				return
			}
			m.processVideo(ctx, video, channel, &acceptedCount)
		}()
	}
	wg.Wait()

	enqueued := int(acceptedCount.Load())
	return ChannelCheckResult{
		VideosDiscovered: len(videos),
		VideosEnqueued:   enqueued,
		VideosSkipped:    len(videos) - enqueued,
	}, nil
}

// processVideo runs the per-video flow:
//   - cheap lexical filter chain (MinViews → MaxClipDuration → title-keyword)
//     — early-returns without consuming a budget slot.
//   - analyzer gate (delegated to analyzer.go via TranscriptProvider +
//     VideoAnalyzer ports). On skip/error, the slot is NOT consumed.
//   - tryReserve (atomic CAS) — only after passing the AI gate.
//   - enqueueFromAnalysis (delegated to enqueue.go via the JobEnqueuer port).
func (m *ChannelMonitor) processVideo(ctx context.Context, info downloader.VideoInfo, channel channels.Channel, acceptedCount *atomic.Int32) {
	videoID := info.ID
	title := info.Title
	m.log.Debug("Found video", zap.String("video_id", videoID), zap.String("title", title))

	// ── Cheap lexical filters (Step 9: stays here) ────────────────────
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

	// ── Keyword filter (cheap; runs BEFORE the AI gate) ──────────────
	keywords := decodeJSONStrings(channel.Keywords)
	if len(keywords) > 0 {
		if !containsAny(title, keywords) {
			m.log.Debug("title keyword no match, skipping",
				zap.String("video_id", videoID),
				zap.Strings("keywords", keywords))
			return
		}
		m.log.Debug("title keyword match", zap.String("video_id", videoID))
	}

	// ── Semantic gate — delegated to analyzer.go (Step 9) ─────────────
	semanticKeywords := decodeJSONStrings(channel.SemanticKeywords)
	analysis, analyzeErr := m.analyzeVideo(ctx, info, channel, semanticKeywords)
	if analyzeErr != nil {
		m.log.Warn("analyzeVideo failed, skipping video",
			zap.String("video_id", videoID),
			zap.Error(analyzeErr))
		return
	}
	if len(analysis.Segments) == 0 {
		// analyzer.go already logged the skip reason (no segments,
		// score below threshold, transcript miss). Cheap early-out.
		return
	}

	// ── MaxVideosPerRun budget reserve (after AI gate) ────────────────
	if acceptedCount != nil && channel.MaxVideosPerRun > 0 {
		if !tryReserve(acceptedCount, channel.MaxVideosPerRun) {
			m.log.Debug("max_videos_per_run reached, skipping",
				zap.String("video_id", videoID),
				zap.Int("max", channel.MaxVideosPerRun))
			return
		}
	}

	// ── Per-video Prometheus observation (been missing since pre-Step-9) ──
	// Old process_video.go:145 incremented ChannelMonitorVideosChecked here,
	// between tryReserve success and enqueueClipExtract. Restoring the
	// observability series the operators watch during a Playbook run.
	channelHandle := extractChannelHandle(channel.ChannelURL)
	if channelHandle == "" {
		channelHandle = "unknown"
	}
	metrics.ChannelMonitorVideosChecked.WithLabelValues(channelHandle).Inc()

	// ── Enqueue (delegates to enqueue.go / JobEnqueuer port) ──────────
	m.enqueueFromAnalysis(ctx, info, channel, analysis)
}

// decodeJSONStrings decodes a JSON-encoded string array (as stored in
// the channels.Channel DTO's Keywords/SemanticKeywords fields) into a
// Go []string. Returns nil if the input is empty or unparseable.
func decodeJSONStrings(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// containsAny checks if a string contains any of the keywords (case-sensitive).
// Cheap O(n*m) loop — for the typical channel-keywords list length (<20) and
// title length (<200 chars) this is dominated by the kernel time of an
// yt-dlp subprocess by 6+ orders of magnitude.
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
