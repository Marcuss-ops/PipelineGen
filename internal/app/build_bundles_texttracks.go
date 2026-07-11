// Package app — build_bundles_texttracks.go: composition glue for
// the TextTrackMaterializer + the asset.text.materialize job handler.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
package app

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TextTrackBundle groups the materializer + the broker-facing
// job handler.
type TextTrackBundle struct {
	Materializer *texttracks.Materializer
	JobHandler   *texttracks.MaterializeJobHandler
}

// BuildTextTrackBundle constructs the canonical bundle.
//
// godlike/07 fail-closed: a nil dep or invalid config
// surfaces as a typed error.
func BuildTextTrackBundle(
	cfg *config.Config,
	repos *RepoBundle,
	ai *AIBundle,
	outbox *OutboxBundle,
	log *zap.Logger,
) (*TextTrackBundle, error) {
	if repos == nil || repos.TextTrackRepo == nil {
		return nil, fmt.Errorf("compose texttracks: RepoBundle.TextTrackRepo is required")
	}
	if ai == nil || ai.OllamaTranslator == nil {
		return nil, fmt.Errorf("compose texttracks: AIBundle.OllamaTranslator is required")
	}
	if outbox == nil || outbox.EventsRepo == nil {
		return nil, fmt.Errorf("compose texttracks: OutboxBundle.EventsRepo is required")
	}
	if log == nil {
		return nil, fmt.Errorf("compose texttracks: log is required")
	}

	resolverCfg := texttracks.ResolverConfig{
		MaterializeLanguages: cfg.Media.Multilingual.MaterializeLanguages,
		SourceLanguage:       cfg.Media.Multilingual.SourceLanguage,
		ModelVersion:         cfg.External.OllamaModel,
		PromptVersion:        resolveTranslationPromptVersion(cfg),
		TranslationPolicy:    cfg.Media.Multilingual.TranslationPolicy,
		TranslationModel:     resolveTranslationModel(cfg.Media.Multilingual.TranslationPolicy),
	}

	materializer, err := texttracks.NewMaterializer(
		repos.TextTrackRepo,
		ai.OllamaTranslator,
		outbox.EventsRepo,
		resolverCfg,
		log,
	)
	if err != nil {
		return nil, fmt.Errorf("compose texttracks: materializer: %w", err)
	}

	handler := texttracks.NewMaterializeJobHandler(materializer, log)
	return &TextTrackBundle{
		Materializer: materializer,
		JobHandler:   handler,
	}, nil
}

// wireTextTrackJobBindings registers the asset.text.materialize
// handler with the canonical jobs.Service.
func wireTextTrackJobBindings(
	textTracks *TextTrackBundle,
	jobsBundle *JobsBundle,
) error {
	if textTracks == nil || textTracks.JobHandler == nil {
		return fmt.Errorf("wire texttracks: TextTrackBundle.JobHandler is required")
	}
	if jobsBundle == nil || jobsBundle.Service == nil {
		return fmt.Errorf("wire texttracks: JobsBundle.Service is required")
	}
	if err := textTracks.JobHandler.Register(jobsBundle.Service); err != nil {
		return fmt.Errorf("wire texttracks: register handler: %w", err)
	}
	return nil
}

// resolveTranslationPromptVersion returns the active translation
// prompt version. Hardcoded to "v1" for Fase 3; a future PR
// adds cfg.AI.TranslationPromptVersion.
func resolveTranslationPromptVersion(_ *config.Config) string {
	return "v1"
}

// resolveTranslationModel maps MultilingualConfig.TranslationPolicy
// to the concrete Ollama model name passed to TranslationPort.
//
// godlike/06 SSOT: this helper is the SOLE canonical owner of
// the policy → model mapping.
//
//   - "auto"    → "" (server default; provider picks)
//   - "fast"    → "gemma3:4b" (canonical fast model)
//   - "quality" → "llama3:70b" (canonical quality model)
//
// A future PR adds cfg.AI.TranslationModel so operators can
// override the concrete model without editing the Go struct.
func resolveTranslationModel(policy string) string {
	switch policy {
	case "fast":
		return "gemma3:4b"
	case "quality":
		return "llama3:70b"
	default:
		return ""
	}
}
