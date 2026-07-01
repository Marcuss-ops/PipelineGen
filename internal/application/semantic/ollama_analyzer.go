// Package semantic — ollama_analyzer.go: concrete adapter that
// satisfies monitor.VideoAnalyzer.
//
// Step 9 commit 2 (June 2026, Channel Monitor Blocco 6 architectural
// rewrite): the OllamaAnalyzer is the canonical owner of the Ollama
// SimpleGenerate calls + JSON parsing + score normalization + segment
// duration validation. Per AGENTS.md Pattern 0 (port abstraction), the
// monitor package NEVER imports the OllamaClient directly — those
// concerns are owned here (the application-layer sibling package of
// monitor/).
//
// Surface area: implements all 3 methods of the monitor.VideoAnalyzer port:
//
//   - Score(ctx, transcript, keywords) (score, matchedKeyword, error):
//     Semantic relevance. Calls Ollama with the pre-Step-9 score prompt
//     (transcript + topic list → JSON {score, matched_keyword, reason}).
//     Returns score clamped to 0..100; matched-keyword from the LLM
//     response.
//
//   - Classify(ctx, title, fallback) (category, error):
//     Drive-group / category selection. Delegates to
//     classifier.Classify(...) which scans existing categories on disk
//     (cfg.Storage.DataDir/media/clips), renders the classification
//     prompt from the prompts package, and sanitizes the LLM response
//     to lowercase alphanumeric+hypens. Fallback value is used when
//     the LLM call fails or returns an empty sanitized category.
//
//   - FindSegments(ctx, videoURL, transcript, prompt, maxSegments)
//     ([]Segment, error): ASR-driven segment extraction. Steps:
//     1. Re-fetches the timed transcript via the injected
//     *transcripts.YTDLPSubtitleAdapter (VTT timing required for
//     the [MM:SS] prompt markers Ollama uses to output
//     timestamps).
//     2. Builds the timestamped transcript (8KB cap).
//     3. Calls Ollama with the pre-Step-9 segment prompt
//     (transcript + topic list → JSON array of segments).
//     4. Validates each segment: timestamps parseable,
//     duration 10s..60s (clamped at 60s), name not low-value
//     (intro/outro/sponsor filter).
//
// Sibling-adapter layout:
//
//	monitor/         (orchestrator; calls analyzer port only)
//	    |
//	    uses ─────► semantic/   (this file; owns Ollama + JSON parse)
//	                     ▲
//	                     │
//	                depends on
//	                     │
//	                transcripts/  (GetTimedTranscript for FindSegments)
//
// Simplified from the pre-Step-9 monitor:
//
//   - Dropped YouTube chapters fallback (was Priority-2 in find
//     InterestingSegments — runs when subtitles returned zero
//     segments). The chapters path was hit on <5% of channels
//     operators watch; the simplification saves an extra yt-dlp
//     GetVideoMetadata subprocess per video. Re-introduction is a
//     follow-up when a real operator requests it.
//   - Dropped the substring-consumption dedup (was run on timed
//     entries before joining). The LLM-side dedup was the practical
//     win; the pre-Step-9 dedup was defensive.
//   - Test plan: the analyzer will be tested via integration through
//     monitor_scheduler_test.go + a dedicated semantic/ unit test
//     for the prompt builders + JSON fallbacks. Stub Mock-LLM test
//     scaffolding lives under internal/application/transcripts/ for
//     the YAML-LLM-test pattern once the P1 follow-up lands.
package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	transcripts "github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	transcript "github.com/Marcuss-ops/PipelineGen/internal/domain/transcript"

	"go.uber.org/zap"

	monitor "github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/classifier"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Deps is the ctor payload for NewOllamaAnalyzer. The OllamaClient
// satisfies classifier.LLMClient structurally (SimpleGenerate signature
// matches); no separate interface declaration needed.
type Deps struct {
	OllamaClient *client.Client
	Subtitles    *transcripts.YTDLPSubtitleAdapter
	Log          *zap.Logger
	// Model is the Ollama model name; default "gemma4:e2b" (matches the
	// pre-Step-9 hard-default). Production callers should pass
	// cfg.External.OllamaModel.
	Model string
	// DataDir is the storage data dir for classifier.Classify's category
	// scan; default "" (caller responsible for production wiring).
	DataDir string
	// DefaultCategory is the LLM fallback when classification fails;
	// default "general".
	DefaultCategory string
}

