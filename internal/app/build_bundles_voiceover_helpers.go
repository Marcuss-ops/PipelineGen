package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

func buildVoiceoverSemanticTagger(metaWriter semantic.MetadataWriterPort) func(context.Context, string, string, string, string) (*voiceover.SemanticTaggerResult, error) {
	return func(ctx context.Context, prompt, style, mediaType, generator string) (*voiceover.SemanticTaggerResult, error) {
		if metaWriter == nil {
			return nil, fmt.Errorf("voiceover: metaWriter not wired (cannot enrich voiceover semantic metadata)")
		}
		payload, _, err := metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
			AssetID:   "",
			AssetType: "voiceover",
			MediaType: mediaType,
			Source:    "voiceover",
			Generator: generator,
			Style:     style,
			Prompt:    prompt,
		})
		if err != nil {
			return nil, err
		}
		return &voiceover.SemanticTaggerResult{
			SearchText: payload.SearchText,
			Tags:       payload.Tags,
			Subjects:   payload.Subjects,
			Mood:       payload.Mood,
		}, nil
	}
}

func buildVoiceoverTranslator(translationPort translation.TranslationPort) func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, text, targetLanguage string) (string, error) {
		if translationPort == nil {
			return text, nil
		}
		res, err := translationPort.Translate(ctx, translation.TranslationCommand{
			TargetLang: targetLanguage,
			Text:       text,
		})
		if err != nil {
			observability.TranslationFailuresTotal.Inc()
			return text, err
		}
		return res.TranslatedText, nil
	}
}

func buildVoiceoverOutboxEnqueuer(
	dispatcher *outbox.Dispatcher,
	clipIndexerService *clipindexer.Service,
	log *zap.Logger,
) voiceover.TxOutboxEnqueuer {
	if dispatcher == nil {
		log.Warn("voiceover service wired WITHOUT outbox dispatcher — indexing will be SKIPPED (no asset.index.requested events emitted)")
		return nil
	}
	if clipIndexerService == nil || !clipIndexerService.IsEnabled() {
		log.Warn("voiceover service wired with outbox dispatcher but clipIndexer disabled — asset.index.requested events will be enqueued but no consumer-side indexing will execute")
	}
	return dispatcher
}

type nopDestinationResolver struct{}

var _ voiceover.DestinationResolver = nopDestinationResolver{}

func (nopDestinationResolver) Resolve(_ context.Context, _ *voiceover.DestinationRequest) (*voiceover.ResolvedDestination, error) {
	return nil, nil
}
