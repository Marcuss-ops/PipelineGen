// Package translation — ollama_translator_batch.go: the Ollama adapter's
// implementation of the optional BatchTranslationPort capability.
//
// It is a thin projection of the platform call (one chat request per chunk,
// cache-aware, JSON-contract validated) onto the application-layer DTOs, so the
// batching decision stays at the port surface and the wire logic stays in
// platform/ollama.
package translation

import (
	"context"
	"fmt"
	"strings"

	ollama "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
)

// TranslateBatch implements BatchTranslationPort. The provider resolves the
// per-cue batching so a subtitle fan-out issues one request per chunk of cues
// instead of one request per cue.
//
// Failure is fail-closed and typed: an error means no segment of the failed
// chunk is usable, and the caller falls back to Translate per segment.
func (o *OllamaTranslator) TranslateBatch(ctx context.Context, cmd BatchTranslationCommand) (BatchTranslationResult, error) {
	if o == nil || o.gen == nil {
		return BatchTranslationResult{}, fmt.Errorf("ollama client not initialized")
	}
	if len(cmd.Segments) == 0 {
		return BatchTranslationResult{}, nil
	}
	if strings.TrimSpace(cmd.TargetLang) == "" {
		return BatchTranslationResult{}, fmt.Errorf("translation.TranslateBatch: TargetLang is empty")
	}

	resolvedModel := ""
	if cmd.ModelPolicy != nil && cmd.ModelPolicy.Model != "" {
		resolvedModel = cmd.ModelPolicy.Model
	}

	segments := make([]ollama.BatchTranslationSegment, len(cmd.Segments))
	for i, segment := range cmd.Segments {
		segments[i] = ollama.BatchTranslationSegment{ID: segment.ID, Text: segment.Text}
	}

	translated, err := o.gen.TranslateBatchWithModel(ctx, segments, cmd.TargetLang, resolvedModel, cmd.ChunkSize)
	if err != nil {
		return BatchTranslationResult{
			UsedModel:    resolvedModel,
			UsedProvider: ProviderOllama,
		}, err
	}

	out := make([]BatchTranslationSegment, len(translated))
	for i, segment := range translated {
		out[i] = BatchTranslationSegment{ID: segment.ID, Text: segment.Text}
	}
	return BatchTranslationResult{
		Segments:     out,
		UsedModel:    resolvedModel,
		UsedProvider: ProviderOllama,
		CacheStatus:  "miss",
	}, nil
}
