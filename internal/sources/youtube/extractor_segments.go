package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

	"go.uber.org/zap"
)

// Semaphore to limit concurrent Ollama model invocations (or scripts invoking it)
var ollamaSem = make(chan struct{}, 2)

// MaxSegmentDuration is the maximum allowed duration for a single clip segment (60 seconds)
const MaxSegmentDuration = 60

// preferredSegmentDuration is the target duration for auto-split segments (45 seconds).
// Set lower than MaxSegmentDuration (60) so the tail guard (minSegmentDuration=15)
// can extend the last chunk without exceeding the absolute 60s limit.
const preferredSegmentDuration = 45
const maxAutoSegmentsPerVideo = 4
const maxAutoSegmentsPerLongSection = 1

// minSegmentDuration is the minimum acceptable duration for an auto-split chunk.
// If the remaining tail after splitting would be smaller than this,
// the last chunk is extended to include it instead of creating a tiny orphan segment.
const minSegmentDuration = 15

// splitLongSegments splits any segments longer than preferredSegmentDuration into
// multiple non-overlapping chunks. Segments already within the limit are returned
// unchanged. Splits are aligned to second boundaries so results are deterministic.
func splitLongSegments(segs []Segment) []Segment {
	if len(segs) == 0 {
		return segs
	}
	out := make([]Segment, 0, len(segs))
	for _, seg := range segs {
		startSec, err := textutil.ParseTimestamp(strings.TrimSpace(seg.Start))
		if err != nil {
			out = append(out, seg)
			continue
		}
		endSec, err := textutil.ParseTimestamp(strings.TrimSpace(seg.End))
		if err != nil {
			out = append(out, seg)
			continue
		}
		duration := endSec - startSec
		if duration <= preferredSegmentDuration {
			out = append(out, seg)
			continue
		}

		partNum := 0
		cur := startSec
		for cur < endSec {
			partEnd := cur + preferredSegmentDuration
			remaining := endSec - partEnd
			// If the remaining tail is shorter than minSegmentDuration,
			// extend this chunk to include it instead of creating a tiny orphan.
			if remaining > 0 && remaining < minSegmentDuration {
				partEnd = endSec
			}
			if partEnd > endSec {
				partEnd = endSec
			}
			partNum++
			// Generate a unique name for each split part (Part 2+, part 1 keeps original)
			// so they don't all map to the same Drive file.
			partName := seg.Name
			if partNum > 1 {
				partName = fmt.Sprintf("%s (Part %d)", seg.Name, partNum)
			}
			out = append(out, Segment{
				Start: textutil.FormatSecondsToTimestamp(cur),
				End:   textutil.FormatSecondsToTimestamp(partEnd),
				Name:  partName,
				Tags:  seg.Tags,
			})
			cur = partEnd
		}
	}
	return out
}

// timedEntry represents a single timed subtitle entry with start/end times and text.
type timedEntry struct {
	start float64
	end   float64
	text  string
}

func (s *Service) getCachedSegments(ctx context.Context, videoID string) ([]Segment, bool) {
	if s.clipsRepo == nil || s.clipsRepo.DB() == nil {
		return nil, false
	}
	var segmentsJSON string
	err := s.clipsRepo.DB().QueryRowContext(ctx, "SELECT segments_json FROM youtube_segments_cache WHERE video_id = ?", videoID).Scan(&segmentsJSON)
	if err == nil {
		var segments []Segment
		if err := json.Unmarshal([]byte(segmentsJSON), &segments); err == nil {
			return segments, true
		}
	}
	return nil, false
}

func (s *Service) setCachedSegments(ctx context.Context, videoID string, segments []Segment) {
	if s.clipsRepo == nil || s.clipsRepo.DB() == nil {
		return
	}
	segmentsJSON, err := json.Marshal(segments)
	if err != nil {
		return
	}
	_, err = s.clipsRepo.DB().ExecContext(ctx, "INSERT OR REPLACE INTO youtube_segments_cache (video_id, segments_json, cached_at) VALUES (?, ?, datetime('now'))", videoID, string(segmentsJSON))
	if err != nil {
		s.log.Warn("failed to cache youtube segments", zap.Error(err))
	}
}

