package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// FindSegments satisfies monitor.VideoAnalyzer.
//
// The pre-Step-9 monitor.semantic_matcher built the timestamped
// transcript inline from the YTDLP subprocess output. After Step 9,
// that responsibility moves to the YTDLPSubtitleAdapter (sibling)
// which is injected via the ctor — OllamaAnalyzer.FindSegments calls
// subtitles.GetTimedTranscript(ctx, videoURL) and assembles the prompt.
//
// Prompts vs. simple text classification: the segment prompt is
// significantly longer and asks the LLM to return a JSON ARRAY
// (segments instead of a single object). The parse path uses inline
// strings.Index/LastIndex for array extraction (vs. the jsonRegexFind
// helper used by Score for object extraction).
//
// Validation (format preserved from pre-Step-9 segment_finder.go):
//   - timestamps parseable with textutil.ParseTimestamp
//   - duration 10s..60s (clamped at 60s)
//   - name not low-value (intro/outro/sponsor/intro-outro filter)
//
// Dropped from pre-Step-9:
//   - YouTube chapters fallback (Priority-2 path; <5% channels
//     actually hit it per operator logs). Re-introduction is a
//     follow-up when a real operator requests it.
//
// Errors:
//   - "subtitles.GetTimedTranscript: ..." (VTT fetch failure bubbles
//     up from the injected adapter)
//   - "ollama SimpleGenerate: ..." (subprocess failure)
//   - "parse ollama segments JSON: ..." (JSON parse failure after
//     markdown fallback)
func (a *OllamaAnalyzer) FindSegments(ctx context.Context, videoURL string, transcript string, prompt string, maxSegments int) ([]ytdomain.Segment, error) {
	if a.ollamaClient == nil {
		return nil, fmt.Errorf("OllamaAnalyzer.FindSegments: ollama client not wired")
	}
	if a.subtitles == nil {
		return nil, fmt.Errorf("OllamaAnalyzer.FindSegments: subtitles not wired (composition bug — pass YTDLPSubtitleAdapter into semantic.NewOllamaAnalyzer)")
	}
	if transcript == "" {
		return nil, fmt.Errorf("OllamaAnalyzer.FindSegments: empty transcript (subtitles-miss path was lost in the port split — re-introducing the chapters fallback is a P1 follow-up)")
	}

	// Step 1: re-fetch timed entries.
	doc, err := a.subtitles.Fetch(ctx, videoURL)
	if err != nil {
		return nil, fmt.Errorf("OllamaAnalyzer.FindSegments: subtitles.Fetch(%s): %w", videoURL, err)
	}
	entries := doc.Entries
	if len(entries) < 5 {
		// Pre-Step-9 segment_finder.go rejected <5 timed entries as
		// "too few subtitle entries for analysis". Preserve the gate.
		a.log.Debug("OllamaAnalyzer.FindSegments: too few timed entries; need >= 5",
			zap.String("video_url", videoURL),
			zap.Int("entries", len(entries)))
		return nil, nil
	}

	// Step 2: build the timestamped transcript string (pre-Step-9 layout
	// with [MM:SS] markers).
	var parts []string
	totalChars := 0
	for _, e := range entries {
		ts := textutil.FormatSecondsToTimestamp(int(e.Start))
		line := fmt.Sprintf("[%s] %s", ts, e.Text)
		totalChars += len(line) + 1
		if totalChars > 8000 {
			break
		}
		parts = append(parts, line)
	}
	timestampedTranscript := strings.Join(parts, "\n")

	// Step 3: build the focus instruction (custom segment prompt overrides
	// default).
	focusInstruction := "Prefer story beats, arguments, revelations, jokes, confessions, surprising statements, or strong emotional turns"
	if prompt != "" {
		focusInstruction = prompt
	}

	// Step 4: invoke Ollama.
	llmPrompt := fmt.Sprintf(`You are an expert video editor analyzing a timestamped YouTube transcript.

Transcript:
%s

Identify up to %d of the best clips from this transcript.
Each segment MUST be between 10 and 60 seconds long — NO SHORTER than 10 seconds.
Absolutely NEVER return a clip less than 10 seconds.

Rules:
- Use EXACT timestamps from the transcript (the [MM:SS] markers)
- Timestamps MUST be in HH:MM:SS format (e.g. "00:05:30" not "5:30")
- Do NOT choose intro/opening greetings, sponsor reads, ads, housekeeping, applause-only moments, or outros
- STRICT RULE: NEVER select any segments that mention, discuss, or reference Donald Trump, politics, elections, political candidates, or any political commentary. If the transcript contains political content at any point, skip those segments entirely.
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
]`, timestampedTranscript, maxSegments, focusInstruction)

	responseStr, err := a.ollamaClient.SimpleGenerate(ctx, a.model, llmPrompt, 60*time.Second, map[string]any{"format": "json"})
	if err != nil {
		return nil, fmt.Errorf("ollama call: %w", err)
	}
	segmentsJSON := responseStr
	if start := strings.Index(responseStr, "["); start >= 0 {
		if end := strings.LastIndex(responseStr, "]"); end > start {
			segmentsJSON = responseStr[start : end+1]
		}
	}
	var rawSegments []struct {
		Start           string   `json:"start"`
		End             string   `json:"end"`
		Name            string   `json:"name"`
		Summary         string   `json:"summary,omitempty"`
		Speakers        []string `json:"speakers,omitempty"`
		MentionedPeople []string `json:"mentioned_people,omitempty"`
	}
	if err := json.Unmarshal([]byte(segmentsJSON), &rawSegments); err != nil {
		a.log.Debug("OllamaAnalyzer.FindSegments: JSON parse failed",
			zap.Error(err),
			zap.String("raw", responseStr))
		return nil, fmt.Errorf("parse ollama segments JSON: %w", err)
	}
	if len(rawSegments) == 0 {
		return nil, nil
	}

	// Step 5: validate timestamps + clamp duration.
	const minSec = 10
	const maxSec = 60

	var out []ytdomain.Segment
	for _, s := range rawSegments {
		startSec, err1 := textutil.ParseTimestamp(s.Start)
		endSec, err2 := textutil.ParseTimestamp(s.End)
		if err1 != nil || err2 != nil || endSec <= startSec {
			a.log.Debug("OllamaAnalyzer.FindSegments: skipping invalid segment",
				zap.String("start", s.Start),
				zap.String("end", s.End),
				zap.String("name", s.Name))
			continue
		}
		duration := endSec - startSec
		if duration < minSec {
			a.log.Debug("OllamaAnalyzer.FindSegments: skipping too-short segment",
				zap.String("name", s.Name),
				zap.Float64("duration_sec", float64(duration)),
				zap.Int("min_required", minSec))
			continue
		}
		if duration > maxSec {
			endSec = startSec + maxSec
		}
		if isLowValueMonitorSegmentName(s.Name) {
			continue
		}
		seg := ytdomain.Segment{
			Name:  s.Name,
			Start: fmt.Sprintf("%d", int(startSec)),
			End:   fmt.Sprintf("%d", int(endSec)),
		}
		// Pass through protagonist/enrichment fields when present.
		if s.Summary != "" {
			seg.Summary = s.Summary
		}
		if len(s.Speakers) > 0 {
			seg.Speakers = s.Speakers
		}
		if len(s.MentionedPeople) > 0 {
			seg.MentionedPeople = s.MentionedPeople
		}
		out = append(out, seg)
		if len(out) >= maxSegments {
			break
		}
	}

	a.log.Debug("OllamaAnalyzer.FindSegments succeeded",
		zap.String("video_url", videoURL),
		zap.Int("raw_segments", len(rawSegments)),
		zap.Int("validated_segments", len(out)))
	return out, nil
}

// isLowValueMonitorSegmentName filters intro/outro/sponsor markers
// from segment names. Sole consumer is FindSegments above.
//
// Migrated unchanged from pre-Step-9 monitor/segment_finder.go.
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
