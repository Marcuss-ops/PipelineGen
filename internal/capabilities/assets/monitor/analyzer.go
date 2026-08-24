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

	channels "github.com/Marcuss-ops/PipelineGen/internal/capabilities/channels"
)

// analyzeVideo runs the AI gate for a single video using the one-shot
// Fetch + AnalyzeFull flow (cutover completed Step 6, June 2026).
//
// Returns:
//   - (Analysis{}, err) — a hard failure (transcript unavailable,
//     AnalyzeFull error). Caller logs + skips.
//   - (Analysis{Segments: nil}, nil) — soft skip: score below threshold,
//     no segments met the duration cut, etc. Caller cheap-logs + skips
//     without consuming a budget slot.
//   - (Analysis{Score, MatchedKeyword, Category, Segments}, nil) — full
//     success: enqueueFromAnalysis receives this.
//
// The legacy GetTranscript + Score + Classify + FindSegments flow was
// removed in Step 6. The one-shot flow calls Fetch ONCE per video
// (yt-dlp subprocess at most once) and AnalyzeFull ONCE (single Ollama
// JSON call), reducing per-video latency by 2-3×.
func (m *ChannelMonitor) analyzeVideo(ctx context.Context, info VideoInfo, channel channels.Channel, semanticKeywords []string) (Analysis, error) {
	videoID := info.ID
	title := info.Title
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// ── Step 1: Fetch transcript (cheap if cached; download otherwise) ──
	if m.transcript == nil {
		return Analysis{}, fmt.Errorf("analyzeVideo: transcript provider not wired")
	}
	doc, err := m.transcript.Fetch(ctx, videoURL)
	if err != nil {
		return Analysis{}, fmt.Errorf("analyzeVideo: Fetch(%s): %w", videoID, err)
	}

	// ── Step 2: One-shot AnalyzeFull (Score + Classify + FindSegments) ──
	if m.analyzer == nil {
		return Analysis{}, fmt.Errorf("analyzeVideo: analyzer not wired")
	}
	opts := AnalyzeOptions{
		SemanticKeywords: semanticKeywords,
		CategoryFallback: channel.Category,
		MaxSegments:      channel.MaxSegments,
		SegmentPrompt:    channel.SegmentPrompt,
		MinScore:         semanticScoreThreshold(channel.MinSemanticScore),
	}
	// Default MaxSegments when unset (pre-Step-9 behaviour).
	if opts.MaxSegments <= 0 {
		opts.MaxSegments = 3
	}
	// Only disable the score gate when NO semantic keywords are
	// configured — Analytics agree this is the correct optimization
	// for "wide net" channels.
	if len(semanticKeywords) == 0 {
		opts.MinScore = 0
	}

	analysis, analyzeErr := m.analyzer.AnalyzeFull(ctx, doc, opts)
	if analyzeErr != nil {
		return Analysis{}, fmt.Errorf("analyzeVideo: AnalyzeFull(%s): %w", videoID, analyzeErr)
	}

	// ── Step 3: Check segments — soft-skip if empty ───────────────────
	segments := analysis.Segments

	// Metrics observations — FASE 3.7 Commit 2 (2026-07-04):
	// emit via the typed m.metrics port (declared in
	// internal/application/assets/monitor/ports_metrics.go) instead
	// of the legacy `internal/platform/observability`
	// package-level vars. The composition root wires an
	// *observability.ObservabilityMetricsRecorder adapter; tests
	// + partial-deploy paths get a NoopMetricsRecorder default so
	// these calls are always safe.
	channelHandle := extractChannelHandle(channel.ChannelURL)
	metricsLabel := channelHandle
	if metricsLabel == "" {
		metricsLabel = "unknown"
	}
	// godlike/07: nil metrics port → no-op (matches package docstring contract)
	if m.metrics != nil {
		m.metrics.ObserveSegmentsPerVideo(metricsLabel, len(segments))
	}

	if len(segments) == 0 {
		m.log.Info("no interesting segments found, skipping video",
			zap.String("video_id", videoID),
			zap.String("title", title))
		return Analysis{}, nil
	}

	if m.metrics != nil {
		m.metrics.IncVideosWithSegments(metricsLabel)
		m.metrics.AddSegmentsFound(metricsLabel, len(segments))
	}

	// ── Step 4: Prefix category to segment names ──────────────────────
	// So the extraction pipeline downstream renders "Comedy: Funny bit
	// about X" instead of bare "Funny bit about X" (pre-Step-9 behaviour).
	category := analysis.Category
	if category == "" {
		category = channel.Category
	}
	for idx := range segments {
		segments[idx].Name = category + " " + segments[idx].Name
	}

	return Analysis{
		Category:       category,
		Score:          analysis.Score,
		MatchedKeyword: analysis.MatchedKeyword,
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
