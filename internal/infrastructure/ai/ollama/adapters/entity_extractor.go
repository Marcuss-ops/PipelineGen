// Package adapters bridges script entity extraction to the Ollama backend.
package adapters

import (
	"context"
	"encoding/json"
	"strings"

	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// OllamaEntityExtractorAdapter is the sole bridge between the script port and
// the real Ollama entity-extraction backend.
type OllamaEntityExtractorAdapter struct {
	client *client.Client
}

func NewOllamaEntityExtractorAdapter(c *client.Client) scriptadapters.EntityExtractor {
	return &OllamaEntityExtractorAdapter{client: c}
}

func (a *OllamaEntityExtractorAdapter) ExtractEntities(ctx context.Context, req script.EntityExtractionRequest) (*script.EntityResult, error) {
	if a.client == nil {
		return nil, scriptadapters.ErrEntityExtractorUnavailable
	}

	segments := splitIntoSegments(req.Text)
	if len(segments) == 0 {
		return &script.EntityResult{}, nil
	}

	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 5
	}
	analysis, err := a.client.ExtractEntitiesFromScriptWithModel(ctx, segments, entityCount, req.Model)
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		return &script.EntityResult{}, nil
	}

	result := &script.EntityResult{
		Persons:          []script.Entity{},
		Places:           []script.Entity{},
		Concepts:         []script.Entity{},
		ArtlistPhrases:   []string{},
		ImportantPhrases: []string{},
		ImportantWords:   []string{},
	}
	seenEntities := make(map[string]struct{})

	for _, seg := range analysis.SegmentEntities {
		for _, rawName := range seg.NomiSpeciali {
			kind, value := parseTypedEntity(rawName)
			if value == "" {
				continue
			}
			switch kind {
			case "PERSON":
				appendUniqueEntity(&result.Persons, seenEntities, kind, value, 0.98)
			case "PLACE", "LOCATION", "CITY", "COUNTRY":
				appendUniqueEntity(&result.Places, seenEntities, "PLACE", value, 0.98)
			default:
				appendUniqueEntity(&result.Concepts, seenEntities, kind, value, 0.95)
			}
		}

		// entity_senza_testo contains identifiable visual subjects mapped to
		// precise visual descriptions. Preserve the subjects as concepts and
		// promote the descriptions to first-class Artlist/image search phrases.
		for subject, description := range seg.EntitaSenzaTesto {
			subject = strings.TrimSpace(subject)
			description = strings.TrimSpace(description)
			if subject != "" {
				appendUniqueEntity(&result.Concepts, seenEntities, "VISUAL_SUBJECT", subject, 0.92)
			}
			if description != "" {
				result.ArtlistPhrases = append(result.ArtlistPhrases, description)
			}
		}

		for _, concept := range seg.ParoleImportanti {
			concept = strings.TrimSpace(concept)
			if concept == "" {
				continue
			}
			appendUniqueEntity(&result.Concepts, seenEntities, "KEYWORD", concept, 0.90)
			result.ImportantWords = append(result.ImportantWords, concept)
		}
		for _, phrase := range seg.FrasiImportanti {
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				result.ImportantPhrases = append(result.ImportantPhrases, phrase)
			}
		}
		for _, phrase := range seg.ArtlistPhrases {
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				result.ArtlistPhrases = append(result.ArtlistPhrases, phrase)
			}
		}
	}

	result.ArtlistPhrases = sliceutil.UniqueStrings(result.ArtlistPhrases)
	result.ImportantPhrases = sliceutil.UniqueStrings(result.ImportantPhrases)
	result.ImportantWords = sliceutil.UniqueStrings(result.ImportantWords)

	if rawBytes, marshalErr := json.Marshal(analysis); marshalErr == nil {
		result.Raw = string(rawBytes)
	}
	return result, nil
}

// parseTypedEntity accepts the new "TYPE: value" contract while remaining
// backward compatible with older untyped cache/model responses.
func parseTypedEntity(raw string) (kind, value string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "[") {
		if end := strings.Index(raw, "]"); end > 1 {
			kind = normalizeEntityKind(raw[1:end])
			value = strings.TrimSpace(raw[end+1:])
			if kind != "" && value != "" {
				return kind, value
			}
		}
	}
	for _, separator := range []string{":", "|"} {
		if before, after, ok := strings.Cut(raw, separator); ok {
			candidateKind := normalizeEntityKind(before)
			candidateValue := strings.TrimSpace(after)
			if candidateKind != "" && candidateValue != "" {
				return candidateKind, candidateValue
			}
		}
	}
	// Untyped legacy names stay searchable but are not falsely labelled as
	// people. The new prompt emits explicit types for high-confidence routing.
	return "CONCEPT", raw
}

func normalizeEntityKind(raw string) string {
	kind := strings.ToUpper(strings.TrimSpace(raw))
	switch kind {
	case "PERSON", "PLACE", "LOCATION", "CITY", "COUNTRY", "ORGANIZATION", "ORG", "EVENT", "WORK", "PRODUCT", "OTHER", "CONCEPT":
		if kind == "ORG" {
			return "ORGANIZATION"
		}
		return kind
	default:
		return ""
	}
}

func appendUniqueEntity(dst *[]script.Entity, seen map[string]struct{}, kind, value string, score float32) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	key := strings.ToUpper(strings.TrimSpace(kind)) + "\x00" + strings.ToLower(value)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*dst = append(*dst, script.Entity{Value: value, Score: score})
}

func splitIntoSegments(text string) []string {
	raw := strings.Split(strings.TrimSpace(text), "\n\n")
	segments := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			segments = append(segments, s)
		}
	}
	return segments
}

var _ scriptadapters.EntityExtractor = (*OllamaEntityExtractorAdapter)(nil)
