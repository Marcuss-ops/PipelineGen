package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// tryOllamaSegmentAnalysis sends the transcript to Ollama and returns segments.
func (s *Service) tryOllamaSegmentAnalysis(ctx context.Context, timedEntries []timedEntry, videoURL, videoID string, maxSegments int) []youtubetypes.Segment {
	if s.callbacks == nil {
		return nil
	}

	// Build timestamped transcript
	var transcriptParts []string
	totalChars := 0
	for _, e := range timedEntries {
		ts := textutil.FormatSecondsToTimestamp(int(e.start))
		line := fmt.Sprintf("[%s] %s", ts, e.text)
		totalChars += len(line) + 1
		if totalChars > 8000 {
			break
		}
		transcriptParts = append(transcriptParts, line)
	}
	transcript := strings.Join(transcriptParts, "\n")

	model := s.cfg.OllamaModel
	if model == "" {
		model = "gemma4:e2b"
	}

	if maxSegments <= 0 {
		maxSegments = maxAutoSegmentsPerVideo
	}

	prompt := fmt.Sprintf(`You are an expert video editor analyzing a timestamped YouTube transcript.

Transcript:
%s

Identify up to %d of the strongest, most substantive clips from this transcript.
Each segment MUST be between 10 and 60 seconds long — NO SHORTER than 10 seconds.
Absolutely NEVER return a clip less than 10 seconds.

Rules:
- Use EXACT timestamps from the transcript (the [HH:MM:SS] markers)
- Do NOT choose intro/opening greetings, sponsor reads, ads, housekeeping, applause-only moments, or outros
- Prefer story beats, arguments, revelations, jokes, confessions, surprising statements, or strong emotional turns
- If the transcript begins with introduction material, skip it and choose a later substantive moment
- Name each segment with a brief descriptive title
- Return ONLY a JSON array, no other text

Format:
[
  {"start": "HH:MM:SS", "end": "HH:MM:SS", "name": "Descriptive title"}
]`, transcript, maxSegments)

	s.log.Info("sending transcript to Ollama for segment analysis",
		zap.String("video_id", videoID),
		zap.String("model", model),
		zap.Int("transcript_chars", len(transcript)))

	release := s.callbacks.AcquireOllamaSem(ctx)
	if release == nil {
		return nil
	}
	defer release()

	responseStr, err := s.callbacks.OllamaSimpleGenerate(ctx, model, prompt, 60, map[string]any{"format": "json"})
	if err != nil {
		s.log.Warn("Ollama call failed for segment analysis", zap.Error(err))
		return nil
	}
	s.log.Info("Ollama returned response for segment analysis",
		zap.String("video_id", videoID),
		zap.Int("response_chars", len(responseStr)))

	segmentsJSON := responseStr
	if start := strings.Index(responseStr, "["); start >= 0 {
		if end := strings.LastIndex(responseStr, "]"); end > start {
			segmentsJSON = responseStr[start : end+1]
		}
	}

	var ollamaSegments []struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal([]byte(segmentsJSON), &ollamaSegments); err != nil {
		s.log.Warn("failed to parse Ollama segments JSON",
			zap.Error(err),
			zap.String("raw", responseStr))
		return nil
	}

	if len(ollamaSegments) == 0 {
		s.log.Warn("Ollama returned empty segments list", zap.String("video_id", videoID))
		return nil
	}

	s.log.Info("found segments from subtitle analysis",
		zap.String("url", videoURL),
		zap.Int("segments", len(ollamaSegments)))

	const minOllamaSegmentDuration = 10

	var result []youtubetypes.Segment
	for _, seg := range ollamaSegments {
		startSec, err1 := textutil.ParseTimestamp(seg.Start)
		endSec, err2 := textutil.ParseTimestamp(seg.End)
		if err1 != nil || err2 != nil || endSec-startSec <= 0 {
			s.log.Debug("skipping invalid segment from Ollama",
				zap.String("start", seg.Start),
				zap.String("end", seg.End),
				zap.String("name", seg.Name))
			continue
		}
		duration := endSec - startSec
		if duration < minOllamaSegmentDuration {
			s.log.Debug("skipping segment shorter than minimum duration",
				zap.String("name", seg.Name),
				zap.Int("duration_sec", duration),
				zap.Int("min_required", minOllamaSegmentDuration))
			continue
		}
		if duration > 60 {
			endSec = startSec + 60
		}
		if isLowValueSegmentName(seg.Name) {
			continue
		}
		result = append(result, youtubetypes.Segment{
			Name:  seg.Name,
			Start: fmt.Sprintf("%d", startSec),
			End:   fmt.Sprintf("%d", endSec),
		})
		if len(result) >= maxSegments {
			break
		}
	}

	if len(result) > 0 {
		s.setCachedSegments(ctx, videoID, result)
	}
	return result
}

