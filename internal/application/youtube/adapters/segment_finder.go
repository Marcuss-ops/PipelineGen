package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

	"go.uber.org/zap"
)

// ── Segment discovery constants ──────────────────────────────────────────

const preferredSegmentDuration = 45
const maxAutoSegmentsPerVideo = 4
const maxAutoSegmentsPerLongSection = 1
const minSegmentDuration = 15

// timedEntry represents a single timed subtitle entry with start/end times and text.
type timedEntry struct {
	start float64
	end   float64
	text  string
}

// ── Segment splitting ────────────────────────────────────────────────────

// ── Segment cache ────────────────────────────────────────────────────────

func (s *Service) getCachedSegments(ctx context.Context, videoID string) ([]youtubetypes.Segment, bool) {
	if s.cache == nil {
		return nil, false
	}
	if segmentsJSON, ok := s.cache.GetSegments(ctx, videoID); ok {
		var segments []youtubetypes.Segment
		if err := json.Unmarshal([]byte(segmentsJSON), &segments); err == nil {
			return segments, true
		}
	}
	return nil, false
}

func (s *Service) setCachedSegments(ctx context.Context, videoID string, segments []youtubetypes.Segment) {
	if s.cache == nil {
		return
	}
	segmentsJSON, err := json.Marshal(segments)
	if err != nil {
		return
	}
	s.cache.SetSegments(ctx, videoID, string(segmentsJSON))
}

// ── Subtitle-based segment discovery ─────────────────────────────────────

func (s *Service) findSegmentsFromSubtitles(ctx context.Context, videoURL string) []youtubetypes.Segment {
	videoID, err := urlutil.ExtractVideoID(videoURL)
	if err != nil || videoID == "" {
		return nil
	}

	segs, found := s.getCachedSegments(ctx, videoID)
	if found {
		return segs
	}

	s.log.Info("downloading subtitles for segment analysis",
		zap.String("url", videoURL))

	tempDir, err := os.MkdirTemp("", "subs_segments_*")
	if err != nil {
		s.log.Debug("failed to create temp dir for subtitles", zap.Error(err))
		return nil
	}
	defer os.RemoveAll(tempDir)

	ytdlpPath := s.cfg.YtdlpPath

	subArgs := buildSubtitleArgs(ytdlpPath, s.cfg.YouTubeCookiesPath, videoURL, filepath.Join(tempDir, "subs"))
	subCmd := exec.CommandContext(ctx, ytdlpPath, subArgs...)
	out, err := subCmd.CombinedOutput()
	outStr := string(out)
	previewLen := len(outStr)
	if previewLen > 500 {
		previewLen = 500
	}
	if err != nil {
		s.log.Info("subtitle download had issues, checking for partial results",
			zap.String("url", videoURL),
			zap.String("output_preview", outStr[:previewLen]))
	}

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

	totalDuration := timedEntries[len(timedEntries)-1].end

	s.log.Info("parsed subtitles successfully",
		zap.Int("entries", len(timedEntries)),
		zap.Float64("total_duration_sec", totalDuration))

	const longVideoThreshold = 1800 // 30 min in seconds
	if totalDuration > longVideoThreshold {
		s.log.Info("video is longer than 30 min, splitting subtitles into 3 sections",
			zap.String("url", videoURL),
			zap.Float64("total_duration_sec", totalDuration))

		sections := splitTimedEntriesByTime(timedEntries, 3)
		var allSegments []youtubetypes.Segment

		for i, section := range sections {
			if len(section) < 5 {
				continue
			}

			s.log.Info("analyzing section of long video",
				zap.Int("section", i+1),
				zap.Int("entries", len(section)),
				zap.Float64("start_time", section[0].start),
				zap.Float64("end_time", section[len(section)-1].end))

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

		s.log.Warn("section-based analysis produced no segments, falling back to full transcript",
			zap.String("url", videoURL))
	}

	ollamaResult := s.tryOllamaSegmentAnalysis(ctx, timedEntries, videoURL, videoID, maxAutoSegmentsPerVideo)
	ollamaResult = filterLowValueSegments(ollamaResult)
	if len(ollamaResult) > 0 {
		return ollamaResult
	}

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

// buildSubtitleArgs assembles the canonical yt-dlp argv for a subtitle
// fetch used by the segment finder. Extracted from findSegmentsFromSubtitles
// so hermetic TDD tests can assert the BaseArgs delegation contract
// WITHOUT spawning a real yt-dlp subprocess.
//
// PR-SEGMENT-FINDER-BASEARGS-MIGRATION (2026-07-06): the canonical
// yt-dlp argv is built in 3 layers —
//  1. baseArgs (the canonical 4-5 anti-bot flags from ytdlp.BaseArgs)
//  2. operation-specific flags (--write-auto-subs / --write-subs /
//     --skip-download / --sub-langs en / --sub-format vtt / -o)
//  3. positional URL (appended last; yt-dlp accepts global options
//     before OR after the positional URL)
//
// useCookies is hardcoded to false (godlike/07 minimum-blast-radius):
// the segment finder operates on PUBLIC videos for clip segmentation,
// not age-restricted / n-challenge boundary cases. The monitor (which
// DOES need cookies) routes through the separate YTDLPSubtitleAdapter.
//
// godlike/07 minimum-blast-radius: the CommandBuilder is constructed
// per-call (no Service struct change). The segment_finder is called
// once per video, so the per-call overhead is negligible. A future
// optimization could hoist this to the Service struct if profiling
// shows it matters.
func buildSubtitleArgs(ytdlpPath, cookiesPath, videoURL, outputTemplate string) []string {
	cfg := &ytcfg.Config{
		External: ytcfg.ExternalConfig{
			YtdlpPath:          ytdlpPath,
			YouTubeCookiesPath: cookiesPath,
		},
	}
	builder := ytdlp.NewCommandBuilder(cfg)

	baseArgs := builder.BaseArgs(videoURL, builder.YouTubeCookiesConfigured())
	args := append([]string{}, baseArgs...)
	args = append(args,
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en", "--sub-format", "vtt",
		"-o", outputTemplate,
		videoURL,
	)
	return args
}

// ── Interesting segments (priority: subtitles > chapters) ────────────────