// findSegmentsFromSubtitles downloads VTT subtitles and asks Ollama/Gemma to find the
// most interesting segments based on the actual transcript content.
// For videos longer than 30 minutes, the transcript is split into 3 time-based sections
// and each section is analyzed separately to ensure coverage across the full video.
// Returns nil if subtitles are not available or analysis fails.
func (s *Service) findSegmentsFromSubtitles(ctx context.Context, videoURL string) []Segment {
	videoID, err := urlutil.ExtractVideoID(videoURL)
	if err != nil || videoID == "" {
		return nil
	}

	// Check cache first
	segs, found := s.getCachedSegments(ctx, videoID)
	if found {
		return segs
	}

	s.log.Info("downloading subtitles for segment analysis",
		zap.String("url", videoURL))

	// Download subtitles via yt-dlp
	tempDir, err := os.MkdirTemp("", "subs_segments_*")
	if err != nil {
		s.log.Debug("failed to create temp dir for subtitles", zap.Error(err))
		return nil
	}
	defer os.RemoveAll(tempDir)

	ytdlpPath := s.cfg.External.ResolvedYtdlpPath()

	subCmd := exec.CommandContext(ctx, ytdlpPath,
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en", "--sub-format", "vtt",
		"-o", filepath.Join(tempDir, "subs"),
		videoURL,
	)
	out, err := subCmd.CombinedOutput()
	outStr := string(out)
	previewLen := min(len(outStr), 500)
	if err != nil {
		s.log.Info("subtitle download had issues, checking for partial results",
			zap.String("url", videoURL),
			zap.String("output_preview", outStr[:previewLen]))
	}

	// Find VTT file(s) — any language works
	var vttPath string
	dirEntries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil
	}
	for _, de := range dirEntries {
		if strings.HasPrefix(de.Name(), "subs.") && strings.HasSuffix(de.Name(), ".vtt") {
			vttPath = filepath.Join(tempDir, de.Name())
			break
		}
	}
	if vttPath == "" {
		s.log.Info("no VTT subtitle file found for video, cannot analyze segments",
			zap.String("url", videoURL),
			zap.Int("dir_entries", len(dirEntries)))
		return nil
	}

	s.log.Info("found VTT subtitle file for analysis",
		zap.String("vtt_path", vttPath),
		zap.String("url", videoURL))

	// Parse VTT into timed text entries
	vttData, err := os.ReadFile(vttPath)
	if err != nil {
		s.log.Warn("failed to read VTT file", zap.String("path", vttPath), zap.Error(err))
		return nil
	}

	content := string(vttData)
	webvttRe := regexp.MustCompile(`(?s)^WEBVTT.*?\n\n`)
	content = webvttRe.ReplaceAllString(content, "")

	var timedEntries []timedEntry

	timeRegex := regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})`)

	for _, block := range strings.Split(content, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		var timeLine string
		var textLines []string
		for _, line := range lines {
			if timeRegex.MatchString(line) {
				timeLine = line
			} else if timeLine != "" {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "align:") && !strings.HasPrefix(line, "position:") {
					textLines = append(textLines, line)
				}
			}
		}
		if timeLine == "" || len(textLines) == 0 {
			continue
		}
		matches := timeRegex.FindStringSubmatch(timeLine)
		if len(matches) < 3 {
			continue
		}

		start := textutil.ParseVTTTimestamp(matches[1])
		end := textutil.ParseVTTTimestamp(matches[2])
		text := textutil.CleanSubtitleText(strings.Join(textLines, " "))
		if text == "" {
			continue
		}
		timedEntries = append(timedEntries, timedEntry{start: start, end: end, text: text})
	}

	if len(timedEntries) < 5 {
		s.log.Info("too few subtitle entries for analysis, skipping",
			zap.String("url", videoURL),
			zap.Int("entries", len(timedEntries)))
		return nil
	}

	// ── Determine total video duration ──────────────────────────────────
	totalDuration := timedEntries[len(timedEntries)-1].end

	s.log.Info("parsed subtitles successfully",
		zap.Int("entries", len(timedEntries)),
		zap.Float64("total_duration_sec", totalDuration))

	// ── For videos LONGER than 30 minutes, split into sections ──────────
	// Each section is analyzed independently so that interesting clips
	// from the middle and end of long videos are not missed.
	const longVideoThreshold = 1800 // 30 min in seconds
	if totalDuration > longVideoThreshold {
		s.log.Info("video is longer than 30 min, splitting subtitles into 3 sections",
			zap.String("url", videoURL),
			zap.Float64("total_duration_sec", totalDuration))

		sections := splitTimedEntriesByTime(timedEntries, 3)
		var allSegments []Segment

		for i, section := range sections {
			if len(section) < 5 {
				s.log.Debug("section too few entries, skipping",
					zap.Int("section", i+1),
					zap.Int("entries", len(section)))
				continue
			}

			s.log.Info("analyzing section of long video",
				zap.Int("section", i+1),
				zap.Int("entries", len(section)),
				zap.Float64("start_time", section[0].start),
				zap.Float64("end_time", section[len(section)-1].end))

			// Try Ollama-based analysis for this section
			sectionID := fmt.Sprintf("%s_section_%d", videoID, i+1)
			sectionResult := s.tryOllamaSegmentAnalysis(ctx, section, videoURL, sectionID, maxAutoSegmentsPerLongSection)
			if len(sectionResult) > maxAutoSegmentsPerLongSection {
				sectionResult = sectionResult[:maxAutoSegmentsPerLongSection]
			}
			sectionResult = filterLowValueSegments(sectionResult)
			if len(sectionResult) > 0 {
				allSegments = append(allSegments, sectionResult...)
				continue
			}

			// Fallback: heuristic segments for this section
			s.log.Info("Ollama unavailable for section, generating heuristic segments",
				zap.Int("section", i+1),
				zap.Int("entries", len(section)))

			sectionHeuristic := generateHeuristicSegments(section, maxAutoSegmentsPerLongSection)
			if len(sectionHeuristic) > maxAutoSegmentsPerLongSection {
				sectionHeuristic = sectionHeuristic[:maxAutoSegmentsPerLongSection]
			}
			sectionHeuristic = filterLowValueSegments(sectionHeuristic)
			if len(sectionHeuristic) > 0 {
				allSegments = append(allSegments, sectionHeuristic...)
			}
		}

		if len(allSegments) > 0 {
			if len(allSegments) > maxAutoSegmentsPerVideo {
				allSegments = allSegments[:maxAutoSegmentsPerVideo]
			}
			s.setCachedSegments(ctx, videoID, allSegments)
			s.log.Info("generated segments from 3-section analysis for long video",
				zap.Int("total_segments", len(allSegments)),
				zap.Float64("duration_sec", totalDuration))
			return allSegments
		}

		// If sections failed entirely, fall through to single-pass analysis
		s.log.Warn("section-based analysis produced no segments, falling back to full transcript",
			zap.String("url", videoURL))
	}

	// ── Standard analysis for shorter videos (or fallback from sections) ─
	// Try Ollama-based analysis first
	ollamaResult := s.tryOllamaSegmentAnalysis(ctx, timedEntries, videoURL, videoID, maxAutoSegmentsPerVideo)
	ollamaResult = filterLowValueSegments(ollamaResult)
	if len(ollamaResult) > 0 {
		return ollamaResult
	}

	// Fallback: when Ollama fails (timeout, overload, etc.) but we have subtitles,
	// generate heuristic segments evenly spread across the video duration.
	s.log.Info("Ollama unavailable for segment analysis, generating heuristic segments from subtitle timing",
		zap.String("url", videoURL),
		zap.Int("subtitle_entries", len(timedEntries)))

	heuristicResult := generateHeuristicSegments(timedEntries, maxAutoSegmentsPerVideo)
	heuristicResult = filterLowValueSegments(heuristicResult)
	if len(heuristicResult) > 0 {
		s.setCachedSegments(ctx, videoID, heuristicResult)
		s.log.Info("generated heuristic segments from subtitles",
			zap.Int("segments", len(heuristicResult)))
		return heuristicResult
	}

	return nil
}

// findInterestingSegments finds interesting segments for a YouTube video.
// Priority:
//  1. Subtitles + Gemma analysis (actual transcript → real highlights)
//  2. YouTube chapters (timestamps from metadata)
//  3. Returns nil (no segments — caller should skip this video)
func (s *Service) findInterestingSegments(ctx context.Context, videoURL string) ([]Segment, error) {
	videoID, err := urlutil.ExtractVideoID(videoURL)
	if err != nil || videoID == "" {
		videoID = hashutil.MD5String(videoURL)[:12]
	}

	// Check cache first
	if segs, found := s.getCachedSegments(ctx, videoID); found {
		s.log.Info("resolved YouTube segments from cache",
			zap.String("video_id", videoID),
			zap.Int("segments_count", len(segs)))
		return segs, nil
	}

	// ── Priority 1: Subtitles + Gemma analysis ───────────────────────────
	// The actual transcript content tells us what's really interesting.
	segments := s.findSegmentsFromSubtitles(ctx, videoURL)
	if len(segments) > 0 {
		s.setCachedSegments(ctx, videoID, segments)
		return segments, nil
	}
	s.log.Debug("subtitle analysis returned no segments, trying YouTube chapters",
		zap.String("url", videoURL))

	// ── Priority 2: YouTube Chapters ─────────────────────────────────────
	ytDlp := downloader.NewYTDLP(s.cfg)
	meta, metaErr := ytDlp.GetVideoMetadata(ctx, videoURL)
	if metaErr == nil && meta != nil && len(meta.Chapters) > 0 {
		s.log.Info("found YouTube chapters, using them as segments",
			zap.String("url", videoURL),
			zap.Int("chapters", len(meta.Chapters)))
		var segments []Segment
		for _, ch := range meta.Chapters {
			chapterDur := int(ch.EndTime - ch.StartTime)
			if chapterDur < 15 {
				continue
			}
			var segStart, segEnd float64
			if chapterDur <= 60 {
				segStart = ch.StartTime
				segEnd = ch.EndTime
			} else {
				offset := (chapterDur - 60) / 2
				segStart = ch.StartTime + float64(offset)
				segEnd = segStart + 60
				if segEnd > ch.EndTime {
					segEnd = ch.EndTime
				}
			}
			segments = append(segments, Segment{
				Name:  ch.Title,
				Start: fmt.Sprintf("%d", int(segStart)),
				End:   fmt.Sprintf("%d", int(segEnd)),
			})
			if len(segments) >= 3 {
				break
			}
		}
		if len(segments) > 0 {
			segments = filterLowValueSegments(segments)
		}
		if len(segments) > 0 {
			s.setCachedSegments(ctx, videoID, segments)
			return segments, nil
		}
	}

	// No segments found from any source
	s.log.Debug("no segments found via subtitles or chapters",
		zap.String("url", videoURL))
	return nil, nil
}
