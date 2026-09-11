package adapters

import (
	"context"

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

var _ scriptgen.ImportantPhraseExtractor = (*OllamaImportantPhraseExtractor)(nil)