// ── Heuristic fallback ───────────────────────────────────────────────────

func splitTimedEntriesByTime(entries []timedEntry, numSections int) [][]timedEntry {
	if len(entries) == 0 || numSections <= 1 {
		return [][]timedEntry{entries}
	}

	totalDuration := entries[len(entries)-1].end
	sectionDuration := totalDuration / float64(numSections)

	sections := make([][]timedEntry, numSections)
	for i := range sections {
		sections[i] = make([]timedEntry, 0)
	}

	for _, entry := range entries {
		sectionIdx := int(entry.start / sectionDuration)
		if sectionIdx >= numSections {
			sectionIdx = numSections - 1
		}
		if sectionIdx < 0 {
			sectionIdx = 0
		}
		sections[sectionIdx] = append(sections[sectionIdx], entry)
	}

	return sections
}

func generateHeuristicSegments(timedEntries []timedEntry, maxCount int) []youtubetypes.Segment {
	if len(timedEntries) < 5 || maxCount <= 0 {
		return nil
	}

	totalDuration := timedEntries[len(timedEntries)-1].end
	if totalDuration <= 0 {
		return nil
	}

	if totalDuration < 30 {
		return []youtubetypes.Segment{{
			Name:  timedEntries[0].text,
			Start: "0",
			End:   fmt.Sprintf("%d", int(totalDuration)),
		}}
	}

	segmentDuration := totalDuration / float64(maxCount)
	if segmentDuration < 30 {
		segmentDuration = 30
		maxCount = int(totalDuration / 30)
		if maxCount < 1 {
			maxCount = 1
		}
	}

	var result []youtubetypes.Segment
	for i := 0; i < maxCount; i++ {
		start := float64(i) * segmentDuration
		end := start + segmentDuration

		if i == maxCount-1 {
			end = totalDuration
		}

		segLen := end - start
		if segLen < 20 {
			end = start + 20
			if end > totalDuration {
				end = totalDuration
			}
		}
		if segLen > 60 {
			mid := (start + end) / 2
			start = mid - 30
			end = mid + 30
			if start < 0 {
				start = 0
				end = 60
			}
			if end > totalDuration {
				end = totalDuration
				start = end - 60
				if start < 0 {
					start = 0
				}
			}
		}

		name := fmt.Sprintf("Part %d", i+1)
		for _, e := range timedEntries {
			if e.start >= start && e.text != "" {
				words := strings.Fields(e.text)
				if len(words) > 6 {
					name = strings.Join(words[:6], " ")
				} else {
					name = e.text
				}
				if len(name) > 60 {
					name = name[:60]
				}
				break
			}
		}

		result = append(result, youtubetypes.Segment{
			Name:  name,
			Start: fmt.Sprintf("%d", int(start)),
			End:   fmt.Sprintf("%d", int(end)),
		})
	}

	return result
}

// ── Low-value filter ─────────────────────────────────────────────────────

func filterLowValueSegments(segments []youtubetypes.Segment) []youtubetypes.Segment {
	if len(segments) == 0 {
		return segments
	}
	out := make([]youtubetypes.Segment, 0, len(segments))
	for _, seg := range segments {
		if isLowValueSegmentName(seg.Name) {
			continue
		}
		out = append(out, seg)
	}
	return out
}

func isLowValueSegmentName(name string) bool {
	sample := strings.ToLower(strings.TrimSpace(name))
	if sample == "" {
		return true
	}
	lowValueMarkers := []string{
		"introduction", "opening", "open", "welcome", "greeting",
		"outro", "closing", "recap", "housekeeping", "sponsor", "sponsored",
		"ad ", " ad", "advert", "promo", "promotional", "shoutout", "shout out",
		"thanks for", "thank you for", "plug", "merch", "subscribe",
		"trailer", "preview", "preview of", "teaser",
	}

	if strings.HasPrefix(sample, "intro:") {
		return false
	}
	for _, marker := range lowValueMarkers {
		if strings.Contains(sample, marker) {
			return true
		}
	}
	return false
}
