package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
)

// estimateSemanticSegments estimates the number of semantic segments in text.
func estimateSemanticSegments(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 1
	}
	headingCount := 0
	for _, paragraph := range semanticParagraphs(text) {
		if isSemanticHeading(paragraph) {
			headingCount++
		}
	}
	if headingCount > 0 {
		return headingCount
	}
	paragraphs := 0
	for _, paragraph := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(paragraph) != "" {
			paragraphs++
		}
	}
	switch {
	case paragraphs >= 9:
		return 4
	case paragraphs >= 6:
		return 3
	case paragraphs >= 3:
		return 2
	default:
		return 1
	}
}

// semanticParagraphs splits text into semantic paragraphs.
func semanticParagraphs(text string) []string {
	paragraphs := make([]string, 0, 8)
	for _, paragraph := range strings.Split(strings.TrimSpace(text), "\n\n") {
		paragraph = cleanTimelineSentence(strings.TrimSpace(paragraph))
		if paragraph == "" || paragraph == "---" {
			continue
		}
		paragraphs = append(paragraphs, paragraph)
	}
	return paragraphs
}

// isSemanticHeading checks if text is a semantic heading.
func isSemanticHeading(text string) bool {
	text = cleanTimelineSentence(text)
	if text == "" {
		return false
	}
	words := strings.Fields(text)
	if len(words) == 0 || len(words) > 12 {
		return false
	}
	if strings.Contains(text, "—") || strings.Contains(text, ":") {
		return true
	}
	return len(words) <= 6
}

// compressSemanticTimelinePlan compresses the plan to target segment count.
func compressSemanticTimelinePlan(plan *timelineLLMPlan, target int) *timelineLLMPlan {
	if plan == nil || len(plan.Segments) <= target {
		return plan
	}

	for len(plan.Segments) > target {
		idx := chooseSemanticMergeIndex(plan.Segments)
		if idx < 0 || idx >= len(plan.Segments)-1 {
			break
		}
		plan.Segments[idx] = mergeTimelineSegments(plan.Segments[idx], plan.Segments[idx+1])
		plan.Segments = append(plan.Segments[:idx+1], plan.Segments[idx+2:]...)
	}

	for i := range plan.Segments {
		plan.Segments[i].Index = i
	}
	return plan
}

// chooseSemanticMergeIndex chooses the best segment index to merge.
func chooseSemanticMergeIndex(segments []timelineLLMSegment) int {
	if len(segments) < 2 {
		return 0
	}

	minIdx := 0
	minWords := segmentWordCount(segments[0]) + segmentWordCount(segments[1])

	for i := 1; i < len(segments)-1; i++ {
		w := segmentWordCount(segments[i]) + segmentWordCount(segments[i+1])
		if w < minWords {
			minWords = w
			minIdx = i
		}
	}
	return minIdx
}

// segmentWordCount counts words in a segment.
func segmentWordCount(seg timelineLLMSegment) int {
	count := 0
	for _, k := range seg.Keywords {
		count += len(strings.Fields(k))
	}
	for _, e := range seg.Entities {
		count += len(strings.Fields(e))
	}
	return count + len(strings.Fields(seg.OpeningSentence)) + len(strings.Fields(seg.ClosingSentence))
}

// mergeTimelineSegments merges two timeline segments.
func mergeTimelineSegments(a, b timelineLLMSegment) timelineLLMSegment {
	keywords := make([]string, 0, len(a.Keywords)+len(b.Keywords))
	keywords = append(keywords, a.Keywords...)
	keywords = append(keywords, b.Keywords...)

	entities := make([]string, 0, len(a.Entities)+len(b.Entities))
	entities = append(entities, a.Entities...)
	entities = append(entities, b.Entities...)

	return timelineLLMSegment{
		Index:               a.Index,
		StartTime:           a.StartTime,
		EndTime:             b.EndTime,
		OpeningSentence:     a.OpeningSentence,
		ClosingSentence:     b.ClosingSentence,
		Keywords:            uniqueStrings(keywords),
		Entities:            uniqueStrings(entities),
		PreferredStockGroup: choosePreferredGroup(a.PreferredStockGroup, b.PreferredStockGroup),
		PreferredStockPaths: uniqueStrings(append(a.PreferredStockPaths, b.PreferredStockPaths...)),
	}
}

