package clipsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"velox/go-master/internal/nlp"
)

func (s *Service) pickYouTubeMoment(ctx context.Context, keyword string, meta *YouTubeClipMetadata) SelectedMoment {
	minDur := getenvInt("VELOX_YOUTUBE_MOMENT_MIN_SEC", 20)
	maxDur := getenvInt("VELOX_YOUTUBE_MOMENT_MAX_SEC", 55)
	if minDur < 6 {
		minDur = 6
	}
	if maxDur < minDur {
		maxDur = minDur
	}
	fallback := SelectedMoment{
		StartSec: 0,
		EndSec:   float64(minDur),
		Reason:   "fallback",
		Source:   "default",
	}

	if meta == nil {
		return fallback
	}
	if meta.DurationSec > 0 && fallback.EndSec > meta.DurationSec {
		fallback.EndSec = meta.DurationSec
	}
	if len(meta.TranscriptSegments) > 0 {
		if fromGemma, ok := s.pickMomentWithGemma(ctx, keyword, meta, minDur, maxDur); ok {
			return fromGemma
		}
		if fromHeuristic, ok := pickMomentHeuristicFromSegments(keyword, meta.TranscriptSegments, minDur, maxDur); ok {
			return fromHeuristic
		}
	}
	if meta.DurationSec > float64(maxDur) {
		start := (meta.DurationSec - float64(maxDur)) / 2
		return SelectedMoment{
			StartSec: start,
			EndSec:   start + float64(maxDur),
			Reason:   "centered fallback without transcript",
			Source:   "fallback-duration",
		}
	}
	return fallback
}

type gemmaMomentResponse struct {
	Moments []struct {
		StartSec float64 `json:"start_sec"`
		EndSec   float64 `json:"end_sec"`
		Reason   string  `json:"reason"`
	} `json:"moments"`
}

func (s *Service) pickMomentWithGemma(ctx context.Context, keyword string, meta *YouTubeClipMetadata, minDur, maxDur int) (SelectedMoment, bool) {
	moments := s.pickMomentsWithGemma(ctx, keyword, meta, minDur, maxDur, 1)
	if len(moments) == 0 {
		return SelectedMoment{}, false
	}
	return moments[0], true
}

func (s *Service) pickMomentsWithGemma(ctx context.Context, keyword string, meta *YouTubeClipMetadata, minDur, maxDur, maxMoments int) []SelectedMoment {
	if s.ollama == nil || meta == nil || len(meta.TranscriptSegments) == 0 {
		return nil
	}
	lines := make([]string, 0, 40)
	for i, seg := range meta.TranscriptSegments {
		if i >= 40 {
			break
		}
		txt := strings.TrimSpace(seg.Text)
		if txt == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%.1f-%.1f] %s", seg.StartSec, seg.EndSec, txt))
	}
	if len(lines) == 0 {
		return nil
	}

	if maxMoments <= 0 {
		maxMoments = 1
	}
	prompt := fmt.Sprintf(`Select the best interview moments for keyword "%s".
Return ONLY JSON:
{"moments":[{"start_sec":12.0,"end_sec":42.0,"reason":"short reason"}]}
Rules:
- relevant to keyword
- avoid intro/outro/ads
- duration between %d and %d seconds
- return up to %d moments ranked best-first

Transcript:
%s`, keyword, minDur, maxDur, maxMoments, strings.Join(lines, "\n"))

	gctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	raw, err := s.ollama.Generate(gctx, prompt)
	if err != nil {
		return nil
	}

	var parsed gemmaMomentResponse
	if err := json.Unmarshal([]byte(extractJSONObject(raw)), &parsed); err != nil {
		return nil
	}
	if len(parsed.Moments) == 0 {
		return nil
	}
	out := make([]SelectedMoment, 0, len(parsed.Moments))
	for _, m := range parsed.Moments {
		if m.EndSec <= m.StartSec {
			continue
		}
		dur := m.EndSec - m.StartSec
		if dur < float64(minDur) {
			m.EndSec = m.StartSec + float64(minDur)
		}
		if dur > float64(maxDur) {
			m.EndSec = m.StartSec + float64(maxDur)
		}
		if meta.DurationSec > 0 && m.EndSec > meta.DurationSec {
			m.EndSec = meta.DurationSec
		}
		if m.StartSec < 0 {
			m.StartSec = 0
		}
		out = append(out, SelectedMoment{
			StartSec: m.StartSec,
			EndSec:   m.EndSec,
			Reason:   strings.TrimSpace(m.Reason),
			Source:   "gemma",
		})
		if len(out) >= maxMoments {
			break
		}
	}
	return out
}

func pickMomentHeuristicFromSegments(keyword string, segments []TranscriptSegment, minDur, maxDur int) (SelectedMoment, bool) {
	if len(segments) == 0 {
		return SelectedMoment{}, false
	}
	vtt := &nlp.VTT{Segments: make([]nlp.Segment, 0, len(segments))}
	for _, s := range segments {
		vtt.Segments = append(vtt.Segments, nlp.Segment{
			Start: s.StartSec,
			End:   s.EndSec,
			Text:  s.Text,
		})
	}
	tokens := keywordSearchTokens(keyword)
	if len(tokens) == 0 {
		tokens = strings.Fields(strings.ToLower(keyword))
	}
	moments := nlp.ExtractMoments(vtt, tokens, 3)
	if len(moments) == 0 {
		return SelectedMoment{}, false
	}
	best := moments[0]
	start := best.StartTime
	end := best.EndTime
	if end <= start {
		end = start + float64(minDur)
	}
	if end-start < float64(minDur) {
		end = start + float64(minDur)
	}
	if end-start > float64(maxDur) {
		end = start + float64(maxDur)
	}
	return SelectedMoment{
		StartSec: start,
		EndSec:   end,
		Reason:   strings.TrimSpace(best.Text),
		Score:    best.Score,
		Source:   "nlp-fallback",
	}, true
}

var jsonObjectRe = regexp.MustCompile(`\{[\s\S]*\}`)

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "{}"
	}
	m := jsonObjectRe.FindString(s)
	if m == "" {
		return "{}"
	}
	return m
}

func getenvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
