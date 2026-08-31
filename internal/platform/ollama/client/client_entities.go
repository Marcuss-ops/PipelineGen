package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"
)

// entityExtractionNumPredict is deliberately scoped to this operation. Entity
// extraction has a small, structured output contract and must not inherit the
// much larger generation budget used by script generation.
const entityExtractionNumPredict = 256

const entityExtractionBatchSize = 5

// entityExtractionJSONSchema is sent as Ollama's top-level structured-output
// format for the single-segment path. The parser still accepts the historical
// labeled-text contract for compatibility, while live models are constrained
// to return one valid extraction object instead of prose or markdown.
var entityExtractionJSONSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"frasi_importanti":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"entity_senza_testo": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		"nomi_speciali":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"parole_importanti":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"artlist_phrases":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"noun_chunks":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required":             []string{"frasi_importanti", "entity_senza_testo", "nomi_speciali", "parole_importanti", "artlist_phrases", "noun_chunks"},
	"additionalProperties": false,
}

// ExtractEntitiesFromSegment extracts entities from a single text segment using Ollama.
func (c *Client) ExtractEntitiesFromSegment(ctx context.Context, req detail.EntityExtractionRequest) (*detail.EntityExtractionResult, error) {
	return c.ExtractEntitiesFromSegmentWithModel(ctx, req, "")
}

// ExtractEntitiesFromSegmentWithModel extracts entities using the specified model.
func (c *Client) ExtractEntitiesFromSegmentWithModel(ctx context.Context, req detail.EntityExtractionRequest, model string) (*detail.EntityExtractionResult, error) {
	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 2
	}

	prompt := prompts.BuildEntityExtractionPromptForLanguage(req.SegmentText, entityCount, req.Language)

	var (
		response string
		err      error
	)
	if model != "" {
		response, err = c.GenerateWithOptions(ctx, model, prompt, map[string]any{
			"num_predict": entityExtractionNumPredict,
			"temperature": 0,
			"think":       false,
			"format":      entityExtractionJSONSchema,
		})
	} else {
		response, err = c.GenerateWithOptions(ctx, c.model, prompt, map[string]any{
			"num_predict": entityExtractionNumPredict,
			"temperature": 0,
			"think":       false,
			"format":      entityExtractionJSONSchema,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("entity extraction failed: %w", err)
	}

	result, err := parseEntityExtractionResult(response, req.SegmentIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse entity result: %w", err)
	}
	// The model response is an untrusted boundary. Apply the same grounding
	// contract used by the batch path before exposing any structured fields.
	result = sanitizeEntityExtractionResult(req.SegmentText, result, entityCount, req.Language)

	return result, nil
}

// ExtractEntitiesFromBatchWithModel performs one bounded generation for up to
// five scenes and returns one typed result per input scene.
func (c *Client) ExtractEntitiesFromBatchWithModel(ctx context.Context, segments []string, entityCount int, model string, language string) ([]*detail.EntityExtractionResult, error) {
	if len(segments) == 0 || len(segments) > entityExtractionBatchSize {
		return nil, fmt.Errorf("entity batch size must be between 1 and %d", entityExtractionBatchSize)
	}
	if entityCount <= 0 {
		entityCount = 2
	}
	prompt := prompts.BuildEntityExtractionBatchPromptForLanguage(segments, entityCount, language)
	response, err := c.GenerateWithOptions(ctx, model, prompt, map[string]any{
		"num_predict": entityExtractionNumPredict * len(segments),
		"temperature": 0,
	})
	if err != nil {
		return nil, fmt.Errorf("entity batch extraction failed: %w", err)
	}
	results, parseErr := parseEntityExtractionBatchResult(response, len(segments))
	if parseErr == nil {
		for i, result := range results {
			// Keep batched extraction subject to the same evidence and
			// placeholder filtering as the single-segment path.
			result = sanitizeEntityExtractionResult(segments[i], result, entityCount, language)
			if resultIsEmpty(result) {
				result = fallbackEntityExtractionResult(segments[i], i, entityCount, language)
				result = sanitizeEntityExtractionResult(segments[i], result, entityCount, language)
			}
			results[i] = capEntityExtractionResult(result, entityCount)
		}
		return results, nil
	}
	// Small local models can ignore the multi-scene envelope. Retry only this
	// bounded batch as individually addressed requests, with two concurrent
	// calls. This preserves correctness without reopening the original 34-call
	// unbounded fan-out.
	return c.extractEntityBatchIndividually(ctx, segments, entityCount, model, language)
}

func (c *Client) extractEntityBatchIndividually(ctx context.Context, segments []string, entityCount int, model string, language string) ([]*detail.EntityExtractionResult, error) {
	results := make([]*detail.EntityExtractionResult, len(segments))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				result, err := c.ExtractEntitiesFromSegmentWithModel(ctx, detail.EntityExtractionRequest{
					SegmentText: segments[index], SegmentIndex: index, EntityCount: entityCount,
				}, model)
				if err != nil {
					if c.entityExtractionFallbackMode != EntityExtractionFallbackDisabled {
						result = fallbackEntityExtractionResult(segments[index], index, entityCount, language)
						result = sanitizeEntityExtractionResult(segments[index], result, entityCount, language)
						results[index] = capEntityExtractionResult(result, entityCount)
						continue
					}
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					continue
				}
				result = sanitizeEntityExtractionResult(segments[index], result, entityCount, language)
				if resultIsEmpty(result) && c.entityExtractionFallbackMode != EntityExtractionFallbackDisabled {
					result = fallbackEntityExtractionResult(segments[index], index, entityCount, language)
					result = sanitizeEntityExtractionResult(segments[index], result, entityCount, language)
				}
				results[index] = capEntityExtractionResult(result, entityCount)
			}
		}()
	}
	for index := range segments {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, fmt.Errorf("entity batch fallback failed: %w", firstErr)
	}
	return results, nil
}

