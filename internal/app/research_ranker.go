package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

type ollamaResearchRanker struct {
	client interface {
		SimpleGenerate(context.Context, string, string, time.Duration, map[string]any) (string, error)
	}
	model string
}

type rankingEvidence struct {
	CandidateID string   `json:"candidate_id"`
	Label       string   `json:"label"`
	Sources     []string `json:"sources"`
	Claims      []string `json:"verified_claims"`
}

func (r *ollamaResearchRanker) Rank(ctx context.Context, topic string, inputs []scriptports.ResearchCandidateRankingInput) ([]scriptports.ResearchCandidateRanking, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("research ranker: ollama client is not configured")
	}
	evidence := make([]rankingEvidence, 0, len(inputs))
	for _, input := range inputs {
		item := rankingEvidence{CandidateID: input.CandidateID, Label: input.Label}
		for _, source := range input.Sources {
			item.Sources = append(item.Sources, strings.TrimSpace(source.Title+" — "+source.URL))
		}
		for _, claim := range input.Claims {
			if claim.Verified {
				item.Claims = append(item.Claims, trimRankerText(claim.Text, 500))
			}
		}
		evidence = append(evidence, item)
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("research ranker: input encode: %w", err)
	}
	prompt := fmt.Sprintf("You are the canonical editorial ranking resolver. Rank the researched candidates for %q using documented financial evidence, business proceeds, fight earnings and retained-wealth context. Evidence quality is a gate, not a ranking signal. Do not invent numbers. Return JSON only as an array of objects with candidate_id, rank, score, rationale. Include every candidate exactly once and use every rank from 1 through %d.\nEVIDENCE:\n%s", topic, len(inputs), payload)
	output, err := r.client.SimpleGenerate(ctx, r.model, prompt, 120*time.Second, map[string]any{"format": "json"})
	if err != nil {
		return nil, fmt.Errorf("research ranker: generation: %w", err)
	}
	ranking, parseErr := parseResearchRanking(output)
	if parseErr != nil {
		retryPrompt := fmt.Sprintf("Return ONLY a valid JSON array. Rank every candidate exactly once from 1 to %d. Schema: [{\"candidate_id\":\"...\",\"rank\":1,\"score\":0,\"rationale\":\"...\"}]. Candidates and source titles: %s", len(inputs), payload)
		retryOutput, retryErr := r.client.SimpleGenerate(ctx, r.model, retryPrompt, 120*time.Second, map[string]any{"format": "json"})
		if retryErr == nil {
			ranking, parseErr = parseResearchRanking(retryOutput)
		}
		if parseErr != nil {
			return fallbackResearchRanking(inputs), nil
		}
	}
	return ranking, nil
}

func fallbackResearchRanking(inputs []scriptports.ResearchCandidateRankingInput) []scriptports.ResearchCandidateRanking {
	type scored struct {
		input scriptports.ResearchCandidateRankingInput
		score float64
	}
	items := make([]scored, 0, len(inputs))
	for _, input := range inputs {
		score := 0.0
		for _, claim := range input.Claims {
			if !claim.Verified {
				continue
			}
			text := strings.ToLower(claim.Text)
			for phrase, weight := range map[string]float64{
				"world champion": 5, "world title": 5, "olympic": 4,
				"championship": 3, "hall of fame": 3, "undefeated": 3,
				"knockout": 1, "title": 2, "won": 1,
			} {
				if strings.Contains(text, phrase) {
					score += weight
				}
			}
		}
		items = append(items, scored{input: input, score: score})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].input.CandidateID < items[j].input.CandidateID
		}
		return items[i].score > items[j].score
	})
	ranking := make([]scriptports.ResearchCandidateRanking, 0, len(items))
	for index, item := range items {
		ranking = append(ranking, scriptports.ResearchCandidateRanking{
			CandidateID: item.input.CandidateID,
			Rank:        index + 1,
			Score:       item.score,
			Rationale:   "Deterministic verified-evidence fallback; model ranking output was invalid.",
		})
	}
	return ranking
}

func parseResearchRanking(output string) ([]scriptports.ResearchCandidateRanking, error) {
	output = strings.TrimSpace(output)
	if start := strings.Index(output, "["); start >= 0 {
		if end := strings.LastIndex(output, "]"); end >= start {
			output = output[start : end+1]
		}
	}
	var ranking []scriptports.ResearchCandidateRanking
	if err := json.Unmarshal([]byte(output), &ranking); err != nil {
		return nil, err
	}
	return ranking, nil
}

func trimRankerText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}
