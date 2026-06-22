package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

	"go.uber.org/zap"
)

func (m *ChannelMonitor) findSegmentsFromSubtitles(ctx context.Context, videoURL string, cfg *MonitorConfig, maxSegments int, segmentPrompt string) []youtubetypes.Segment {
	if maxSegments <= 0 {
		maxSegments = 3 // default
	}

	videoID, _ := urlutil.ExtractVideoID(videoURL)
	if videoID == "" {
		m.log.Debug("could not extract video ID from URL", zap.String("url", videoURL))
		return nil
	}

	// Download subtitles via yt-dlp
	tempDir, err := os.MkdirTemp("", "subs_segments_*")
	if err != nil {
		m.log.Debug("failed to create temp dir for subtitles", zap.Error(err))
		return nil
	}
	defer os.RemoveAll(tempDir)

	subCmd := exec.CommandContext(ctx, cfg.YtdlpPath,
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en", "--sub-format", "vtt",
		"-o", filepath.Join(tempDir, "subs"),
		videoURL,
	)
	// Run yt-dlp — it may return non-zero if some subtitle languages fail (e.g. 429),
	// but EN subtitles may still have been downloaded successfully.
	out, _ := subCmd.CombinedOutput()

	// Find VTT file (check even if yt-dlp returned error — EN may have succeeded)
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
		m.log.Debug("no VTT subtitle file found for video",
			zap.String("url", videoURL),
			zap.String("yt_output", strings.TrimSpace(string(out))))
		return nil
	}

	// Parse VTT into timed text entries
	vttData, err := os.ReadFile(vttPath)
	if err != nil {
		return nil
	}

	content := string(vttData)
	content = regexRemoveVTTHeader(content)

	type timedEntry struct {
		start float64
		end   float64
		text  string
	}
	var timedEntries []timedEntry
	seenTexts := make(map[string]bool)

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
		raw := strings.Join(textLines, " ")
		cleaned := regexRemoveXMLTags(raw)
		text := textutil.CleanSubtitleText(cleaned)
		if text == "" {
			continue
		}
		if seenTexts[text] {
			continue
		}
		seenTexts[text] = true
		timedEntries = append(timedEntries, timedEntry{start: start, end: end, text: text})
	}

	if len(timedEntries) < 5 {
		m.log.Debug("too few subtitle entries for analysis",
			zap.String("url", videoURL),
			zap.Int("entries", len(timedEntries)))
		return nil
	}

	// Secondary dedup: remove entries whose text is a substring of an adjacent entry.
	// Auto-generated VTT captions often have overlapping lines like:
	//   [00:07] "All right, here we go. Lord Jamar, welcome back to Vlad TV."
	//   [00:10] "welcome back to Vlad TV."
	// The shorter one is redundant.
	deduped := make([]timedEntry, 0, len(timedEntries))
	for i, e := range timedEntries {
		isRedundant := false
		// Check if this text is a substring of the previous non-redundant entry
		if len(deduped) > 0 {
			prev := deduped[len(deduped)-1]
			if len(e.text) < len(prev.text) && strings.Contains(prev.text, e.text) {
				isRedundant = true
			}
		}
		// Also check if next entry subsumes this one
		if !isRedundant && i+1 < len(timedEntries) {
			next := timedEntries[i+1]
			if len(e.text) < len(next.text) && strings.Contains(next.text, e.text) {
				isRedundant = true
			}
		}
		if !isRedundant {
			deduped = append(deduped, e)
		}
	}

	// Build timestamped transcript for Ollama (limit to first 8000 chars to prevent overflow)
	var transcriptParts []string
	totalChars := 0
	for _, e := range deduped {
		ts := textutil.FormatSecondsToTimestamp(int(e.start))
		line := fmt.Sprintf("[%s] %s", ts, e.text)
		totalChars += len(line) + 1
		if totalChars > 8000 {
			break
		}
		transcriptParts = append(transcriptParts, line)
	}
	transcript := strings.Join(transcriptParts, "\n")

	model := m.cfg.External.OllamaModel
	if model == "" {
		model = "gemma4:e2b"
	}

	// Build the focus instruction: use custom segment prompt if provided,
	// otherwise fall back to the default "strong, substantive clips" guidance.
	focusInstruction := "Prefer story beats, arguments, revelations, jokes, confessions, surprising statements, or strong emotional turns"
	if segmentPrompt != "" {
		focusInstruction = segmentPrompt
	}

	prompt := fmt.Sprintf(`You are an expert video editor analyzing a timestamped YouTube transcript.

Transcript:
%s

Identify up to %d of the best clips from this transcript.
Each segment MUST be between 10 and 60 seconds long — NO SHORTER than 10 seconds.
Absolutely NEVER return a clip less than 10 seconds.

Rules:
- Use EXACT timestamps from the transcript (the [MM:SS] markers)
- Timestamps MUST be in HH:MM:SS format (e.g. "00:05:30" not "5:30")
- Do NOT choose intro/opening greetings, sponsor reads, ads, housekeeping, applause-only moments, or outros
- STRICT RULE: NEVER select any segments that mention, discuss, or reference Donald Trump, politics,
  elections, political candidates, or any political commentary. If the transcript contains
  political content at any point, skip those segments entirely.
- %s
- If the transcript begins with introduction material, skip it and choose a later substantive moment
- Name each segment with a brief descriptive title
- Identify the protagonists: who is the HOST and who are any GUESTS speaking in each segment
- Provide a brief summary of what happens in each segment
- Return ONLY a JSON array, no other text

Format:
[
  {
    "start": "HH:MM:SS",
    "end": "HH:MM:SS",
    "name": "Descriptive title",
    "summary": "Brief description of what happens in this segment",
    "speakers": ["host name", "guest name"],
    "mentioned_people": ["other person mentioned"]
  }
]`, transcript, maxSegments, focusInstruction)

	responseStr, err := m.ollamaClient.SimpleGenerate(ctx, model, prompt, 60*time.Second, map[string]any{"format": "json"})
	if err != nil {
		m.log.Debug("ollama call failed for segment analysis", zap.Error(err))
		return nil
	}
	segmentsJSON := responseStr
	if start := strings.Index(responseStr, "["); start >= 0 {
		if end := strings.LastIndex(responseStr, "]"); end > start {
			segmentsJSON = responseStr[start : end+1]
		}
	}

	var ollamaSegments []struct {
		Start           string   `json:"start"`
		End             string   `json:"end"`
		Name            string   `json:"name"`
		Summary         string   `json:"summary,omitempty"`
		Speakers        []string `json:"speakers,omitempty"`
		MentionedPeople []string `json:"mentioned_people,omitempty"`
	}
	if err := json.Unmarshal([]byte(segmentsJSON), &ollamaSegments); err != nil {
		m.log.Debug("failed to parse ollama segments JSON",
			zap.Error(err),
			zap.String("raw", responseStr))
		return nil
	}

	if len(ollamaSegments) == 0 {
		return nil
	}

	m.log.Info("✅ found segments from subtitle analysis",
		zap.String("url", videoURL),
		zap.Int("segments", len(ollamaSegments)))

	// Minimum segment duration: 10 seconds — clips shorter than this are too
	// brief to be useful (3s clips don't convey enough context for comedy).
	const minSegmentDuration = 10

	var result []youtubetypes.Segment
	for _, s := range ollamaSegments {
		// Validate timestamps
		startSec, err1 := textutil.ParseTimestamp(s.Start)
		endSec, err2 := textutil.ParseTimestamp(s.End)
		if err1 != nil || err2 != nil || endSec-startSec <= 0 {
			m.log.Debug("skipping invalid segment from Ollama",
				zap.String("start", s.Start),
				zap.String("end", s.End),
				zap.String("name", s.Name))
			continue
		}
		duration := endSec - startSec
		// Enforce minimum duration — skip very short clips
		if duration < minSegmentDuration {
			m.log.Debug("skipping segment shorter than minimum duration",
				zap.String("name", s.Name),
				zap.Int("duration_sec", duration),
				zap.Int("min_required", minSegmentDuration))
			continue
		}
		// Cap duration at 60s (max clip length for comedy segments)
		if duration > 60 {
			endSec = startSec + 60
		}
		if isLowValueMonitorSegmentName(s.Name) {
			continue
		}
		resultSeg := youtubetypes.Segment{
			Name:  s.Name,
			Start: fmt.Sprintf("%d", startSec),
			End:   fmt.Sprintf("%d", endSec),
		}
		// Pass through protagonist/enrichment fields from Gemma
		if s.Summary != "" {
			resultSeg.Summary = s.Summary
		}
		if len(s.Speakers) > 0 {
			resultSeg.Speakers = s.Speakers
		}
		if len(s.MentionedPeople) > 0 {
			resultSeg.MentionedPeople = s.MentionedPeople
		}
		result = append(result, resultSeg)
		if len(result) >= maxSegments {
			break
		}
	}
	return result
}

