package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractEntitiesFromSegment extracts entities from a single text segment using Ollama
func (c *Client) ExtractEntitiesFromSegment(ctx context.Context, req EntityExtractionRequest) (*EntityExtractionResult, error) {
	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 12
	}

	// Build the entity extraction prompt
	prompt := buildEntityExtractionPrompt(req.SegmentText, entityCount)

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
func (c *Client) ExtractEntitiesFromScript(ctx context.Context, segments []string, entityCount int) (*FullEntityAnalysis, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments provided")
	}

	if entityCount <= 0 {
		entityCount = 12
	}

	analysis := &FullEntityAnalysis{
		TotalSegments:         len(segments),
		SegmentEntities:       make([]SegmentEntities, 0, len(segments)),
		EntityCountPerSegment: entityCount,
	}

	// Extract entities for each segment
	for i, segment := range segments {
		req := EntityExtractionRequest{
			SegmentText:  segment,
			SegmentIndex: i,
			EntityCount:  entityCount,
		}

		result, err := c.ExtractEntitiesFromSegment(ctx, req)
		if err != nil {
			// Continue with empty entities for this segment
			result = &EntityExtractionResult{
				SegmentIndex:     i,
				FrasiImportanti:  []string{},
				EntitaSenzaTesto: make(map[string]string),
				NomiSpeciali:     []string{},
				ParoleImportanti: []string{},
			}
		}

		segmentEntities := SegmentEntities{
			SegmentIndex:     i,
			SegmentText:      segment,
			FrasiImportanti:  result.FrasiImportanti,
			EntitaSenzaTesto: result.EntitaSenzaTesto,
			NomiSpeciali:     result.NomiSpeciali,
			ParoleImportanti: result.ParoleImportanti,
		}

		analysis.SegmentEntities = append(analysis.SegmentEntities, segmentEntities)

		// Count total entities
		analysis.TotalEntities += len(result.FrasiImportanti) +
			len(result.EntitaSenzaTesto) +
			len(result.NomiSpeciali) +
			len(result.ParoleImportanti)
	}

	return analysis, nil
}

// parseEntityExtractionResult parses the JSON response from Ollama
func parseEntityExtractionResult(response string, segmentIndex int) (*EntityExtractionResult, error) {
	jsonStr := response

	// Remove markdown code blocks if present
	if len(jsonStr) > 7 && jsonStr[:7] == "```json" {
		end := len(jsonStr) - 3
		if end > 7 {
			jsonStr = jsonStr[7:end]
		}
	} else if len(jsonStr) > 3 && jsonStr[:3] == "```" {
		end := len(jsonStr) - 3
		if end > 3 {
			jsonStr = jsonStr[3:end]
		}
	}

	var raw struct {
		FrasiImportanti  []string        `json:"frasi_importanti"`
		EntitaSenzaTesto json.RawMessage   `json:"entity_senza_testo"`
		NomiSpeciali     []string        `json:"nomi_speciali"`
		ParoleImportanti []string        `json:"parole_importanti"`
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
			} else {
				var stringList []string
				if err := json.Unmarshal(raw.EntitaSenzaTesto, &stringList); err == nil {
					for _, name := range stringList {
						name = strings.TrimSpace(name)
						if name == "" {
							continue
						}
						entityMap[name] = ""
					}
				}
			}
		}
	}

	return &EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  raw.FrasiImportanti,
		EntitaSenzaTesto: entityMap,
		NomiSpeciali:     raw.NomiSpeciali,
		ParoleImportanti: raw.ParoleImportanti,
	}, nil
}