// choosePreferredGroup chooses the preferred group from two options.
func choosePreferredGroup(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// buildTimelinePlanFromSourceText builds a timeline plan from source text.
func buildTimelinePlanFromSourceText(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative, focus, dataDir string) *TimelinePlan {
	segmentCount := estimateSemanticSegments(narrative)
	discovery := buildDiscoveryContext(dataDir, req.Topic, 12, 5)
	rawPlan, err := requestSourceTextTimelinePlan(ctx, gen, req, narrative, focus, discovery, segmentCount)
	if err != nil {
		rawPlan = fallbackSemanticTimelinePlan(req, narrative, focus, segmentCount)
	}

	rawPlan = compressSemanticTimelinePlan(rawPlan, segmentCount)
	plan := normalizeSemanticTimelinePlan(rawPlan, req, narrative, focus, segmentCount)
	return plan
}

// requestSourceTextTimelinePlan requests a timeline plan for source text from LLM.
func requestSourceTextTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative, focus, discovery string, segmentCount int) (*timelineLLMPlan, error) {
	client := gen.GetClient()
	if client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}

	prompt := fmt.Sprintf(`Sei un editor video molto veloce.

Devi dividere il testo sorgente in %d segmenti semantici.
Non usare simboli markdown, non usare titoli esistenti come delimitatori, non dipendere da ###, --- o intestazioni.
Devi capire da solo dove finisce un tema e ne inizia un altro.

Durata totale: %d secondi.
Argomento/protagonista: %s.

TESTO:
%s

REGOLE:
- Restituisci SOLO JSON puro.
- Ogni segmento deve avere index, start_time, end_time, opening_sentence, closing_sentence, keywords, entities.
- Le frasi opening/closing devono essere prese dal testo sorgente, non inventate.
- Segmenta per cambi di argomento o parentesi narrative.
- L'introduzione generale va nel primo segmento, non in un blocco a parte.
- La chiusura generale va nell'ultimo segmento, non in un blocco a parte.
- Per ogni segmento, se possibile, aggiungi uno o più percorsi stock reali nella chiave preferred_stock_paths scegliendoli solo dal catalogo disponibile.
- Non inserire spiegazioni, markdown o testo extra.

DISCOVERY STOCK DISPONIBILE:
%s

JSON:`, segmentCount, req.Duration, focus, narrative, discovery)

	options := map[string]interface{}{
		"temperature": 0.15,
		"num_predict": 1400,
	}

	raw, err := client.GenerateWithOptions(ctx, "gemma3:4b", prompt, options)
	if err != nil {
		return nil, err
	}

	cleaned := stripCodeFence(raw)
	jsonPayload := extractJSONObject(cleaned)
	if jsonPayload == "" {
		return nil, fmt.Errorf("source timeline response did not contain JSON")
	}

	var plan timelineLLMPlan
	if err := json.Unmarshal([]byte(jsonPayload), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// fallbackSemanticTimelinePlan creates a fallback semantic timeline plan.
func fallbackSemanticTimelinePlan(req ScriptDocsRequest, narrative, focus string, segmentCount int) *timelineLLMPlan {
	segments := make([]timelineLLMSegment, 0, segmentCount)
	total := float64(req.Duration)
	segDuration := total / float64(segmentCount)

	for i := 0; i < segmentCount; i++ {
		start := float64(i) * segDuration
		end := float64(i+1) * segDuration
		if i == segmentCount-1 {
			end = total
		}
		segments = append(segments, timelineLLMSegment{
			Index:           i,
			StartTime:       roundSeconds(start),
			EndTime:         roundSeconds(end),
			OpeningSentence: "",
			ClosingSentence: "",
			Keywords:        collectTopicTerms(req.Topic),
			Entities:        []string{focus},
		})
	}

	return &timelineLLMPlan{
		PrimaryFocus: focus,
		Segments:     segments,
	}
}

// normalizeSemanticTimelinePlan normalizes a semantic timeline plan to TimelinePlan.
func normalizeSemanticTimelinePlan(plan *timelineLLMPlan, req ScriptDocsRequest, narrative, focus string, segmentCount int) *TimelinePlan {
	if plan == nil || len(plan.Segments) == 0 {
		plan = fallbackSemanticTimelinePlan(req, narrative, focus, segmentCount)
	}

	total := float64(req.Duration)
	segDuration := total / float64(len(plan.Segments))
	segments := make([]TimelineSegment, 0, len(plan.Segments))

	for i, seg := range plan.Segments {
		start := float64(i) * segDuration
		end := float64(i+1) * segDuration
		if i == len(plan.Segments)-1 {
			end = total
		}

		segments = append(segments, TimelineSegment{
			Index:               i,
			StartTime:           roundSeconds(start),
			EndTime:             roundSeconds(end),
			Timestamp:           formatTimestamp(roundSeconds(start), roundSeconds(end)),
			OpeningSentence:     firstSentence(seg.OpeningSentence),
			ClosingSentence:     lastSentence(seg.ClosingSentence),
			Keywords:            uniqueStrings(append(seg.Keywords, collectTopicTerms(req.Topic)...)),
			Entities:            uniqueStrings(append(seg.Entities, focus)),
			PreferredStockGroup: seg.PreferredStockGroup,
			PreferredStockPaths: seg.PreferredStockPaths,
		})
	}

	return &TimelinePlan{
		PrimaryFocus:  focus,
		SegmentCount:  len(segments),
		TotalDuration: req.Duration,
		Segments:      segments,
	}
}