// findInterestingSegments finds interesting segments for a YouTube video.
// Priority:
//  1. Download subtitles → ask Gemma to find interesting segments from actual transcript
//  2. YouTube chapters (timestamps from metadata)
//  3. Returns nil (no segments — caller should skip this video)
//
// maxSegments and segmentPrompt come from the channel config for per-channel customization.
func (m *ChannelMonitor) findInterestingSegments(ctx context.Context, videoURL string, cfg *MonitorConfig, maxSegments int, segmentPrompt string) []youtubetypes.Segment {
	// ── Priority 1: Subtitles + Gemma analysis ───────────────────────────
	// The actual transcript content tells us what's really interesting,
	// unlike chapter titles which can be wrong/misleading.
	segments := m.findSegmentsFromSubtitles(ctx, videoURL, cfg, maxSegments, segmentPrompt)
	if len(segments) > 0 {
		return segments
	}
	m.log.Debug("subtitle analysis returned no segments, trying YouTube chapters",
		zap.String("url", videoURL))

	// ── Priority 2: YouTube Chapters ─────────────────────────────────────
	ytDlp := downloader.NewYTDLP(m.cfg)
	meta, err := ytDlp.GetVideoMetadata(ctx, videoURL)
	if err == nil && meta != nil && len(meta.Chapters) > 0 {
		m.log.Info("found YouTube chapters, using them as segments",
			zap.String("url", videoURL),
			zap.Int("chapters", len(meta.Chapters)))
		var segments []youtubetypes.Segment
		for _, ch := range meta.Chapters {
			chapterDur := int(ch.EndTime - ch.StartTime)
			if chapterDur < 15 {
				continue
			}
			var segStart, segEnd float64
			if chapterDur <= 120 {
				segStart = ch.StartTime
				segEnd = ch.EndTime
			} else {
				offset := (chapterDur - 120) / 2
				segStart = ch.StartTime + float64(offset)
				segEnd = segStart + 120
				if segEnd > ch.EndTime {
					segEnd = ch.EndTime
				}
			}
			segments = append(segments, youtubetypes.Segment{
				Name:  ch.Title,
				Start: fmt.Sprintf("%d", int(segStart)),
				End:   fmt.Sprintf("%d", int(segEnd)),
			})
			if len(segments) >= 3 {
				break
			}
		}
		if len(segments) > 0 {
			filtered := make([]youtubetypes.Segment, 0, len(segments))
			for _, seg := range segments {
				if isLowValueMonitorSegmentName(seg.Name) {
					continue
				}
				filtered = append(filtered, seg)
			}
			if len(filtered) > 0 {
				return filtered
			}
		}
	}

	// No segments found from any source — return nil
	m.log.Debug("no segments found via subtitles or chapters",
		zap.String("url", videoURL))
	return nil
}

func isLowValueMonitorSegmentName(name string) bool {
	sample := strings.ToLower(strings.TrimSpace(name))
	if sample == "" {
		return true
	}
	lowValueMarkers := []string{
		"intro", "introduction", "opening", "welcome", "greeting",
		"outro", "closing", "recap", "housekeeping", "sponsor", "sponsored",
		"ad ", " ad", "advert", "promo", "promotional", "shoutout", "shout out",
		"thanks for", "thank you for", "plug", "merch", "subscribe",
		"trailer", "preview", "teaser",
	}
	for _, marker := range lowValueMarkers {
		if strings.Contains(sample, marker) {
			return true
		}
	}
	return false
}

// downloadClip downloads a clip from YouTube using the shared media service
