package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/core"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/prompts"
)

// ExtractEntitiesFromSegment extracts entities from a single text segment using Ollama.
func (c *Client) ExtractEntitiesFromSegment(ctx context.Context, req core.EntityExtractionRequest) (*core.EntityExtractionResult, error) {
	return c.ExtractEntitiesFromSegmentWithModel(ctx, req, "")
}

// ExtractEntitiesFromSegmentWithModel extracts entities using the specified model.
func (c *Client) ExtractEntitiesFromSegmentWithModel(ctx context.Context, req core.EntityExtractionRequest, model string) (*core.EntityExtractionResult, error) {
	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 2
	}

	prompt := prompts.BuildEntityExtractionPrompt(req.SegmentText, entityCount)

	var (
		response string
		err      error
	)
	if model != "" {
		response, err = c.GenerateWithOptions(ctx, model, prompt, nil)
	} else {
		response, err = c.Generate(ctx, prompt)
	}
	if err != nil {
		return nil, fmt.Errorf("entity extraction failed: %w", err)
	}

	result, err := parseEntityExtractionResult(response, req.SegmentIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse entity result: %w", err)
	}

	return result, nil
}

// ExtractEntitiesFromScript extracts entities from all segments.
func (c *Client) ExtractEntitiesFromScript(ctx context.Context, segments []string, entityCount int) (*core.FullEntityAnalysis, error) {
	return c.ExtractEntitiesFromScriptWithModel(ctx, segments, entityCount, "")
}

// ExtractEntitiesFromScriptWithModel extracts entities using the specified model.
func (c *Client) ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, entityCount int, model string) (*core.FullEntityAnalysis, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments provided")
	}
	if entityCount <= 0 {
		entityCount = 2
	}

	analysis := &core.FullEntityAnalysis{
		TotalSegments:         len(segments),
		SegmentEntities:       make([]core.SegmentEntities, 0, len(segments)),
		EntityCountPerSegment: entityCount,
	}

	for i, segment := range segments {
		req := core.EntityExtractionRequest{
			SegmentText:  segment,
			SegmentIndex: i,
			EntityCount:  entityCount,
		}

		result, err := c.ExtractEntitiesFromSegmentWithModel(ctx, req, model)
		if err != nil {
			result = fallbackEntityExtractionResult(segment, i, entityCount)
		}
		result = sanitizeEntityExtractionResult(segment, result, entityCount)
		if resultIsEmpty(result) {
			result = fallbackEntityExtractionResult(segment, i, entityCount)
			result = sanitizeEntityExtractionResult(segment, result, entityCount)
		}
		result = capEntityExtractionResult(result, entityCount)

		analysis.SegmentEntities = append(analysis.SegmentEntities, core.SegmentEntities{
			SegmentIndex:     i,
			SegmentText:      segment,
			FrasiImportanti:  result.FrasiImportanti,
			EntitaSenzaTesto: result.EntitaSenzaTesto,
			NomiSpeciali:     result.NomiSpeciali,
			ParoleImportanti: result.ParoleImportanti,
			ArtlistPhrases:   result.ArtlistPhrases,
		})

		analysis.TotalEntities += len(result.FrasiImportanti) +
			len(result.EntitaSenzaTesto) +
			len(result.NomiSpeciali) +
			len(result.ParoleImportanti) +
			len(result.ArtlistPhrases)
	}

	return analysis, nil
}

func parseEntityExtractionResult(response string, segmentIndex int) (*core.EntityExtractionResult, error) {
	jsonStr := strings.TrimSpace(response)

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
		ArtlistPhrases   []string        `json:"artlist_phrases"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	if raw.FrasiImportanti == nil {
		raw.FrasiImportanti = []string{}
	}
	if raw.NomiSpeciali == nil {
		raw.NomiSpeciali = []string{}
	}
	if raw.ParoleImportanti == nil {
		raw.ParoleImportanti = []string{}
	}
	if raw.ArtlistPhrases == nil {
		raw.ArtlistPhrases = []string{}
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

	return &core.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  raw.FrasiImportanti,
		EntitaSenzaTesto: entityMap,
		NomiSpeciali:     raw.NomiSpeciali,
		ParoleImportanti: raw.ParoleImportanti,
		ArtlistPhrases:   raw.ArtlistPhrases,
	}, nil
}

func resultIsEmpty(result *core.EntityExtractionResult) bool {
	if result == nil {
		return true
	}
	return len(result.FrasiImportanti) == 0 &&
		len(result.EntitaSenzaTesto) == 0 &&
		len(result.NomiSpeciali) == 0 &&
		len(result.ParoleImportanti) == 0 &&
		len(result.ArtlistPhrases) == 0
}

func capEntityExtractionResult(result *core.EntityExtractionResult, limit int) *core.EntityExtractionResult {
	if result == nil {
		return nil
	}
	if limit <= 0 {
		limit = 2
	}
	if len(result.FrasiImportanti) > limit {
		result.FrasiImportanti = result.FrasiImportanti[:limit]
	}
	if len(result.NomiSpeciali) > limit {
		result.NomiSpeciali = result.NomiSpeciali[:limit]
	}
	if len(result.ParoleImportanti) > limit {
		result.ParoleImportanti = result.ParoleImportanti[:limit]
	}
	// Artlist phrases have their own stricter cap (max 5) regardless of the general limit
	artlistCap := 5
	if len(result.ArtlistPhrases) > artlistCap {
		result.ArtlistPhrases = result.ArtlistPhrases[:artlistCap]
	}
	if len(result.EntitaSenzaTesto) > limit {
		capped := make(map[string]string, limit)
		i := 0
		for k, v := range result.EntitaSenzaTesto {
			capped[k] = v
			i++
			if i >= limit {
				break
			}
		}
		result.EntitaSenzaTesto = capped
	}
	return result
}
