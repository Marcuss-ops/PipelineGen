// Package app — build_bundles_texttracks.go: composition glue for
// the TextTrackMaterializer + the asset.text.materialize job handler
// + the AcquireService (Fase 5).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): added
// AcquireService to the bundle so the backfill CLI can trigger
// the full 5-priority chain (DB → local VTT/SRT → YouTube subs →
// Whisper) when the source track is missing.
package wiring

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TextTrackBundle groups the materializer + the broker-facing
// job handler + the acquire service (Fase 5) + the post-publish
// fan-out helper (Fase 4).
//
// Fase 4 (July 2026): FanOut is the canonical post-publish
// enqueue helper that pipeline finalizers (YouTube, Artlist,
// Stock, Voiceover) call to schedule asset.text.materialize
// translation jobs AFTER their canonical asset.index.requested
// outbox emission has committed. Exposing it on the bundle
// lets composition root thread it into every pipeline's
// finalizer without each pipeline importing the texttracks
// package directly.
type TextTrackBundle struct {
	Materializer   *texttracks.Materializer
	JobHandler     *texttracks.MaterializeJobHandler
	AcquireService *texttracks.AcquireService
	FanOut         *texttracks.MaterializeFanOut

	// Translator is the canonical clip-translation port (Argos primary +
	// Ollama fallback, or Ollama-only per translation_provider). Exposed so
	// other consumers (e.g. the multilingual render admin command's
	// CueTranslator) route through the SAME provider chain as the
	// materializer instead of reaching Ollama directly.
	Translator translation.TranslationPort

	// ArgosServer is the persistent Argos Translate sidecar adapter
	// (PR-ARGOS-TRANSLATION, Aug 2026). Exposed so the composition
	// root can register its Stop on graceful shutdown. nil when the
	// provider is ollama-only or the bridge is unavailable.
	ArgosServer *translation.ArgosServerTranslator
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
	Drive     drivepkg.Reader
	CueWriter texttracks.TimedCueWriter
}

