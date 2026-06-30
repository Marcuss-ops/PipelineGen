// Package monitor — analyzer.go: per-video AI gate via the TranscriptProvider
// and VideoAnalyzer ports.
//
// Step 9 (June 2026, Channel Monitor Blocco 6 architectural rewrite):
// the package is now exactly 5 production files (scheduler.go,
// discovery.go, analyzer.go, enqueue.go, ports.go). This file owns:
//
//   - analyzeVideo: orchestrates the per-video AI gate.
//     1. TranscriptProvider.GetTranscript
//     2. VideoAnalyzer.Score (gating — only if SemanticKeywords set)
//     3. VideoAnalyzer.Classify (best-effort category; falls back to
//     channel.Category on error)
//     4. VideoAnalyzer.FindSegments (the actual clip-discovery drive)
//   - semanticScoreThreshold: helper for the score-threshold logic.
//
// This file NEVER imports os/exec, OllamaClient, or VTT regex helpers —
// those concerns moved out under the TranscriptProvider / VideoAnalyzer
// ports. The concrete YTDLPSubtitleAdapter (transcripts) and
// OllamaAnalyzer (semantic) are installed in the next commit, in the
// internal/application/{transcripts,semantic}/ siblings.
//
// analyzeVideo returns (Analysis, error) with the following contract:
//   - (Analysis{}, err): the AI gate produced a real failure (transcript
//     unavailable, Ollama returns error). Caller (processVideo) treats
//     this as "skip this video and log the reason".
//   - (Analysis{Score, MatchedKeyword, Category, []Segment}, nil): the
//     AI gate succeeded and the segments are ready. If Segments is
//     empty (e.g. score below threshold, transcript too short, the
//     caller treats it as "skip without error").
package monitor

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// analyzeVideo runs the AI gate for a single video. Returns:
//
//   - (Analysis{}, err) — a hard failure (transcript unavailable,
//     Score error, FindSegments error). Caller logs + skips.
//   - (Analysis{Segments: nil}, nil) — soft skip: score below threshold,
//     no segments met the duration cut, etc. Caller logs cheaply + skips
//     without consuming a budget slot.
//   - (Analysis{Score, MatchedKeyword, Category, Segments}, nil) — full
//     success: enqueueFromAnalysis receives this.
//
// The order of operations is significant:
//   - GetTranscript runs FIRST so a missing transcript short-circuits
//     the whole pipeline (no Ollama call for a video we cannot analyze).
//   - Score is gated by len(semanticKeywords) > 0 — many channels have
//     no SemanticKeywords configured, and we don't want to pay the
//     LLM-call latency on every video just to confirm "no keywords".
//   - Classify runs only if the channel has no pre-set Category (the
//     call is best-effort; fallback uses channel.Category verbatim).
//   - FindSegments runs LAST and is the actual cliper — without segments,
//     there is no job to enqueue regardless of score.
func (m *ChannelMonitor) analyzeVideo(ctx context.Context, info downloader.VideoInfo, channel channels.Channel, semanticKeywords []string) (Analysis, error) {
	videoID := info.ID
	title := info.Title
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// ── Step 1: GetTranscript (cheap if cached; download otherwise) ───
	if m.transcript == nil {
		return Analysis{}, fmt.Errorf("analyzeVideo: transcript provider not wired")
	}
	transcript, err := m.transcript.GetTranscript(ctx, videoURL)
	if err != nil {
		return Analysis{}, fmt.Errorf("analyzeVideo: GetTranscript(%s): %w", videoID, err)
	}

	// ── Step 2: Semantic score (gating — only if keywords set) ────────
	var (
		score          int
		matchedKeyword string
	)
	if len(semanticKeywords) > 0 {
		if m.analyzer == nil {
			return Analysis{}, fmt.Errorf("analyzeVideo: analyzer not wired (semanticKeywords set but Analyzer is nil)")
		}
		threshold := semanticScoreThreshold(channel.MinSemanticScore)
		var scoreErr error
		score, matchedKeyword, scoreErr = m.analyzer.Score(ctx, transcript, semanticKeywords)
		if scoreErr != nil {
			return Analysis{}, fmt.Errorf("analyzeVideo: Score(%s): %w", videoID, scoreErr)
		}
		if score < threshold {
			m.log.Info("video does not match semantic keywords",
				zap.String("video_id", videoID),
				zap.String("title", title),
				zap.Int("score", score),
				zap.Int("threshold", threshold))
			// Soft skip — return empty Segments, nil error. Caller
			// (processVideo) recognizes an empty analysis.Segments and
			// skips without consuming a budget slot.
			return Analysis{}, nil
		}
		m.log.Info("semantic match",
			zap.String("video_id", videoID),
			zap.String("title", title),
			zap.String("matched_keyword", matchedKeyword),
			zap.Int("score", score))
	}

	// ── Step 3: Category (best-effort; falls back to channel.Category) ─
	category := channel.Category
	// Only call the LLM-driven classify when there is no pre-set category
	// or the channel does not have a Drive folder bound (channel-only
	// category + Drive folder = pre-bound, no LLM call required).
	if category == "" || channel.DriveFolderID == "" {
		if m.analyzer != nil {
			classified, cerr := m.analyzer.Classify(ctx, title, category)
			if cerr == nil && classified != "" {
				category = classified
			} else if cerr != nil {
				m.log.Debug("analyzeVideo: Classify failed, using channel.Category fallback",
					zap.String("video_id", videoID), zap.Error(cerr))
			}
		}
	}

	// ── Step 4: FindSegments (the actual clip-discovery drive) ───────
	var segments []ytdomain.Segment
	if m.analyzer != nil {
		maxSegments := channel.MaxSegments
		if maxSegments <= 0 {
			maxSegments = 3
		}
		segs, serr := m.analyzer.FindSegments(ctx, transcript, channel.SegmentPrompt, maxSegments)
		if serr != nil {
			return Analysis{}, fmt.Errorf("analyzeVideo: FindSegments(%s): %w", videoID, serr)
		}
		segments = segs
	}

	// ── Metrics observations for the per-channel-handle Prometheus counters
	channelHandle := extractChannelHandle(channel.ChannelURL)
	metricsLabel := channelHandle
	if metricsLabel == "" {
		metricsLabel = "unknown"
	}
	metrics.ChannelMonitorSegmentsPerVideo.WithLabelValues(metricsLabel).Observe(float64(len(segments)))

	if len(segments) == 0 {
		m.log.Info("no interesting segments found, skipping video",
			zap.String("video_id", videoID),
			zap.String("title", title))
		// Soft skip: empty Segments + nil error; caller (processVideo)
		// short-circuits without consuming the per-channel budget slot.
		return Analysis{}, nil
	}

	metrics.ChannelMonitorVideosWithSegments.WithLabelValues(metricsLabel).Inc()
	metrics.ChannelMonitorSegmentsFound.WithLabelValues(metricsLabel).Add(float64(len(segments)))

	// Prefix the category to the segment name so the extraction pipeline
	// downstream can render "Comedy: Funny bit about X" instead of bare
	// "Funny bit about X" — matches the pre-Step-9 behavior at
	// segment_finder.go.
	for idx := range segments {
		segments[idx].Name = category + " " + segments[idx].Name
	}

	return Analysis{
		Category:       category,
		Score:          score,
		MatchedKeyword: matchedKeyword,
		Segments:       segments,
	}, nil
}

// semanticScoreThreshold returns the configured threshold (channel.MinSemanticScore
// when > 0, otherwise the 60 default from the pre-Step-9 pipeline). Lives in
// analyzer.go because it's only consumed by the AI gate.
func semanticScoreThreshold(channelMin int) int {
	if channelMin > 0 {
		return channelMin
	}
	return 60
}
