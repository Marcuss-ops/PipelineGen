package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
)

// buildTimelinePlan creates a timeline plan based on the request and narrative.
func buildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative string, analysis *ollama.FullEntityAnalysis, dataDir string) (*TimelinePlan, error) {
	focus := pickTimelineFocus(req.Topic, analysis)

	var plan *TimelinePlan
	if strings.TrimSpace(req.SourceText) != "" {
		plan = buildTimelinePlanFromSourceText(ctx, gen, req, narrative, focus, dataDir)
	} else {
		segmentCount := 1
		rawPlan, err := requestTimelinePlan(ctx, gen, req, narrative, focus, segmentCount)
		if err != nil {
			rawPlan = fallbackTimelinePlan(req, narrative, focus, segmentCount)
		}
		plan = normalizeTimelinePlan(rawPlan, req, narrative, focus, segmentCount)
	}

	enrichTimelineSegments(plan, dataDir, req)
	return plan, nil
}

// requestTimelinePlan requests a timeline plan from the LLM.
func requestTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative, focus string, segmentCount int) (*timelineLLMPlan, error) {
	client := gen.GetClient()
	if client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}

	prompt := buildTimelinePrompt(req, narrative, focus, "", segmentCount)
	options := map[string]interface{}{
		"temperature": 0.2,
		"num_predict": 1024,
	}

	raw, err := client.GenerateWithOptions(ctx, "gemma3:4b", prompt, options)
	if err != nil {
		return nil, err
	}

	cleaned := stripCodeFence(raw)
	jsonPayload := extractJSONObject(cleaned)
	if jsonPayload == "" {
		return nil, fmt.Errorf("timeline plan response did not contain JSON")
	}

	var plan timelineLLMPlan
	if err := json.Unmarshal([]byte(jsonPayload), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// buildTimelinePrompt builds the prompt for timeline generation.
func buildTimelinePrompt(req ScriptDocsRequest, narrative, focus, discovery string, segmentCount int) string {
	catalogBlock := ""
	if strings.TrimSpace(discovery) != "" {
		catalogBlock = fmt.Sprintf(`
DISCOVERY STOCK DISPONIBILE:
%s
`, discovery)
	}

	return fmt.Sprintf(`Sei un editor video molto veloce.

Dividi lo script in un solo segmento consecutivo.
Durata totale: %d secondi.
Argomento/protagonista: %s.

SCRIPT:
%s

REGOLE:
- Restituisci SOLO JSON puro.
- Ci deve essere un solo segmento con index, start_time, end_time, opening_sentence, closing_sentence, keywords, entities.
- start_time deve essere 0 e end_time deve arrivare alla durata totale.
- opening_sentence deve essere la frase iniziale dello script.
- closing_sentence deve essere la frase finale dello script.
- keywords e entities devono essere utili per cercare stock e drive.
- preferred_stock_group deve indicare il group della cartella dove andrà il voiceover.
- preferred_stock_paths deve usare solo cartelle presenti nel discovery.
- Non includere immagini o immagini-soggetto.
%s

JSON:`,
		req.Duration,
		focus,
		narrative,
		catalogBlock,
	)
}

// pickTimelineFocus picks the focus for the timeline based on analysis.
func pickTimelineFocus(topic string, analysis *ollama.FullEntityAnalysis) string {
	if analysis != nil {
		for _, seg := range analysis.SegmentEntities {
			for name := range seg.EntitaSenzaTesto {
				if strings.TrimSpace(name) != "" {
					return name
				}
			}
			if len(seg.NomiSpeciali) > 0 {
				return seg.NomiSpeciali[0]
			}
		}
	}
	return strings.TrimSpace(topic)
}

// fallbackTimelinePlan creates a fallback timeline plan.
func fallbackTimelinePlan(req ScriptDocsRequest, narrative, focus string, segmentCount int) *timelineLLMPlan {
	opening, closing := extractOpeningAndClosingSentence(narrative)
	total := float64(req.Duration)
	segments := []timelineLLMSegment{
		{
			Index:           0,
			StartTime:       0,
			EndTime:         roundSeconds(total),
			OpeningSentence: opening,
			ClosingSentence: closing,
			Keywords:        collectTopicTerms(req.Topic),
			Entities:        []string{focus},
		},
	}

	return &timelineLLMPlan{
		PrimaryFocus: focus,
		Segments:     segments,
	}
}

// normalizeTimelinePlan normalizes the LLM timeline plan to a TimelinePlan.
func normalizeTimelinePlan(plan *timelineLLMPlan, req ScriptDocsRequest, narrative, focus string, segmentCount int) *TimelinePlan {
	if plan == nil {
		plan = fallbackTimelinePlan(req, narrative, focus, segmentCount)
	}

	var first timelineLLMSegment
	var last timelineLLMSegment
	if len(plan.Segments) > 0 {
		first = plan.Segments[0]
		last = plan.Segments[len(plan.Segments)-1]
	}

	// Always extract from actual narrative to be sure we get the real first/last sentences
	opening, closing := extractOpeningAndClosingSentence(narrative)
	if opening == "" || closing == "" {
		plan = fallbackTimelinePlan(req, narrative, focus, segmentCount)
		return normalizeTimelinePlan(plan, req, narrative, focus, segmentCount)
	}

	segments := []TimelineSegment{
		{
			Index:           0,
			StartTime:       0,
			EndTime:         roundSeconds(float64(req.Duration)),
			Timestamp:       formatTimestamp(0, float64(req.Duration)),
			OpeningSentence: opening,
			ClosingSentence: closing,
			Keywords:        uniqueStrings(append(append(first.Keywords, last.Keywords...), collectTopicTerms(req.Topic)...)),
			Entities:        uniqueStrings(append(append(first.Entities, last.Entities...), focus)),
		},
	}

	return &TimelinePlan{
		PrimaryFocus:  focus,
		SegmentCount:  1,
		TotalDuration: req.Duration,
		Segments:      segments,
	}
}
