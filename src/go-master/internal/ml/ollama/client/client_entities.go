package client

import (
	"velox/go-master/internal/ml/ollama/prompts"
	"velox/go-master/internal/ml/ollama/types"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractEntitiesFromSegment extracts entities from a single text segment using Ollama
func (c *Client) ExtractEntitiesFromSegment(ctx context.Context, req types.EntityExtractionRequest) (*types.EntityExtractionResult, error) {
	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 12
	}

	// Build the entity extraction prompt
	prompt := prompts.BuildEntityExtractionPrompt(req.SegmentText, entityCount)

	// Call Ollama using legacy generate for JSON tasks (often more stable for JSON)
	response, err := c.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("entity extraction failed: %w", err)
	}

	// Parse JSON response
	result, err := parseEntityExtractionResult(response, req.SegmentIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse entity result: %w", err)
	}

	return result, nil
}

// ExtractEntitiesFromScript extracts entities from all segments of a script
func (c *Client) ExtractEntitiesFromScript(ctx context.Context, segments []string, entityCount int) (*types.FullEntityAnalysis, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments provided")
	}

	if entityCount <= 0 {
		entityCount = 12
	}

	analysis := &types.FullEntityAnalysis{
		TotalSegments:         len(segments),
		SegmentEntities:       make([]types.SegmentEntities, 0, len(segments)),
		EntityCountPerSegment: entityCount,
	}

	// Extract entities for each segment
	for i, segment := range segments {
		req := types.EntityExtractionRequest{
			SegmentText:  segment,
			SegmentIndex: i,
			EntityCount:  entityCount,
		}

		result, err := c.ExtractEntitiesFromSegment(ctx, req)
		if err != nil {
			// Continue with empty entities for this segment
			result = &types.EntityExtractionResult{
				SegmentIndex:     i,
				FrasiImportanti:  []string{},
				EntitaSenzaTesto: make(map[string]string),
				NomiSpeciali:     []string{},
				ParoleImportanti: []string{},
			}
		}

		segmentEntities := types.SegmentEntities{
			SegmentIndex:     i,
			SegmentText:      segment,
			FrasiImportanti:  result.FrasiImportanti,
			EntitaSenzaTesto: result.EntitaSenzaTesto,
			NomiSpeciali:     result.NomiSpeciali,
			ParoleImportanti: result.ParoleImportanti,
			ArtlistPhrases:   result.ArtlistPhrases,
		}

		analysis.SegmentEntities = append(analysis.SegmentEntities, segmentEntities)

		// Count total entities
		analysis.TotalEntities += len(result.FrasiImportanti) +
			len(result.EntitaSenzaTesto) +
			len(result.NomiSpeciali) +
			len(result.ParoleImportanti) +
			len(result.ArtlistPhrases)
	}

	return analysis, nil
}

// parseEntityExtractionResult parses the JSON response from Ollama
func parseEntityExtractionResult(response string, segmentIndex int) (*types.EntityExtractionResult, error) {
	jsonStr := strings.TrimSpace(response)

	// Remove markdown code blocks
	if strings.HasPrefix(jsonStr, "```") {
		lines := strings.Split(jsonStr, "\n")
		var contentLines []string
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				continue
			}
			contentLines = append(contentLines, line)
		}
		jsonStr = strings.TrimSpace(strings.Join(contentLines, "\n"))
	}

	// Ultimate fallback: find first { and last }
	start := strings.Index(jsonStr, "{")
	end := strings.LastIndex(jsonStr, "}")
	if start != -1 && end != -1 && end > start {
		jsonStr = jsonStr[start : end+1]
	}

	var raw struct {
		FrasiImportanti  []string        `json:"frasi_importanti"`
		EntitaSenzaTesto json.RawMessage `json:"entity_senza_testo"`
		NomiSpeciali     []string        `json:"nomi_speciali"`
		ParoleImportanti []string        `json:"parole_importanti"`
		ArtlistPhrases   json.RawMessage `json:"artlist_phrases"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	// Ensure slices are not nil
	if raw.FrasiImportanti == nil {
		raw.FrasiImportanti = []string{}
	}
	if raw.NomiSpeciali == nil {
		raw.NomiSpeciali = []string{}
	}
	if raw.ParoleImportanti == nil {
		raw.ParoleImportanti = []string{}
	}

	artlistPhrases := make(map[string][]string)
	if len(raw.ArtlistPhrases) > 0 && string(raw.ArtlistPhrases) != "null" {
		if err := json.Unmarshal(raw.ArtlistPhrases, &artlistPhrases); err != nil {
			var list []struct {
				Frase    string   `json:"frase"`
				Phrase   string   `json:"phrase"`
				Keywords []string `json:"keyword"`
				Kwds     []string `json:"keywords"`
			}
			if err := json.Unmarshal(raw.ArtlistPhrases, &list); err == nil {
				for _, item := range list {
					f := item.Frase
					if f == "" {
						f = item.Phrase
					}
					k := item.Keywords
					if len(k) == 0 {
						k = item.Kwds
					}
					if f != "" {
						artlistPhrases[f] = k
					}
				}
			}
		}
	}

	entityMap := make(map[string]string)
	if len(raw.EntitaSenzaTesto) > 0 && string(raw.EntitaSenzaTesto) != "null" {
		if err := json.Unmarshal(raw.EntitaSenzaTesto, &entityMap); err != nil {
			var entityList []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			}
			if err := json.Unmarshal(raw.EntitaSenzaTesto, &entityList); err == nil {
				for _, item := range entityList {
					name := strings.TrimSpace(item.Name)
					if name == "" {
						continue
					}
					entityMap[name] = strings.TrimSpace(item.URL)
				}
			}
		}
	}

	return &types.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  raw.FrasiImportanti,
		EntitaSenzaTesto: entityMap,
		NomiSpeciali:     raw.NomiSpeciali,
		ParoleImportanti: raw.ParoleImportanti,
		ArtlistPhrases:   artlistPhrases,
	}, nil
}
