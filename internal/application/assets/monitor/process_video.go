package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	yttypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"

	"go.uber.org/zap"
)

func effectivePlaylistEnd(channel ChannelConfig, globalDefault int) int {
	if channel.PlaylistEnd > 0 {
		return channel.PlaylistEnd
	}
	if channel.PlaylistEnd == 0 {
		// 0 means "all videos" — explicit in DB (default is -1) or JSON.
		// yt-dlp --playlist-end 0 = fetch all videos.
		return 0
	}
	// channel.PlaylistEnd < 0 (e.g. -1 from DB default) → use global default
	return globalDefault
}

// checkChannel checks a single channel for new videos
func (m *ChannelMonitor) processVideoLine(ctx context.Context, line string, channel ChannelConfig, cfg *MonitorConfig, acceptedCount *atomic.Int32) {
	// Parse: video_id title view_count duration
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return
	}

	videoID := parts[0]
	title := strings.Join(parts[1:len(parts)-2], " ")

	m.log.Debug("Found video", zap.String("video_id", videoID), zap.String("title", title))

	// ── Fast filter: keyword match on title ────────────────────────────
	if len(channel.Keywords) > 0 {
		if !containsAny(title, channel.Keywords) {
			m.log.Debug("title keyword no match, skipping",
				zap.String("video_id", videoID),
				zap.Strings("keywords", channel.Keywords))
			return
		}
		m.log.Debug("title keyword match", zap.String("video_id", videoID))
	}

	// ── Dedup: check if this video was already processed ────────────────
	// The clip_folders table stores the actual YouTube video_id independently
	// of the folderSlug (which may use the protagonist name instead).
	// We use GetClipFolderByVideoID() to query by the real video ID.
	// This correctly detects re-runs even when the folder uses a protagonist slug.
	// Essential for full-scan mode (playlist_end=0) to skip already-processed videos.
	if m.clipsRepo != nil {
		existing, err := m.clipsRepo.GetClipFolderByVideoID(ctx, videoID)
		if err == nil && existing != nil {
			m.log.Debug("⏭️  video already processed, skipping",
				zap.String("video_id", videoID),
				zap.String("title", title),
				zap.String("folder", existing.FolderPath),
				zap.Int("existing_clips", existing.ClipCount))
			return
		}
	}

	// ── Semantic filter: transcript-level content matching ──────────────
	// Only for channels with semantic_keywords configured.
	// Downloads subtitles and asks Ollama: "Does this video discuss [theme]?"
	// Score < threshold → skip. Score >= threshold → process.
	if len(channel.SemanticKeywords) > 0 {
		videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
		score, matchedKeyword, err := m.matchSemantically(ctx, videoURL, channel.SemanticKeywords, channel.MinSemanticScore, cfg)
		if err != nil {
			m.log.Warn("semantic matching failed, skipping video",
				zap.String("video_id", videoID),
				zap.Error(err))
			// On error, skip the video to be safe (don't download irrelevant content)
			return
		}
		if score < semanticScoreThreshold(channel.MinSemanticScore) {
			m.log.Info("⏭️  video does not match semantic keywords",
				zap.String("video_id", videoID),
				zap.String("title", title),
				zap.Int("score", score),
				zap.Int("threshold", semanticScoreThreshold(channel.MinSemanticScore)))
			return
		}
		m.log.Info("✅ semantic match",
			zap.String("video_id", videoID),
			zap.String("title", title),
			zap.String("matched_keyword", matchedKeyword),
			zap.Int("score", score))
	}

	// ── MaxVideosPerRun: atomically reserve a slot ──────────────────────
	// Using CompareAndSwap loop to ensure at-most-N videos are downloaded
	// even under parallel goroutines.
	if acceptedCount != nil && channel.MaxVideosPerRun > 0 {
		if !m.tryReserve(acceptedCount, channel.MaxVideosPerRun) {
			m.log.Debug("max_videos_per_run reached, skipping download",
				zap.String("video_id", videoID),
				zap.Int("max", channel.MaxVideosPerRun))
			return
		}
	}

	// Download clip if it passes filters
	channelHandle := extractChannelHandle(channel.URL)
	if channelHandle == "" {
		channelHandle = "unknown"
	}
	metrics.ChannelMonitorVideosChecked.WithLabelValues(channelHandle).Inc()
	m.downloadClip(ctx, videoID, title, channel, cfg)
}

