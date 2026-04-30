package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/ml/ollama/prompts"
	"velox/go-master/internal/ml/ollama/types"
)

// ExtractEntitiesFromSegment extracts entities from a single text segment using Ollama
func (c *Client) ExtractEntitiesFromSegment(ctx context.Context, req types.EntityExtractionRequest) (*types.EntityExtractionResult, error) {
	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 2
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
		entityCount = 2
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
			result = fallbackEntityExtractionResult(segment, i, entityCount)
		}
		if resultIsEmpty(result) {
			result = fallbackEntityExtractionResult(segment, i, entityCount)
		}
		result = capEntityExtractionResult(result, entityCount)

		segmentEntities := types.SegmentEntities{
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
			}
		}
	}

	return &types.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  raw.FrasiImportanti,
		EntitaSenzaTesto: entityMap,
		NomiSpeciali:     raw.NomiSpeciali,
		ParoleImportanti: raw.ParoleImportanti,
	}, nil
}

func resultIsEmpty(result *types.EntityExtractionResult) bool {
	if result == nil {
		return true
	}
	return len(result.FrasiImportanti) == 0 &&
		len(result.EntitaSenzaTesto) == 0 &&
		len(result.NomiSpeciali) == 0 &&
		len(result.ParoleImportanti) == 0
}

func capEntityExtractionResult(result *types.EntityExtractionResult, limit int) *types.EntityExtractionResult {
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

func fallbackEntityExtractionResult(segment string, segmentIndex, entityCount int) *types.EntityExtractionResult {
	segment = strings.TrimSpace(segment)
	if entityCount <= 0 {
		entityCount = 2
	}

	phrases := fallbackImportantPhrases(segment, entityCount)
	names := fallbackSpecialNames(segment, entityCount)
	words := fallbackImportantWords(segment, entityCount)
	entityMap := make(map[string]string, len(names))
	for _, name := range names {
		entityMap[name] = ""
	}
	return &types.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  phrases,
		EntitaSenzaTesto: entityMap,
		NomiSpeciali:     names,
		ParoleImportanti: words,
	}
}

func fallbackImportantPhrases(segment string, limit int) []string {
	if limit <= 0 {
		limit = 1
	}
	if segment == "" {
		return nil
	}
	parts := splitSentences(segment)
	if len(parts) == 0 {
		return []string{trimPhrase(segment, 120)}
	}

	out := make([]string, 0, limit)
	for _, part := range parts {
		part = trimPhrase(part, 120)
		if part == "" {
			continue
		}
		out = append(out, part)
		if len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, trimPhrase(segment, 120))
	}
	return uniqueLocalStrings(out)
}

func fallbackSpecialNames(segment string, limit int) []string {
	if limit <= 0 {
		limit = 1
	}
	if segment == "" {
		return nil
	}

	words := strings.Fields(segment)
	names := make([]string, 0, limit)
	for _, raw := range words {
		word := strings.Trim(raw, `"'“”‘’,.:;!?()[]{}<>`)
		if len([]rune(word)) < 3 {
			continue
		}
		runes := []rune(word)
		if len(runes) == 0 || !unicode.IsUpper(runes[0]) {
			continue
		}
		if matching.IsStopWord(strings.ToLower(word)) {
			continue
		}
		names = append(names, word)
		if len(names) >= limit {
			break
		}
	}
	if len(names) == 0 {
		return nil
	}
	return uniqueLocalStrings(names)
}

func fallbackImportantWords(segment string, limit int) []string {
	if limit <= 0 {
		limit = 1
	}
	if segment == "" {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]string, 0, limit)
	for _, raw := range strings.FieldsFunc(strings.ToLower(segment), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len(raw) < 4 || matching.IsStopWord(raw) {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
}

func trimPhrase(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxLen <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}

func uniqueLocalStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, s := range input {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}
