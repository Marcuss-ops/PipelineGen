package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
)

// buildTimelinePlan creates a timeline plan based on the request and narrative.
func buildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, dataDir string, repo *clips.Repository, nodeScraperDir string) (*TimelinePlan, error) {
	focus := pickTimelineFocus(req.Topic, analysis)

	var plan *TimelinePlan
	if strings.TrimSpace(req.SourceText) != "" {
		plan = buildTimelinePlanFromSourceText(ctx, gen, req, narrative, focus, dataDir)
	} else {
		words := len(strings.Fields(narrative))
		segmentCount := words / 800
		if segmentCount < 1 {
			segmentCount = 1
		}
		rawPlan, err := requestTimelinePlan(ctx, gen, req, narrative, focus, segmentCount)
		if err != nil {
			rawPlan = fallbackTimelinePlan(req, narrative, focus, segmentCount)
		}
		plan = normalizeTimelinePlan(rawPlan, req, narrative, focus, segmentCount)
	}

	enrichTimelineSegments(plan, dataDir, req, repo, ctx, gen, analysis, nodeScraperDir)
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

	return fmt.Sprintf(`Sei un editor video molto veloce e un documentarista esperto.

Dividi lo script in circa %d capitoli logici (in base al cambio di argomento, di situazione o di personaggio). Se ritieni necessario crearne di più o di meno per seguire al meglio la narrazione, sei libero di farlo.
Durata totale stimata del video: %d secondi.
Argomento/protagonista principale: %s.

SCRIPT:
%s

REGOLE:
- Restituisci SOLO JSON puro strutturato come un array di segmenti ("segments": [ ... ]).
- Per OGNI segmento devi fornire: index, start_time, end_time, opening_sentence, closing_sentence, keywords, entities.
- start_time del primo segmento deve essere 0. end_time dell'ultimo segmento deve arrivare alla durata totale stimata. I tempi devono essere sequenziali.
- opening_sentence deve essere la vera frase iniziale del capitolo estratta testualmente dallo script.
- closing_sentence deve essere la vera frase finale del capitolo estratta testualmente dallo script.
- keywords e entities devono essere estremamente specifici per quel preciso capitolo (es. se si parla di Mike Tyson in questo capitolo, metti "Mike Tyson" nelle entities) e utili per cercare stock video in Drive.
- preferred_stock_group deve indicare il group della cartella dove andrà il voiceover per questo capitolo.
- Non includere immagini o immagini-soggetto.
%s

JSON:`,
		segmentCount,
		req.Duration,
		focus,
		narrative,
		catalogBlock,
	)
}

// pickTimelineFocus picks the focus for the timeline based on analysis.
func pickTimelineFocus(topic string, analysis *types.FullEntityAnalysis) string {
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
	if plan == nil || len(plan.Segments) == 0 {
		plan = fallbackTimelinePlan(req, narrative, focus, segmentCount)
	}

	totalDuration := float64(req.Duration)
	segments := make([]TimelineSegment, 0, len(plan.Segments))

	for i, llmSeg := range plan.Segments {
		startTime := llmSeg.StartTime
		endTime := llmSeg.EndTime

		if i == 0 {
			startTime = 0
		}
		if i == len(plan.Segments)-1 {
			endTime = totalDuration
		}
		
		if endTime <= startTime {
			endTime = startTime + (totalDuration / float64(len(plan.Segments)))
		}

		segments = append(segments, TimelineSegment{
			Index:           llmSeg.Index,
			StartTime:       roundSeconds(startTime),
			EndTime:         roundSeconds(endTime),
			Timestamp:       formatTimestamp(startTime, endTime),
			OpeningSentence: llmSeg.OpeningSentence,
			ClosingSentence: llmSeg.ClosingSentence,
			Keywords:        uniqueStrings(append(llmSeg.Keywords, collectTopicTerms(req.Topic)...)),
			Entities:        uniqueStrings(append(llmSeg.Entities, focus)),
		})
	}

	return &TimelinePlan{
		PrimaryFocus:  focus,
		SegmentCount:  len(segments),
		TotalDuration: req.Duration,
		Segments:      segments,
	}
}