// semanticScoreThreshold returns the minimum score for a semantic match.
// Default: 60 if channel's MinSemanticScore is 0.
func semanticScoreThreshold(channelMin int) int {
	if channelMin > 0 {
		return channelMin
	}
	return 60 // default threshold
}

// extractChannelHandle extracts the @handle from a YouTube channel URL.
// Examples:
//
//	https://www.youtube.com/@ziwe          → "ziwe"
//	https://www.youtube.com/@TeamCoco      → "TeamCoco"
//	https://www.youtube.com/channel/UC...  → "" (fallback, unknown mapping)
func extractChannelHandle(url string) string {
	// Look for @handle pattern
	if idx := strings.LastIndex(url, "@"); idx >= 0 {
		handle := url[idx+1:]
		// Strip trailing slash if present
		handle = strings.TrimRight(handle, "/")
		return handle
	}
	return ""
}

// matchSemantically downloads subtitles for a video and asks Ollama
// if the transcript content matches any of the target keywords/themes.
// Uses a transcript cache to avoid re-downloading VTT files.
// Returns: score (0-100), matched keyword, error.
func (m *ChannelMonitor) downloadClip(ctx context.Context, videoID string, title string, channel ChannelConfig, cfg *MonitorConfig) {
	if m.youtubeSvc == nil {
		m.log.Error("youtubeSvc not initialized in monitor")
		return
	}

	// Surround the extraction call with a recover so a panic from a
	// single bad request does NOT crash the monitor loop. Without this,
	// any un-caught panic in ytextraction (e.g. nil-deref from a mis-wired
	// port that escaped its nil-guard) would tear down the background
	// ticker goroutine and stop the entire monitor. Per ARCHITECTURE.md
	// §7, the monitor path is non-fatal-by-design.
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("channel-monitor: panic recovered during downloadClip; monitor loop continues",
				zap.String("video_id", videoID),
				zap.String("title", title),
				zap.Any("panic", r))
		}
	}()

	// Determine category: use channel's explicit category if DriveFolderID is set (faster, no Ollama call),
	// otherwise fall back to Ollama-based classification.
	var category string
	if channel.DriveFolderID != "" && channel.Category != "" {
		category = channel.Category
		m.log.Info("using channel's configured category (explicit Drive folder)",
			zap.String("video_id", videoID),
			zap.String("category", category),
			zap.String("drive_folder_id", channel.DriveFolderID))
	} else {
		category = m.classifyCategory(ctx, title, channel.Category, cfg)
	}

	// Extract channel handle for per-channel subfolder naming
	channelHandle := extractChannelHandle(channel.URL)

	// Build destination group: if we have a channel handle and a dedicated Drive root,
	// create a per-channel subfolder (e.g. "Comedy/ziwe", "Comedy/TeamCoco")
	destinationGroup := category
	localSubDir := category
	if channelHandle != "" && channel.DriveFolderID != "" {
		destinationGroup = channelHandle
		localSubDir = filepath.Join(category, channelHandle)
	}

	// Ensure local directory exists
	localDir := filepath.Join(m.cfg.Storage.DataDir, "media", "clips", localSubDir)
	m.log.Info("resolving destination directory",
		zap.String("video_id", videoID),
		zap.String("title", title),
		zap.String("category", category),
		zap.String("channel_handle", channelHandle),
		zap.String("destination_group", destinationGroup),
		zap.String("path", localDir))

	if err := os.MkdirAll(localDir, 0755); err != nil {
		m.log.Warn("failed to pre-create destination directory", zap.String("dir", localDir), zap.Error(err))
	}

	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// Extract interesting segments using Ollama/Gemma
	// Uses per-channel settings: maxSegments and segmentPrompt customize
	// how many segments to extract and what to focus on.
	maxSegments := channel.MaxSegments
	if maxSegments <= 0 {
		maxSegments = 3 // default
	}
	segments := m.findInterestingSegments(ctx, videoURL, cfg, maxSegments, channel.SegmentPrompt)

	// Track segment metrics per channel
	metricsLabel := channelHandle
	if metricsLabel == "" {
		metricsLabel = "unknown"
	}
	metrics.ChannelMonitorSegmentsPerVideo.WithLabelValues(metricsLabel).Observe(float64(len(segments)))

	if len(segments) == 0 {
		m.log.Info("no interesting segments found, skipping video",
			zap.String("video_id", videoID),
			zap.String("title", title))
		return
	}

	// Track videos with at least one segment and total segments found
	metrics.ChannelMonitorVideosWithSegments.WithLabelValues(metricsLabel).Inc()
	metrics.ChannelMonitorSegmentsFound.WithLabelValues(metricsLabel).Add(float64(len(segments)))

	// Prepended category label to the segment names
	for idx := range segments {
		segments[idx].Name = category + " " + segments[idx].Name
	}

	// Resolve Drive folder: use channel's explicit FolderID, or fall back to configured ClipsRootFolder.
	driveFolderID := channel.DriveFolderID
	if driveFolderID == "" && m.cfg != nil {
		driveFolderID = m.cfg.Drive.ClipsFolder()
	}

	// ── Per-channel subfolder on Drive ───────────────────────────────────
	// Create (or find) a per-channel folder inside the Drive root.
	// E.g. "Comedy Root/ziwe/", "Comedy Root/TeamCoco/".
	// Then use THAT folder's ID for the extract request, so clips go into
	// the channel folder rather than directly into the root.
	if channelHandle != "" && driveFolderID != "" {
		channelFolderID, folderErr := m.youtubeSvc.GetOrCreateChannelFolder(ctx, channelHandle, driveFolderID)
		if folderErr == nil && channelFolderID != "" && channelFolderID != driveFolderID {
			m.log.Info("using per-channel Drive subfolder for clips",
				zap.String("channel", channelHandle),
				zap.String("folder_id", channelFolderID))
			driveFolderID = channelFolderID
		}
	}

	req := &yttypes.ExtractRequest{
		URL:      videoURL,
		Segments: segments,
		Destination: &yttypes.DestinationRequest{
			Group:    category,
			FolderID: driveFolderID,
		},
	}

	// Add proper defaults to extraction request
	normalize := true
	req.Normalize = &normalize

	// Extract & upload via the orchestrator's Extract facade (Wave-2+ ladder).
	// The orchestrator owns lazy enricher / Indexer / Drive wiring inside
	// the ytextraction capability (see ARCHITECTURE.md §7 "Extract facade
	// contract"); the monitor stays ignorant of those deps and treats each
	// call as a one-shot background operation. Hard failures (capability
	// not wired, port error) log Error and skip; business failures (no
	// segments, asset_repo nil) are surfaced through resp.Error at Warn.
	resp, err := m.youtubeSvc.Extract(ctx, req)
	if err != nil {
		m.log.Error("channel-monitor: youtube extract failed; skipping clip",
			zap.String("video_id", videoID),
			zap.String("title", title),
			zap.String("category", category),
			zap.Int("segments", len(segments)),
			zap.String("drive_folder_id", driveFolderID),
			zap.Error(err))
		return
	}
	// Defensive normalise: a `(nil, nil)` return from the capability (should
	// not happen per the orchestrator's typed contract, but might after a
	// future refactor) would otherwise silently log a misleading "0 items
	// extracted" success. Treat as hard failure.
	if resp == nil {
		m.log.Error("channel-monitor: youtube extract returned nil response without error; skipping clip",
			zap.String("video_id", videoID),
			zap.String("category", category))
		return
	}
	if !resp.OK {
		m.log.Warn("channel-monitor: extract pipeline reported !OK; skipping clip",
			zap.String("video_id", videoID),
			zap.String("title", title),
			zap.String("category", category),
			zap.Int("segments", len(segments)),
			zap.String("drive_folder_id", driveFolderID),
			zap.String("response_error", resp.Error))
		return
	}

	// Use the dedicated counter fields, not `len(Items)` — items can be
	// failed/skipped and a naive length would over-report. nil-guarded
	// because the capability may legitimately leave Stats empty for a
	// short-circuit success path (cached clip, no segments requested).
	processed, skipped, failed := 0, 0, 0
	if resp.Stats != nil {
		processed = resp.Stats.Processed
		skipped = resp.Stats.Skipped
		failed = resp.Stats.Failed
	}
	m.log.Info("Successfully extracted and uploaded channel clip",
		zap.String("video_id", videoID),
		zap.String("category", category),
		zap.String("channel_handle", channelHandle),
		zap.String("destination_group", destinationGroup),
		zap.Int("items_processed", processed),
		zap.Int("items_skipped", skipped),
		zap.Int("items_failed", failed))
}

// loadConfig loads the monitor configuration from file
func (m *ChannelMonitor) tryReserve(counter *atomic.Int32, limit int) bool {
	for {
		current := counter.Load()
		if current >= int32(limit) {
			return false
		}
		if counter.CompareAndSwap(current, current+1) {
			return true
		}
	}
}
