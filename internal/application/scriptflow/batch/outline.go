package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/types"

	"go.uber.org/zap"
)

// generateBatchOutline generates auto-outline topics from outline_topic field.
// Populates req.BatchTopics and req.Chapters (ScriptPlan) with the generated chapters.
func (s *BatchService) generateBatchOutline(ctx context.Context, req *GenerateBatchRequest) error {
	if len(req.BatchTopics) > 0 || strings.TrimSpace(req.OutlineTopic) == "" {
		return nil
	}

	num := req.NumChapters
	if num <= 0 {
		num = 5
	}
	if num > 15 {
		num = 15
	}

	s.log.Info("batch generation: generating ScriptPlan outline for macro-topic", zap.String("topic", req.OutlineTopic), zap.Int("items", num))

	prompt := fmt.Sprintf(
		`Devi creare un piano di scrittura (ScriptPlan) composto da esattamente %d capitoli sequenziali per un libro o script sul tema: '%s'.
Restituisci esclusivamente un oggetto JSON valido con la chiave "chapters" che contiene un array di capitoli. Ogni capitolo deve seguire questa struttura:
{
  "index": 1,
  "title": "Titolo del capitolo",
  "purpose": "Scopo pratico o narrativo del capitolo",
  "key_points": ["punto chiave 1", "punto chiave 2"],
  "emotional_role": "ruolo emotivo (es. curiosity, tension, resolve, hook, trust, logic, action)",
  "retention_goal": "obiettivo di ritenzione dell'attenzione",
  "facts_to_include": ["fatto 1", "fatto 2"],
  "facts_to_avoid": ["cosa evitare 1"],
  "transition_goal": "obiettivo di transizione verso il capitolo successivo",
  "anti_repetition_phrase": ["parole o temi da non ripetere"]
}
Non aggiungere introduzioni, note o altro testo al di fuori del JSON.`,
		num, req.OutlineTopic,
	)

	res, err := s.generator.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
		Language: req.Language,
		Duration: 30,
		Tone:     "outline",
		Model:    req.Model,
		Prompt:   prompt,
	})
	if err != nil {
		return fmt.Errorf("failed to generate outline: %w", err)
	}

	cleanJSON := strings.TrimSpace(res.Script)
	if strings.HasPrefix(cleanJSON, "```") {
		lines := strings.Split(cleanJSON, "\n")
		filtered := make([]string, 0, len(lines))
		for _, line := range lines {
			if !strings.HasPrefix(strings.TrimSpace(line), "```") {
				filtered = append(filtered, line)
			}
		}
		cleanJSON = strings.Join(filtered, "\n")
	}

	// Try parsing as structured ScriptPlan
	var plan ChapterPlanList
	if err := json.Unmarshal([]byte(cleanJSON), &plan); err == nil && len(plan.Chapters) > 0 {
		req.Chapters = plan.Chapters
		req.BatchTopics = make([]BatchTopic, len(plan.Chapters))
		for i, ch := range plan.Chapters {
			req.BatchTopics[i] = BatchTopic{
				Topic:      ch.Title,
				SourceText: "",
			}
		}
		s.log.Info("batch generation: outline ScriptPlan successfully generated and parsed", zap.Int("chapters", len(plan.Chapters)))
		return nil
	}

	// Fallback: Try parsing as flat array of titles (legacy)
	var outlineTopics []string
	if err := json.Unmarshal([]byte(cleanJSON), &outlineTopics); err != nil {
		lines := strings.Split(cleanJSON, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			line = regexp.MustCompile(`^(\d+\.|\-|\*)\s*`).ReplaceAllString(line, "")
			line = strings.Trim(line, `"'[] ,`)
			if line != "" {
				outlineTopics = append(outlineTopics, line)
			}
		}
	}
	if len(outlineTopics) == 0 {
		return fmt.Errorf("could not generate a valid outline of items")
	}
	req.BatchTopics = make([]BatchTopic, len(outlineTopics))
	for i, t := range outlineTopics {
		req.BatchTopics[i] = BatchTopic{Topic: t}
	}
	return nil
}
