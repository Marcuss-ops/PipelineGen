package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

// OllamaImportantPhraseExtractor is the composition-root adapter for the
// model-owned important-phrase surface. It requests the same structured NLP
// response used by the entity extractor and exposes only frasi_importanti;
// the runner performs grounding validation but does not create phrases.
type OllamaImportantPhraseExtractor struct {
	client *client.Client
}

func NewOllamaImportantPhraseExtractor(c *client.Client) scriptgen.ImportantPhraseExtractor {
	return &OllamaImportantPhraseExtractor{client: c}
}

func (a *OllamaImportantPhraseExtractor) ExtractImportantPhrases(ctx context.Context, sourceText string, limit int, language, model string) ([]string, error) {
	result, err := a.ExtractSceneNLP(ctx, sourceText, limit, language, model)
	return result.ImportantPhrases, err
}

// ExtractSceneNLP returns every grounded field from the structured model
// response. The caller must still project each candidate against sourceText;
// the Ollama client also sanitizes candidates before returning them.
func (a *OllamaImportantPhraseExtractor) ExtractSceneNLP(ctx context.Context, sourceText string, limit int, language, model string) (scriptgen.SceneNLPExtraction, error) {
	if a == nil || a.client == nil {
		return scriptgen.SceneNLPExtraction{}, nil
	}
	result, err := a.client.ExtractEntitiesFromSegmentWithModel(ctx, detail.EntityExtractionRequest{
		SegmentText: sourceText,
		EntityCount: limit,
		Language:    language,
	}, model)
	if err != nil || result == nil {
		return scriptgen.SceneNLPExtraction{}, err
	}
	return sceneNLPExtraction(result), nil
}

// ExtractImportantPhrasesBatch satisfies scriptgen.BatchImportantPhraseExtractor
// by extracting the phrases of up to client.EntityExtractionBatchLimit segments
// per model call.
//
// The underlying client already owns the correctness contract for batched
// extraction: it addresses every segment explicitly (### SEGMENT_INDEX), binds
// each returned block to its input, applies the same grounding/sanitization as
// the single-segment path, and retries a malformed batch as bounded individual
// requests. This method only chunks the input by that bound and projects the
// structured results onto frasi_importanti, so no caller can exceed the batch
// contract.
//
// An error means "the batched request could not be answered"; callers fall back
// to the per-scene path. It is never interpreted as "this language has no
// phrases".
func (a *OllamaImportantPhraseExtractor) ExtractImportantPhrasesBatch(ctx context.Context, sourceTexts []string, limit int, language, model string) ([][]string, error) {
	results, err := a.ExtractSceneNLPBatch(ctx, sourceTexts, limit, language, model)
	if err != nil {
		return nil, err
	}
	if results == nil {
		return nil, nil
	}
	phrases := make([][]string, len(results))
	for i := range results {
		phrases[i] = results[i].ImportantPhrases
	}
	return phrases, nil
}

// ExtractSceneNLPBatch returns full structured NLP per input scene, aligned
// positionally and chunked by the Ollama client's canonical batch limit.
func (a *OllamaImportantPhraseExtractor) ExtractSceneNLPBatch(ctx context.Context, sourceTexts []string, limit int, language, model string) ([]scriptgen.SceneNLPExtraction, error) {
	if a == nil || a.client == nil {
		return nil, nil
	}
	if len(sourceTexts) == 0 {
		return nil, nil
	}
	extractions := make([]scriptgen.SceneNLPExtraction, len(sourceTexts))
	for start := 0; start < len(sourceTexts); start += client.EntityExtractionBatchLimit {
		end := min(start+client.EntityExtractionBatchLimit, len(sourceTexts))
		chunk := sourceTexts[start:end]
		results, err := a.client.ExtractEntitiesFromBatchWithModel(ctx, chunk, limit, model, language)
		if err != nil {
			return nil, fmt.Errorf("scene NLP batch [%d:%d]: %w", start, end, err)
		}
		if len(results) != len(chunk) {
			return nil, fmt.Errorf("scene NLP batch [%d:%d]: got %d results for %d segments", start, end, len(results), len(chunk))
		}
		for i, result := range results {
			if result == nil {
				continue
			}
			extractions[start+i] = sceneNLPExtraction(result)
		}
	}
	return extractions, nil
}

func sceneNLPExtraction(result *detail.EntityExtractionResult) scriptgen.SceneNLPExtraction {
	if result == nil {
		return scriptgen.SceneNLPExtraction{}
	}
	extraction := scriptgen.SceneNLPExtraction{
		ImportantPhrases: append([]string(nil), result.FrasiImportanti...),
		ImportantWords:   append([]string(nil), result.ParoleImportanti...),
	}
	for _, candidate := range result.NomiSpeciali {
		candidate = strings.TrimSpace(candidate)
		if kind, value, ok := typedNamedEntity(candidate); ok {
			extraction.SpecialNames = append(extraction.SpecialNames, value)
			extraction.Entities = append(extraction.Entities, scriptgen.VisualEntity{Text: value, Type: kind, Score: 0.95})
			continue
		}
		if candidate != "" {
			extraction.SpecialNames = append(extraction.SpecialNames, candidate)
		}
	}
	return extraction
}

func typedNamedEntity(candidate string) (scriptpkg.EntityType, string, bool) {
	label, value, found := strings.Cut(candidate, ":")
	if !found {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	switch strings.ToUpper(strings.TrimSpace(label)) {
	case "PERSON":
		return scriptpkg.EntityTypePerson, value, value != ""
	case "PLACE", "LOCATION":
		return scriptpkg.EntityTypeLocation, value, value != ""
	case "ORGANIZATION", "ORG":
		return scriptpkg.EntityTypeOrganization, value, value != ""
	case "EVENT":
		return scriptpkg.EntityTypeEvent, value, value != ""
	case "WORK":
		return scriptpkg.EntityTypeWork, value, value != ""
	case "PRODUCT":
		return scriptpkg.EntityTypeProduct, value, value != ""
	case "OTHER":
		return scriptpkg.EntityTypeVisualConcept, value, value != ""
	default:
		return "", "", false
	}
}

var _ scriptgen.ImportantPhraseExtractor = (*OllamaImportantPhraseExtractor)(nil)
var _ scriptgen.BatchImportantPhraseExtractor = (*OllamaImportantPhraseExtractor)(nil)
var _ scriptgen.SceneNLPExtractor = (*OllamaImportantPhraseExtractor)(nil)
var _ scriptgen.BatchSceneNLPExtractor = (*OllamaImportantPhraseExtractor)(nil)
