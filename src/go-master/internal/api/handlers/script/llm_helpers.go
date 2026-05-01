package script

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/matching"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
)

func chooseTimelinePlanWithLLM(ctx context.Context, gen *ollama.Generator, topic string, duration int, sourceText, narrative string) (*timelineLLMPlan, error) {
	if gen == nil || gen.GetClient() == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("invalid duration")
	}

	client := gen.GetClient()
	model := client.Model()
	if strings.TrimSpace(model) == "" {
		model = types.DefaultModel
	}

	prompt := fmt.Sprintf(`You are a documentary timeline editor.

Split the script into the most natural topical segments.

Rules:
- The overall topic is: %s
- Every segment subject must stay close to the topic and its immediate subtopic.
- Never output file names, path fragments, or unrelated people/places.
- Keep each subject short and human-readable, ideally 2 to 6 words.
- Divide by argument or topic shifts, not by a fixed number of segments.
- Create as many segments as the narrative needs.
- Keep segments in the same order as the script.
- Each segment must be contiguous and represent one coherent idea.
- If the source material contains obvious section headings or subject transitions, use them to preserve the natural chapter structure.
- For a script built from clearly separated subjects, return one segment per subject block.
- Use timestamps in seconds from 0 to %d.
- The first segment must start at 0.
- The last segment must end exactly at %d.
- Segments must not overlap.
- Return ONLY valid JSON with this shape:
{
  "primary_focus": "short title of the main subject",
  "segments": [
    {
      "index": 1,
      "start_time": 0,
      "end_time": 18.5,
      "subject": "Main person or specific topic of this block (e.g. 'Mike Tyson' or 'Amish lifestyle')",
      "narrative_text": "exact excerpt from the script for this segment",
      "opening_sentence": "opening sentence or excerpt",
      "closing_sentence": "closing sentence or excerpt",
      "keywords": ["optional", "keywords"],
      "entities": ["optional", "entities"]
    }
  ]
}

SCRIPT:
%s

SOURCE MATERIAL:
%s

JSON:`, truncateString(topic, 200), duration, duration, truncateString(narrative, 6000), truncateString(sourceText, 6000))

	raw, err := client.GenerateWithOptions(ctx, model, prompt, map[string]interface{}{
		"temperature": 0.0,
		"num_predict": 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("timeline planning failed: %w", err)
	}

	zap.L().Info("Raw LLM timeline response", zap.String("raw", raw))

	cleaned := stripCodeFence(raw)
	jsonPayload := extractJSONObject(cleaned)
	if jsonPayload == "" {
		return nil, fmt.Errorf("timeline planning returned empty payload")
	}

	var plan timelineLLMPlan
	if err := json.Unmarshal([]byte(jsonPayload), &plan); err != nil {
		return nil, fmt.Errorf("timeline planning returned invalid json: %w", err)
	}

	if normalized := normalizeTimelineLLMPlan(&plan, duration); normalized != nil {
		sanitizeTimelineLLMPlan(normalized, topic)
		return normalized, nil
	}

	return nil, fmt.Errorf("timeline planning returned unusable segments")
}

func sanitizeTimelineLLMPlan(plan *timelineLLMPlan, topic string) {
	if plan == nil {
		return
	}
	topicTokens := topicTokens(topic)
	for i := range plan.Segments {
		seg := &plan.Segments[i]
		if shouldReplaceLLMSubject(seg.Subject) || !subjectMatchesTopic(seg.Subject, topicTokens) {
			seg.Subject = deriveFallbackSubject(seg, topic, topicTokens)
		}
		if entitySubject := preferredEntitySubject(seg, topicTokens); entitySubject != "" {
			seg.Subject = entitySubject
		}
		if seg.Subject == "" {
			seg.Subject = topic
		}
	}
	if strings.TrimSpace(plan.PrimaryFocus) == "" || !subjectMatchesTopic(plan.PrimaryFocus, topicTokens) {
		plan.PrimaryFocus = topic
	}
}

func shouldReplaceLLMSubject(subject string) bool {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return true
	}
	lower := strings.ToLower(subject)
	if strings.Contains(lower, ".mp4") || strings.Contains(lower, ".mov") || strings.Contains(lower, ".m3u8") {
		return true
	}
	if strings.Contains(lower, "/") || strings.Contains(lower, "\\") || strings.Contains(lower, "|") {
		return true
	}
	if len(strings.Fields(subject)) > 8 {
		return true
	}
	return false
}