func parseEntityExtractionBatchResult(response string, expected int) ([]*detail.EntityExtractionResult, error) {
	cleaned := stripMarkdownFences(strings.TrimSpace(response))
	const marker = "### SEGMENT_INDEX:"
	parts := strings.Split(cleaned, marker)
	results := make([]*detail.EntityExtractionResult, expected)
	seen := make([]bool, expected)
	for _, part := range parts[1:] {
		lines := strings.SplitN(part, "\n", 2)
		if len(lines) != 2 {
			continue
		}
		indexText := strings.TrimSpace(lines[0])
		index, err := strconv.Atoi(indexText)
		if err != nil || index < 0 || index >= expected || seen[index] {
			// Small models sometimes copy the prompt placeholder "N". The
			// response order is still deterministic, so bind such a block to
			// the next unassigned input rather than guessing from its content.
			index = -1
			for candidate := range seen {
				if !seen[candidate] {
					index = candidate
					break
				}
			}
			if index < 0 {
				return nil, fmt.Errorf("entity batch response has too many segment blocks")
			}
		}
		block := strings.SplitN(lines[1], "### END_SEGMENT", 2)[0]
		result, err := parseEntityExtractionResult(strings.TrimSpace(block), index)
		if err != nil {
			return nil, fmt.Errorf("failed to parse entity batch segment %d: %w", index, err)
		}
		results[index] = result
		seen[index] = true
	}
	for index, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("entity batch response missing segment %d", index)
		}
	}
	return results, nil
}

// ExtractEntitiesFromScript extracts entities from all segments.
func (c *Client) ExtractEntitiesFromScript(ctx context.Context, segments []string, entityCount int) (*detail.FullEntityAnalysis, error) {
	return c.ExtractEntitiesFromScriptWithModel(ctx, segments, entityCount, "")
}

// ExtractEntitiesFromScriptWithModel extracts entities using the specified model.
func (c *Client) ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, entityCount int, model string) (*detail.FullEntityAnalysis, error) {
	return c.ExtractEntitiesFromScriptWithModelAndLanguage(ctx, segments, entityCount, model, "")
}