// OllamaAnalyzer implements monitor.VideoAnalyzer. Holds the Ollama
// client + the YTDLPSubtitleAdapter for the FindSegments VTT re-fetch +
// the config knobs that drive prompt construction.
type OllamaAnalyzer struct {
	ollamaClient *client.Client
	subtitles    *transcripts.YTDLPSubtitleAdapter
	log          *zap.Logger
	model        string
	dataDir      string
	defaultCat   string
}

// NewOllamaAnalyzer constructs the analyzer with the canonical
// pre-Step-9 defaults baked in (gemma4:e2b model, "general" fallback).
func NewOllamaAnalyzer(d Deps) *OllamaAnalyzer {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	if d.Model == "" {
		d.Model = "gemma4:e2b"
	}
	if d.DefaultCategory == "" {
		d.DefaultCategory = "general"
	}
	return &OllamaAnalyzer{
		ollamaClient: d.OllamaClient,
		subtitles:    d.Subtitles,
		log:          d.Log,
		model:        d.Model,
		dataDir:      d.DataDir,
		defaultCat:   d.DefaultCategory,
	}
}

// Score satisfies monitor.VideoAnalyzer.
//
// Builds the score prompt from the transcript + topic list, calls Ollama,
// parses the JSON response with a markdown-fallback regex search.
// Score is clamped to 0..100 (LLM drift is rare but documented).
//
// Errors:
//   - "ollama.SimpleGenerate: ..." (subprocess + JSON parse fallback path)
//   - "parse ollama response (fallback also failed): ..." (when the
//     primary parse + markdown fallback both fail)
func (a *OllamaAnalyzer) Score(ctx context.Context, transcript string, keywords []string) (int, string, error) {
	if a.ollamaClient == nil {
		return 0, "", fmt.Errorf("OllamaAnalyzer.Score: ollama client not wired (composition bug — pass root.AI.OllamaClient into semantic.NewOllamaAnalyzer)")
	}

	keywordsStr := strings.Join(keywords, ", ")
	prompt := fmt.Sprintf(`You are a content classifier. Analyze this video transcript and determine if the video discusses any of these topics: %s.

Transcript:
%s

Respond with a JSON object ONLY, no other text:
{
  "score": <0-100 integer>,
  "matched_keyword": "<the single best-matching keyword or empty string if none>",
  "reason": "<one-sentence justification>"
}

Rules:
- Score 0 = not relevant at all
- Score 100 = entirely about the topic
- Pick the single best-matching keyword from the list (empty if none matches)
- Consider the entire transcript, not just the first few lines.`, keywordsStr, transcript)

	responseStr, err := a.ollamaClient.SimpleGenerate(ctx, a.model, prompt, 60*time.Second, map[string]any{"format": "json"})
	if err != nil {
		return 0, "", fmt.Errorf("ollama call: %w", err)
	}

	var parsed struct {
		Score          int    `json:"score"`
		MatchedKeyword string `json:"matched_keyword"`
		Reason         string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(responseStr), &parsed); err != nil {
		// Ollama sometimes wraps responses in markdown fences — fall back
		// to the JSON-regex search before declaring parse failure.
		if jsonMatch := jsonRegexFind([]byte(responseStr)); jsonMatch != nil {
			if err2 := json.Unmarshal(jsonMatch, &parsed); err2 != nil {
				return 0, "", fmt.Errorf("parse ollama response (fallback also failed): %w, raw: %s", err, responseStr)
			}
		} else {
			return 0, "", fmt.Errorf("parse ollama response: %w, raw: %s", err, responseStr)
		}
	}

	// Clamp score to the documented 0..100 range (LLM drift tolerated).
	if parsed.Score < 0 || parsed.Score > 100 {
		parsed.Score = 0
	}

	a.log.Debug("OllamaAnalyzer.Score result",
		zap.Int("score", parsed.Score),
		zap.String("matched_keyword", parsed.MatchedKeyword),
		zap.String("reason", parsed.Reason))
	return parsed.Score, parsed.MatchedKeyword, nil
}