func subjectMatchesTopic(subject string, topicTokens []string) bool {
	if len(topicTokens) == 0 {
		return true
	}
	subjectTokens := topicTokensFromText(subject)
	if len(subjectTokens) == 0 {
		return false
	}
	matches := 0
	for _, tok := range subjectTokens {
		for _, tt := range topicTokens {
			if strings.EqualFold(tok, tt) {
				matches++
				break
			}
		}
	}
	return matches > 0
}

func deriveFallbackSubject(seg *timelineLLMSegment, topic string, topicTokens []string) string {
	if entitySubject := preferredEntitySubject(seg, topicTokens); entitySubject != "" {
		return entitySubject
	}
	candidates := []string{
		seg.Subject,
		seg.OpeningSentence,
		seg.ClosingSentence,
	}
	for _, candidate := range candidates {
		if s := conciseSubject(candidate); s != "" {
			return s
		}
	}
	return topic
}

func preferredEntitySubject(seg *timelineLLMSegment, topicTokens []string) string {
	if seg == nil {
		return ""
	}
	candidates := uniqueStrings(append([]string{}, seg.Entities...))
	candidates = append(candidates, seg.Subject)

	best := ""
	bestScore := 0
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		score := 0
		if subjectMatchesTopic(candidate, topicTokens) {
			score += 50
		}
		if looksLikePersonName(candidate) {
			score += 20
		}
		words := strings.Fields(candidate)
		if len(words) >= 2 && len(words) <= 4 {
			score += 15
		}
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}
	if bestScore < 50 {
		return ""
	}
	return best
}

func looksLikePersonName(text string) bool {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || len(parts) > 5 {
		return false
	}
	score := 0
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		first := []rune(part)[0]
		if first >= 'A' && first <= 'Z' {
			score++
		}
	}
	return score >= 1 && len(parts) <= 4
}

func conciseSubject(text string) string {
	tokens := topicTokensFromText(text)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) > 5 {
		tokens = tokens[:5]
	}
	return strings.Join(tokens, " ")
}

func topicTokens(topic string) []string {
	return topicTokensFromText(topic)
}

func topicTokensFromText(text string) []string {
	tokens := matching.Tokenize(text)
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		out = append(out, tok)
	}
	return out
}

func normalizeTimelineLLMPlan(plan *timelineLLMPlan, duration int) *timelineLLMPlan {
	if plan == nil || len(plan.Segments) == 0 || duration <= 0 {
		return nil
	}

	segments := make([]timelineLLMSegment, 0, len(plan.Segments))
	for _, seg := range plan.Segments {
		if strings.TrimSpace(seg.NarrativeText) == "" && strings.TrimSpace(seg.OpeningSentence) == "" && strings.TrimSpace(seg.ClosingSentence) == "" {
			continue
		}
		if seg.EndTime <= seg.StartTime {
			continue
		}
		segments = append(segments, seg)
	}
	if len(segments) == 0 {
		return nil
	}

	sort.SliceStable(segments, func(i, j int) bool {
		if segments[i].StartTime == segments[j].StartTime {
			if segments[i].EndTime == segments[j].EndTime {
				return segments[i].Index < segments[j].Index
			}
			return segments[i].EndTime < segments[j].EndTime
		}
		return segments[i].StartTime < segments[j].StartTime
	})

	dur := float64(duration)
	if segments[0].StartTime < 0 {
		segments[0].StartTime = 0
	}
	if segments[0].StartTime > 0 {
		segments[0].StartTime = 0
	}

	prevEnd := 0.0
	for i := range segments {
		if i == 0 {
			segments[i].StartTime = 0
		} else if segments[i].StartTime < prevEnd {
			segments[i].StartTime = prevEnd
		}

		if segments[i].StartTime > dur {
			segments[i].StartTime = dur
		}
		if segments[i].EndTime > dur {
			segments[i].EndTime = dur
		}

		if i == len(segments)-1 {
			segments[i].EndTime = dur
		}
		if segments[i].EndTime <= segments[i].StartTime {
			return nil
		}
		prevEnd = segments[i].EndTime
	}

	for i := range segments {
		segments[i].Index = i + 1
		segments[i].StartTime = roundSeconds(segments[i].StartTime)
		segments[i].EndTime = roundSeconds(segments[i].EndTime)
		if i == 0 {
			segments[i].StartTime = 0
		}
		if i == len(segments)-1 {
			segments[i].EndTime = dur
		}
	}

	plan.Segments = segments
	if strings.TrimSpace(plan.PrimaryFocus) == "" {
		plan.PrimaryFocus = "Timeline"
	}
	return plan
}
