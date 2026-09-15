package adapters

import (
	"context"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
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
	if a == nil || a.client == nil {
		return nil, nil
	}
	result, err := a.client.ExtractEntitiesFromSegmentWithModel(ctx, detail.EntityExtractionRequest{
		SegmentText: sourceText,
		EntityCount: limit,
		Language:    language,
	}, model)
	if err != nil || result == nil {
		return nil, err
	}
	return result.FrasiImportanti, nil
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
	if a == nil || a.client == nil {
		return nil, nil
	}
	if len(sourceTexts) == 0 {
		return nil, nil
	}
	phrases := make([][]string, len(sourceTexts))
	for start := 0; start < len(sourceTexts); start += client.EntityExtractionBatchLimit {
		end := min(start+client.EntityExtractionBatchLimit, len(sourceTexts))
		chunk := sourceTexts[start:end]
		results, err := a.client.ExtractEntitiesFromBatchWithModel(ctx, chunk, limit, model, language)
		if err != nil {
			return nil, fmt.Errorf("important phrase batch [%d:%d]: %w", start, end, err)
		}
		if len(results) != len(chunk) {
			return nil, fmt.Errorf("important phrase batch [%d:%d]: got %d results for %d segments", start, end, len(results), len(chunk))
		}
		for i, result := range results {
			if result == nil {
				continue
			}
			phrases[start+i] = result.FrasiImportanti
		}
	}
	return phrases, nil
}

var _ scriptgen.ImportantPhraseExtractor = (*OllamaImportantPhraseExtractor)(nil)
var _ scriptgen.BatchImportantPhraseExtractor = (*OllamaImportantPhraseExtractor)(nil)