// BuildTextTrackBundle constructs the canonical bundle.
//
// godlike/07 fail-closed: a nil dep or invalid config
// surfaces as a typed error.
//
// Fase 4 (July 2026): the FanOut field is left nil here so
// existing callers do NOT need to thread a jobs.Enqueuer
// through this constructor. Composition roots that wire
// FanOut (e.g. production NewComposition) must call
// WireTextTracksFanOut(textTracks, jobsService, log) AFTER
// BuildTextTrackBundle returns. This is a godlike/07
// backward-compatible split: the FanOut wiring is a
// forward-only addition; tests + compositions that don't
// need FanOut continue to work unchanged.
func BuildTextTrackBundle(
	cfg *config.Config,
	repos *RepoBundle,
	ai *AIBundle,
	outbox *OutboxBundle,
	acquirePorts *AcquirePorts,
	publisher delivery.Publisher,
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

	mlCfg := ActiveMultilingualConfig(cfg)
	registry, err := BuildLanguageRegistry(mlCfg)
	if err != nil {
		return nil, fmt.Errorf("compose texttracks: language registry: %w", err)
	}

	// PR-ARGOS-TRANSLATION (Aug 2026): the active provider strategy is
	// selected by media.multilingual.translation_provider (argos|ollama).
	// Argos Translate is the deterministic, CPU-only primary; Ollama is
	// the quality fallback. Construction is fail-SOFT: when the Argos
	// bridge is unavailable the materializer falls back to Ollama-only
	// and the request fingerprint stays on the Ollama model taxonomy
	// (no provider-name leak into persisted provenance).
	ollamaModel := resolveTranslationModel(mlCfg.TranslationPolicy)
	translationModel := ollamaModel
	modelVersion := cfg.External.OllamaModel

	var clipTranslator translation.TranslationPort = ai.OllamaTranslator
	var argosServer *translation.ArgosServerTranslator

	if resolveTranslationProvider(mlCfg.TranslationProvider) == "argos" {
		server, argosErr := translation.NewArgosServerTranslator(
			translation.ArgosServerConfig{
				ScriptsDir: cfg.Paths.PythonScriptsDir,
				PythonBin:  cfg.Paths.ArgosPythonBin,
			},
			log,
		)
		if argosErr != nil {
			log.Warn("ArgosTranslator unavailable; using Ollama-only translation",
				zap.Error(argosErr))
		} else {
			argosServer = server
			clipTranslator = translation.NewFallbackTranslator(server, ai.OllamaTranslator, log)
			translationModel = translation.ArgosTranslationModel
			modelVersion = translation.ArgosTranslationModelVersion
			log.Info("ArgosTranslator wired as primary translation provider (Ollama fallback)")
		}
	}

	resolverCfg := texttracks.ResolverConfig{
		Registry:          registry,
		SourceLanguage:    mlCfg.SourceLanguage,
		ModelVersion:      modelVersion,
		PromptVersion:     resolveTranslationPromptVersion(cfg),
		TranslationPolicy: mlCfg.TranslationPolicy,
		TranslationModel:  translationModel,
		OllamaModel:       ollamaModel,
	}

	materializer, err := texttracks.NewMaterializer(
		repos.TextTrackRepo,
		clipTranslator,
		outbox.EventsRepo,
		resolverCfg,
		log,
	)
	if err != nil {
		return nil, fmt.Errorf("compose texttracks: materializer: %w", err)
	}
	// Parallel per-language translation fan-out. The upstream translator
	// (Ollama) is the dominant per-language cost; overlapping the calls
	// hides its latency. Keep a modest bound so a single materialize run
	// never saturates the LLM/GPU.
	materializer.SetConcurrency(4)

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
		if acquirePorts.Drive != nil {
			acquireService.WithDrive(acquirePorts.Drive)
		}
	}
	backfill, err := texttracks.NewBackfillService(texttracks.BackfillServiceDeps{
		Data: texttracks.BackfillDataDeps{
			Clips:      repos.ClipsRepo,
			Repo:       repos.TextTrackRepo,
			Cues:       acquirePorts.CueWriter,
			SubArtRepo: repos.SubtitleArtifactRepo,
		},
		Pipeline: texttracks.BackfillPipelineDeps{
			Materializer: materializer,
			Acquirer:     acquireService,
		},
		Delivery: texttracks.BackfillDeliveryDeps{
			Publisher:     publisher,
			DriveFolderID: cfg.Drive.ClipsFolder(),
		},
		Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("compose texttracks: automatic backfill: %w", err)
	}
	handler.WithBackfill(backfill)

	return &TextTrackBundle{
		Materializer:   materializer,
		JobHandler:     handler,
		AcquireService: acquireService,
		Translator:     clipTranslator,
		ArgosServer:    argosServer,
		// FanOut is populated by WireTextTracksFanOut (called
		// after NewComposition assembles the JobsBundle so the
		// fan-out can reach the broker).
	}, nil
}

// WireTextTracksFanOut populates the TextTrackBundle.FanOut
// field with a MaterializeFanOut wired to the canonical
// jobs broker. Composition root (internal/app/composition.go)
// calls this AFTER BuildTextTrackBundle returns + AFTER
// JobsBundle has been built (so the enqueuer surface is
// available). nil-tolerant: when jobsService is nil (test
// fixtures, disabled-mode wiring), FanOut stays nil and
// per-pipeline finalizers gracefully skip the fan-out call.
//
// godlike/07 NO-FAKE-AVAILABILITY: FanOut nil is observable
// to the finalizer hooks as a no-op (the helper returns
// nil error in disabled mode). Production composition MUST
// inject a non-nil jobsService to wire active fan-out.
//
// godlike/06 SSOT: this is the SOLE canonical wiring site
// for the post-publish enqueue helper. The 5 pipeline
// finalizers (YouTube, Artlist, Stock, Voiceover, plus the
// canonical AssetFinalizerTx post-commit hook) all reach
// FanOut via composition-root-threaded deps; no other site
// constructs a MaterializeFanOut.
func WireTextTracksFanOut(
	textTracks *TextTrackBundle,
	jobsService texttracks.MaterializeEnqueuer,
	log *zap.Logger,
) {
	if textTracks == nil {
		return
	}
	if jobsService == nil {
		// Disabled-mode wiring (test fixture or composition
		// opt-out). Log Info so operators can identify the
		// misconfiguration without a hard boot-time failure.
		if log != nil {
			log.Info("WireTextTracksFanOut: jobsService nil — FanOut disabled (per-pipeline finalizers will skip asset.text.materialize enqueue)")
		}
		return
	}
	textTracks.FanOut = texttracks.NewMaterializeFanOut(jobsService, log)
}

