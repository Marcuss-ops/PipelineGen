// Package app — build_bundles_texttracks.go: composition glue for
// the TextTrackMaterializer + the asset.text.materialize job handler
// + the AcquireService (Fase 5).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): added
// AcquireService to the bundle so the backfill CLI can trigger
// the full 5-priority chain (DB → local VTT/SRT → YouTube subs →
// Whisper) when the source track is missing.
package app

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TextTrackBundle groups the materializer + the broker-facing
// job handler + the acquire service (Fase 5).
type TextTrackBundle struct {
	Materializer   *texttracks.Materializer
	JobHandler     *texttracks.MaterializeJobHandler
	AcquireService *texttracks.AcquireService
}

// AcquirePorts groups the two ports the AcquireService needs.
// The composition root derives them from the existing
// build_bundles_domain_media.go (SubtitleFetcherAdapter) and
// the AIBundle (WhisperTranscriberAdapter). Wrapping them in a
// single struct keeps the BuildTextTrackBundle signature
// stable when more ports are added in a future Fase.
type AcquirePorts struct {
	Subtitles youtubeports.SubtitleFetcherPort
	Whisper   youtubeports.WhisperTranscriberPort
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
	acquirePorts *AcquirePorts,
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

	// AcquireService (Fase 5): wraps the SubtitleFetcherPort
	// + WhisperTranscriberPort into a single typed surface for
	// the backfill CLI. Both ports are OPTIONAL — the
	// AcquireService silently skips a nil port (the chain
	// falls through to the next priority). This preserves
	// backward compat: dev/test compositions can pass a nil
	// AcquirePorts to get a backfill CLI that only does
	// translation fan-out (no source acquisition).
	var acquireService *texttracks.AcquireService
	if acquirePorts != nil {
		// texttracks.SubtitlesPort is a NARROW interface
		// (only FetchSegmentSubtitles). The concrete
		// *ytinfra.SubtitleFetcherAdapter satisfies both
		// the narrow texttracks interface AND the full
		// youtubeports.SubtitleFetcherPort — we just pass
		// the same instance to the narrow type assertion
		// (structural typing in Go).
		var subsPort texttracks.SubtitlesPort
		if acquirePorts.Subtitles != nil {
			if sp, ok := acquirePorts.Subtitles.(texttracks.SubtitlesPort); ok {
				subsPort = sp
			} else {
				return nil, fmt.Errorf("compose texttracks: acquirePorts.Subtitles does not satisfy texttracks.SubtitlesPort (got %T)", acquirePorts.Subtitles)
			}
		}
		var whispPort texttracks.WhisperPort
		if acquirePorts.Whisper != nil {
			if wp, ok := acquirePorts.Whisper.(texttracks.WhisperPort); ok {
				whispPort = wp
			} else {
				return nil, fmt.Errorf("compose texttracks: acquirePorts.Whisper does not satisfy texttracks.WhisperPort (got %T)", acquirePorts.Whisper)
			}
		}
		acquireService, err = texttracks.NewAcquireService(subsPort, whispPort, log)
		if err != nil {
			return nil, fmt.Errorf("compose texttracks: acquire service: %w", err)
		}
	}

	return &TextTrackBundle{
		Materializer:   materializer,
		JobHandler:     handler,
		AcquireService: acquireService,
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