// ExtractEntitiesFromScriptWithModelAndLanguage is the language-aware variant
// of ExtractEntitiesFromScriptWithModel. It forwards the request language into
// sanitization so stop-word and function-word filtering uses the correct
// per-language lexicon instead of the cross-linguistic fallback profile.
func (c *Client) ExtractEntitiesFromScriptWithModelAndLanguage(ctx context.Context, segments []string, entityCount int, model string, language string) (*detail.FullEntityAnalysis, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments provided")
	}
	if entityCount <= 0 {
		entityCount = 2
	}

	analysis := &detail.FullEntityAnalysis{
		TotalSegments:         len(segments),
		SegmentEntities:       make([]detail.SegmentEntities, 0, len(segments)),
		EntityCountPerSegment: entityCount,
	}

	for i, segment := range segments {
		req := detail.EntityExtractionRequest{
			SegmentText:  segment,
			SegmentIndex: i,
			EntityCount:  entityCount,
			Language:     language,
		}

		result, err := c.ExtractEntitiesFromSegmentWithModel(ctx, req, model)
		if err != nil {
			if c.entityExtractionFallbackMode == EntityExtractionFallbackDisabled {
				return nil, fmt.Errorf("entity extraction failed for segment %d (fallback disabled): %w", i, err)
			}
			result = fallbackEntityExtractionResult(segment, i, entityCount, language)
		}
		result = sanitizeEntityExtractionResult(segment, result, entityCount, language)
		if resultIsEmpty(result) {
			if c.entityExtractionFallbackMode == EntityExtractionFallbackDisabled {
				return nil, fmt.Errorf("LLM returned empty entity extraction result for segment %d (fallback disabled)", i)
			}
			result = fallbackEntityExtractionResult(segment, i, entityCount, language)
			result = sanitizeEntityExtractionResult(segment, result, entityCount, language)
		}
		result = capEntityExtractionResult(result, entityCount)

		analysis.SegmentEntities = append(analysis.SegmentEntities, detail.SegmentEntities{
			SegmentIndex:     i,
			SegmentText:      segment,
			FrasiImportanti:  result.FrasiImportanti,
			EntitaSenzaTesto: result.EntitaSenzaTesto,
			NomiSpeciali:     result.NomiSpeciali,
			ParoleImportanti: result.ParoleImportanti,
			ArtlistPhrases:   result.ArtlistPhrases,
			NounChunks:       result.NounChunks,
			Source:           result.Source,
		})

		analysis.TotalEntities += len(result.FrasiImportanti) +
			len(result.EntitaSenzaTesto) +
			len(result.NomiSpeciali) +
			len(result.ParoleImportanti) +
			len(result.ArtlistPhrases)
	}

	return analysis, nil
}

func parseEntityExtractionResult(response string, segmentIndex int) (*detail.EntityExtractionResult, error) {
	cleaned := stripMarkdownFences(strings.TrimSpace(response))

	// Primary path: plain-text labeled sections (LLM-PLAIN-TEXT-CONTRACT P1).
	if !looksLikeEntityJSON(cleaned) {
		result, err := parsePlainTextEntityResult(cleaned, segmentIndex)
		if err == nil && !resultIsEmpty(result) {
			return result, nil
		}
		// Fall through to legacy JSON parser if plain-text yields nothing.
	}

	return parseLegacyJSONEntityResult(cleaned, segmentIndex)
}

// stripMarkdownFences removes ``` fence lines from the response.
func stripMarkdownFences(s string) string {
	if !strings.Contains(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	var clean []string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			continue
		}
		clean = append(clean, line)
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}

// looksLikeEntityJSON returns true when the response appears to be JSON
// (starts with { or contains a JSON object). Plain-text responses with
// Markdown headers (##) are not JSON-like.
func looksLikeEntityJSON(s string) bool {
	s = strings.TrimSpace(s)
	// If it starts with a Markdown header or bullet, it's plain text.
	if strings.HasPrefix(s, "#") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "*") {
		return false
	}
	return strings.HasPrefix(s, "{")
}

// sectionHeaders maps lowercase header strings to canonical section keys.
var sectionHeaders = map[string]string{
	"frasi_importanti":   "frasi_importanti",
	"entity_senza_testo": "entity_senza_testo",
	"nomi_speciali":      "nomi_speciali",
	"parole_importanti":  "parole_importanti",
	"artlist_phrases":    "artlist_phrases",
	"noun_chunks":        "noun_chunks",
}