// WireTextTrackJobBindings registers the asset.text.materialize
// handler with the canonical jobs.Service.
func WireTextTrackJobBindings(
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

// BuildLanguageRegistry constructs the canonical
// asset.LanguageRegistry from cfg.MultilingualConfig. godlike/06
// SSOT: this helper is the SOLE canonical owner of the
// "YAML → registry" projection. Two-tier priority:
//
//  1. cfg.MultilingualConfig.Languages — the typed
//     `languages:` list (PR-CATALOG-MULTILINGUA step 3
//     SSOT). Each entry is verified by the
//     LanguageSpecSlice.UnmarshalYAML hook (legacy CSV
//     auto-promoted to defaults; struct-list preserved
//     verbatim).
//  2. asset.EmptyLanguageRegistry() — pipeline in disabled
//     mode. godlike/07 fail-closed; no silent fallback to
//     "en".
//
// Registry-construction errors (duplicate code, invalid spec)
// are returned to the caller — BuildTextTrackBundle surfaces
// them via the same `fmt.Errorf("…: %w", err)` chain that
// surfaces every other compose-time failure. godlike/07
// fail-closed does NOT require a panic: an error returned
// from the composition root IS the boot-time fail-fast.
func BuildLanguageRegistry(ml config.MultilingualConfig) (asset.LanguageRegistry, error) {
	if len(ml.Languages) > 0 {
		reg, err := asset.NewLanguageRegistry(ml.Languages)
		if err != nil {
			return nil, fmt.Errorf("typed Languages list rejected: %w", err)
		}
		return reg, nil
	}
	return asset.EmptyLanguageRegistry(), nil
}

// BuildMultilingualLanguageCSV projects the canonical registry onto a
// deterministic comma-separated language list. Callers can filter the
// enabled set when a specific capability is needed (e.g. subtitle
// probing wants TranslateClips=true targets only).
func BuildMultilingualLanguageCSV(ml config.MultilingualConfig, filter func(asset.LanguageSpec) bool) (string, error) {
	reg, err := BuildLanguageRegistry(ml)
	if err != nil {
		return "", err
	}
	specs := reg.EnabledLanguages()
	codes := make([]string, 0, len(specs))
	for _, spec := range specs {
		if filter != nil && !filter(spec) {
			continue
		}
		codes = append(codes, spec.Code)
	}
	return buildBcp47CSV(codes), nil
}

// ActiveMultilingualConfig picks the nested media.multilingual config
// when present and falls back to the legacy top-level Multilingual
// block for back-compat tests and old YAMLs.
func ActiveMultilingualConfig(cfg *config.Config) config.MultilingualConfig {
	if cfg == nil {
		return config.MultilingualConfig{}
	}
	nested := cfg.Media.Multilingual
	if len(nested.Languages) > 0 ||
		nested.Enabled ||
		nested.RequireLanguageCertainty ||
		nested.RequireTranscriptReady ||
		nested.RequireAllLanguagesBeforeVideo ||
		nested.SourceLanguage != "" ||
		nested.TranslationPolicy != "" ||
		nested.TranslationProvider != "" {
		return nested
	}
	return cfg.Multilingual
}

// resolveTranslationPromptVersion returns the active translation
// prompt version. Hardcoded to "v1" for Fase 3; a future PR
// adds cfg.AI.TranslationPromptVersion.
func resolveTranslationPromptVersion(_ *config.Config) string {
	return "v1"
}

// resolveTranslationProvider maps media.multilingual.translation_provider
// to the canonical provider strategy token. "ollama" → Ollama-only;
// anything else ("argos", "auto", empty) → Argos primary + Ollama fallback
// (the default).
func resolveTranslationProvider(provider string) string {
	if strings.EqualFold(strings.TrimSpace(provider), "ollama") {
		return "ollama"
	}
	return "argos"
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
func buildBcp47CSV(codes []string) string {
	var out []string
	for _, raw := range codes {
		normalized, err := asset.Normalize(raw)
		if err != nil || normalized == "und" {
			continue
		}
		out = append(out, normalized)
	}
	return strings.Join(out, ",")
}
