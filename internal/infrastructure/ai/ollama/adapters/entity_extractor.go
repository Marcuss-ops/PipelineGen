// Package adapters — entity_extractor.go bridges the application-layer
// EntityExtractor port (internal/application/scripts/adapters) with the
// real ollama.Client entity extraction backend.
//
// PR-ENTITY-EXTRACTOR-WIRING (July 2026): the V2 postprocessor pipeline
// had the entities processor wired through the fail-closed
// unavailableEntityExtractionAdapter which always returned
// ErrEntityExtractorUnavailable. This adapter replaces it so the
// ollama.Client.ExtractEntitiesFromScriptWithModel backend is invoked
// at runtime, populating the canonical EntityResult with Persons,
// Places, Concepts, and preserving the raw JSON for the ArtlistPhrases
// → SearchArtlistClips consumption path.
//
// godlike/06 SSOT (one canonical owner per fact): this adapter is the
// SOLE bridge between the EntityExtractor port and the real Ollama
// backend. No other package may define a parallel adapter.
//
// godlike/07 NO-FAKE-AVAILABILITY: the adapter calls the real Ollama
// backend — it does not return empty/synthetic results. A nil client
// at construction time is rejected via the adapter returning
// ErrEntityExtractorUnavailable at runtime (mirrors the unavailable
// adapter contract so callers can probe identically).
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

// OllamaEntityExtractorAdapter implements scriptadapters.EntityExtractor
// by delegating to the real ollama.Client entity extraction backend.
type OllamaEntityExtractorAdapter struct {
	client *client.Client
}

// NewOllamaEntityExtractorAdapter constructs the adapter. client may
// be nil — in that case, ExtractEntities returns
// scriptadapters.ErrEntityExtractorUnavailable (fail-closed per
// godlike/07) so callers probe identically to the unavailable adapter.
func NewOllamaEntityExtractorAdapter(c *client.Client) scriptadapters.EntityExtractor {
	return &OllamaEntityExtractorAdapter{client: c}
}

// ExtractEntities implements scriptadapters.EntityExtractor.
//
// Flow:
//  1. Split Text into segments (by double newline).
//  2. Call ollama.Client.ExtractEntitiesFromScriptWithModel.
//  3. Convert asset.FullEntityAnalysis → script.EntityResult:
//     - NomiSpeciali → Persons (heterogeneous named entities)
//     - ParoleImportanti → Concepts (key concepts)
//     - Places stays nil (Italian schema has no dedicated place bucket)
//     - Raw carries the JSON-serialised FullEntityAnalysis for
//     the downstream ArtlistPhrases → SearchArtlistClips path
func (a *OllamaEntityExtractorAdapter) ExtractEntities(ctx context.Context, req script.EntityExtractionRequest) (*script.EntityResult, error) {
	if a.client == nil {
		return nil, scriptadapters.ErrEntityExtractorUnavailable
	}

	// Step 1: split text into segments (paragraphs).
	segments := splitIntoSegments(req.Text)
	if len(segments) == 0 {
		return &script.EntityResult{}, nil
	}

	// Step 2: call the real Ollama backend.
	entityCount := 2 // default per-segment entity count
	analysis, err := a.client.ExtractEntitiesFromScriptWithModel(ctx, segments, entityCount, req.Model)
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		return &script.EntityResult{}, nil
	}

	// Step 3: convert FullEntityAnalysis → EntityResult.
	result := &script.EntityResult{
		Persons:          []script.Entity{},
		Places:           nil,
		Concepts:         []script.Entity{},
		ArtlistPhrases:   []string{},
		ImportantPhrases: []string{},
		ImportantWords:   []string{},
	}

	for _, seg := range analysis.SegmentEntities {
		for _, name := range seg.NomiSpeciali {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			result.Persons = append(result.Persons, script.Entity{Value: name})
		}
		for _, concept := range seg.ParoleImportanti {
			concept = strings.TrimSpace(concept)
			if concept == "" {
				continue
			}
			result.Concepts = append(result.Concepts, script.Entity{Value: concept})
			result.ImportantWords = append(result.ImportantWords, concept)
		}
		for _, phrase := range seg.FrasiImportanti {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			result.ImportantPhrases = append(result.ImportantPhrases, phrase)
		}
		for _, phrase := range seg.ArtlistPhrases {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			result.ArtlistPhrases = append(result.ArtlistPhrases, phrase)
		}
	}

	// Deduplicate string slices while preserving order.
	result.ArtlistPhrases = sliceutil.UniqueStrings(result.ArtlistPhrases)
	result.ImportantPhrases = sliceutil.UniqueStrings(result.ImportantPhrases)
	result.ImportantWords = sliceutil.UniqueStrings(result.ImportantWords)

	// Step 4: preserve the full analysis as JSON in Raw.
	// This allows downstream consumers (InsightBuilder,
	// SearchArtlistClips) to extract ArtlistPhrases,
	// FrasiImportanti, EntitaSenzaTesto, etc.
	if rawBytes, marshalErr := json.Marshal(analysis); marshalErr == nil {
		result.Raw = string(rawBytes)
	}

	return result, nil
}

// splitIntoSegments splits text into segments by double newline,
// trimming whitespace and dropping empty segments.
func splitIntoSegments(text string) []string {
	raw := strings.Split(strings.TrimSpace(text), "\n\n")
	segments := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			segments = append(segments, s)
		}
	}
	return segments
}

// Compile-time pin: OllamaEntityExtractorAdapter satisfies
// scriptadapters.EntityExtractor.
var _ scriptadapters.EntityExtractor = (*OllamaEntityExtractorAdapter)(nil)