// parsePlainTextEntityResult parses the LLM-PLAIN-TEXT-CONTRACT P1 format:
// labeled sections with "## SectionName" headers and "- item" bullet points.
func parsePlainTextEntityResult(response string, segmentIndex int) (*detail.EntityExtractionResult, error) {
	result := &detail.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		EntitaSenzaTesto: make(map[string]string),
	}

	var currentSection string
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Detect "## section_name" header.
		if strings.HasPrefix(line, "##") || strings.HasPrefix(line, "#") {
			currentSection = detectEntitySection(line)
			continue
		}

		// Detect bare section name on its own line (model may skip ##).
		// Normalize spaces→underscores same as detectEntitySection.
		bare := normalizeEntitySectionName(line)
		if sec, ok := sectionHeaders[bare]; ok {
			currentSection = sec
			continue
		}

		// Skip non-bullet lines when not in a known section.
		if currentSection == "" {
			continue
		}

		// Extract bullet item.
		val := stripEntityBullet(line)
		if val == "" {
			continue
		}

		switch currentSection {
		case "frasi_importanti":
			result.FrasiImportanti = append(result.FrasiImportanti, val)
		case "nomi_speciali":
			result.NomiSpeciali = append(result.NomiSpeciali, val)
		case "parole_importanti":
			result.ParoleImportanti = append(result.ParoleImportanti, val)
		case "artlist_phrases":
			result.ArtlistPhrases = append(result.ArtlistPhrases, val)
		case "noun_chunks":
			result.NounChunks = append(result.NounChunks, val)
		case "entity_senza_testo":
			if key, value, ok := parseEntityKeyValue(val); ok {
				result.EntitaSenzaTesto[key] = value
			}
		}
	}

	return result, nil
}

// normalizeEntitySectionName strips leading #, trailing :, lowercases,
// and replaces spaces with underscores for fuzzy section-name matching.
// Called by both detectEntitySection (# headers) and the bare-name path.
func normalizeEntitySectionName(line string) string {
	name := strings.TrimLeft(line, "# ")
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, ":")
	return strings.ReplaceAll(strings.ToLower(name), " ", "_")
}

// detectEntitySection extracts the section name from a header line like
// "## frasi_importanti" or "# entity_senza_testo".
func detectEntitySection(line string) string {
	if sec, ok := sectionHeaders[normalizeEntitySectionName(line)]; ok {
		return sec
	}
	return ""
}

// stripEntityBullet removes bullet markers ("- ", "* ", "• ") from a line.
func stripEntityBullet(line string) string {
	for _, prefix := range []string{"- ", "* ", "• "} {
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// parseEntityKeyValue parses a "Key: Value" pair from a bullet item.
func parseEntityKeyValue(item string) (key, value string, ok bool) {
	idx := strings.Index(item, ":")
	if idx < 1 {
		return "", "", false
	}
	key = strings.TrimSpace(item[:idx])
	value = strings.TrimSpace(item[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

// parseLegacyJSONEntityResult parses the pre-P1 JSON format.
// Kept as fallback for cache hits and models that still produce JSON.
func parseLegacyJSONEntityResult(jsonStr string, segmentIndex int) (*detail.EntityExtractionResult, error) {
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
		NounChunks       []string        `json:"noun_chunks"`
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

	return &detail.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  raw.FrasiImportanti,
		EntitaSenzaTesto: entityMap,
		NomiSpeciali:     raw.NomiSpeciali,
		ParoleImportanti: raw.ParoleImportanti,
		ArtlistPhrases:   raw.ArtlistPhrases,
		NounChunks:       raw.NounChunks,
	}, nil
}

func resultIsEmpty(result *detail.EntityExtractionResult) bool {
	if result == nil {
		return true
	}
	return len(result.FrasiImportanti) == 0 &&
		len(result.EntitaSenzaTesto) == 0 &&
		len(result.NomiSpeciali) == 0 &&
		len(result.ParoleImportanti) == 0 &&
		len(result.ArtlistPhrases) == 0 &&
		len(result.NounChunks) == 0
}

func capEntityExtractionResult(result *detail.EntityExtractionResult, limit int) *detail.EntityExtractionResult {
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