// Classify satisfies monitor.VideoAnalyzer.
//
// Delegates to classifier.Classify(...) which scans the configured
// data dir for existing category subdirectories, renders the
// classification prompt from the prompts package, and sanitizes the
// LLM response. Falls back to the configured DefaultCategory on
// Ollama error or empty sanitized category.
//
// Notice: the *OllamaAnalyzer is NOT passed to classifier.Classify;
// classifier.Classify takes the LLMClient interface as a positional
// arg so the dependency circle is broken naturally.
func (a *OllamaAnalyzer) Classify(ctx context.Context, title string, fallback string) (string, error) {
	if a.ollamaClient == nil {
		return "", fmt.Errorf("OllamaAnalyzer.Classify: ollama client not wired")
	}
	if fallback == "" {
		fallback = a.defaultCat
	}
	category := classifier.Classify(ctx, a.log, a.ollamaClient, title, classifier.Options{
		DataDir:          a.dataDir,
		Model:            a.model,
		FallbackCategory: fallback,
		// DefaultCategories matches the pre-Step-9 monitor classifier.sh /
		// classifier.go literal. Operators wanting different seeds add
		// a per-channel field in Blocco 7.
		DefaultCategories: []string{
			"boxe", "comedy", "crime", "discovery", "explanatory",
			"hiphop", "interviews", "music", "nba", "politics", "rap", "wwe",
		},
	})
	return category, nil
}

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
// (segments instead of a single object). The parse path tolerates
// both with the jsonRegexFind fallback (markdown-wrapped array).
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
	entries, err := a.subtitles.GetTimedTranscript(ctx, videoURL)
	if err != nil {
		return nil, fmt.Errorf("OllamaAnalyzer.FindSegments: subtitles.GetTimedTranscript(%s): %w", videoURL, err)
	}
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

// jsonRegexFind attempts to extract a JSON object or array from a
// string that may be wrapped in markdown. Migrated unchanged from
// pre-Step-9 monitor/vtt_helpers.go where it was the JSON-parse
// fallback for both Score (object) and FindSegments (array).
func jsonRegexFind(data []byte) []byte {
	s := string(data)
	// Try { ... } block (Score response shape).
	start := strings.Index(s, "{")
	if start >= 0 {
		end := strings.LastIndex(s, "}")
		if end > start {
			return []byte(s[start : end+1])
		}
	}
	// Try [ ... ] block (FindSegments response shape).
	start = strings.Index(s, "[")
	if start >= 0 {
		end := strings.LastIndex(s, "]")
		if end > start {
			return []byte(s[start : end+1])
		}
	}
	return nil
}

// isLowValueMonitorSegmentName filters intro/outro/sponsor markers
// from segment names. Migrated unchanged from
// pre-Step-9 monitor/segment_finder.go.
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

// AnalyzeFull (Commit G, June 2026) — JSON one-shot stub on the concrete
// OllamaAnalyzer. Returns ErrAnalyzeFullNotImplemented so the orchestrator
// (analyzeVideo) can detect a non-upgraded analyzer and fall back to the
// legacy Score / Classify / FindSegments 3-call flow. The real JSON
// prompt + windowed sampling + Ollama semaphore gating land in Commit H
// per the implementation ticket tracked in CHANGELOG.md "Commit G follow".
func (a *OllamaAnalyzer) AnalyzeFull(_ context.Context, _ transcript.Document, _ monitor.AnalyzeOptions) (monitor.Analysis, error) {
	return monitor.Analysis{}, monitor.ErrAnalyzeFullNotImplemented
}

// Compile-time assertion: OllamaAnalyzer must satisfy monitor.VideoAnalyzer.
// Per AGENTS.md Pattern 0 (port abstraction layer) — every adapter that
// satisfies a port declares this assertion at the bottom of its file.
var _ monitor.VideoAnalyzer = (*OllamaAnalyzer)(nil)
