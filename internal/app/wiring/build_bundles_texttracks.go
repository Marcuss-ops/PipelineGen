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
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// newPostgresMediaAssetLister returns the canonical PostgreSQL media SSOT clip
// reader for the batch/fan-out asset lookups (BackfillService + the
// `asset.text.materialize` handler). A closed media plane returns nil — the
// documented degraded signal, never a panicking searcher.
func newPostgresMediaAssetLister(mediaPG *sql.DB) texttracks.MediaAssetLister {
	if mediaPG == nil {
		return nil
	}
	return pgmedia.NewMediaClipAssetReader(pgmedia.NewMediaSearcher(mediaPG))
}

// subtitleRootLayoutResolver is the single owner of the subtitle-artifact
// Drive LAYOUT: <configured subtitle root>/<source video id>/ — exactly the
// folder the extraction pipeline uploaded the .txt transcript sidecar into — so
// the per-language .ass artifacts are co-located with it instead of being
// nested under the clips/media tree.
//
// It returns the ROOT plus the per-video child PATH and leaves the get-or-create
// to the publisher, which is the surface every composition that can publish
// subtitle artifacts already wires. Resolving the child id here instead would
// need the Drive folder-admin port, and that port is NOT present in every
// composition: the operator text-tracks backfill CLI publishes correctly while
// its folder admin is nil, so an admin-dependent resolver silently degraded to
// <clips root>/Ass Sub/ — splitting one clip's subtitles across two trees.
type subtitleRootLayoutResolver struct{ rootID string }

// NewSubtitleRootLayoutResolver returns nil when no subtitle root is
// configured: the BackfillService then keeps its documented legacy layout
// instead of failing the composition.
//
// Exported because there is more than one BackfillService construction site
// (the runtime bundle here and the operator CLI in cmd/admin) and the Drive
// layout MUST be identical for both: when the CLI omitted this seam every .ass
// it delivered landed in <clips root>/Ass Sub/ while the .txt sidecar sat in
// <subtitle root>/<videoID>/ — the split this resolver exists to remove.
func NewSubtitleRootLayoutResolver(rootID string) texttracks.SubtitleFolderResolver {
	if strings.TrimSpace(rootID) == "" {
		return nil
	}
	return &subtitleRootLayoutResolver{rootID: rootID}
}

func (r *subtitleRootLayoutResolver) ResolveSubtitleLocation(_ context.Context, assetID, sourceVideoID string) (texttracks.SubtitleLocation, error) {
	if r == nil || strings.TrimSpace(r.rootID) == "" {
		return texttracks.SubtitleLocation{}, nil
	}
	name := strings.TrimSpace(sourceVideoID)
	if name == "" {
		name = strings.TrimSpace(assetID)
	}
	if name == "" {
		return texttracks.SubtitleLocation{FolderID: r.rootID}, nil
	}
	// Mirror the extraction's own layout EXACTLY:
	// <subtitle root>/<SubtitleDriveGroup>/<videoID>/. The namespace segment is
	// the youtube drive adapter's constant, not a second literal, so the .txt
	// sidecar and the per-language .ass files can never drift into two trees.
	return texttracks.SubtitleLocation{
		FolderID: r.rootID,
		Subpath:  []string{ytadapters.SubtitleDriveGroup, name},
	}, nil
}

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

	// MediaAssets is the canonical PostgreSQL media SSOT reader used by
	// the BackfillService to load the asset behind an
	// `asset.text.materialize` job. It is SEPARATE from repos.ClipsRepo
	// on purpose: that facade is the legacy operational SQLite store, so
	// a clip the YouTube pipeline committed to PostgreSQL was invisible
	// to the fan-out and the job dead-lettered with "asset not found for
	// acquisition" (no translations, no subtitle artifacts). Optional:
	// nil keeps the legacy facade as the documented degraded path.
	MediaAssets texttracks.MediaAssetLister

	// SubtitleFolders is the single owner of the subtitle artifact Drive
	// FOLDER, so .ass and .txt share one per-video destination. Optional:
	// nil keeps the legacy asset-folder + "Ass Sub" layout.
	SubtitleFolders texttracks.SubtitleFolderResolver
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

	// POSTGRES-MEDIA-CUTOVER (September 2026): route the post-translation
	// reindex through the PostgreSQL media index plane and recompose
	// media_assets.search_text before requesting it.
	//
	// BEFORE this wiring the materializer emitted asset.index.requested into
	// the operational SQLite outbox. The SQLite outbox has NO media handler
	// in any mode (assertSingleMediaIndexOwner fails boot closed on one), so
	// every post-translation reindex silently dead-lettered and the nine
	// translated transcripts never reached pgvector — even though their rows
	// were durably present in asset_text_tracks.
	if outbox.MediaIndexRequester != nil {
		materializer.SetIndexRequester(outbox.MediaIndexRequester)
	}
	if outbox.MediaSearchTextRebuilder != nil {
		materializer.SetSearchTextRebuilder(outbox.MediaSearchTextRebuilder)
	}
	if outbox.MediaIndexRequester == nil {
		log.Warn("POSTGRES-MEDIA-CUTOVER: no PostgreSQL media index requester wired — asset.text.materialize reindex requests fall back to the operational SQLite outbox, which owns no media index handler (graceful degrade, godlike/07)")
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
		if acquirePorts.Drive != nil {
			acquireService.WithDrive(acquirePorts.Drive)
		}
	}
	// MEDIA DEMOLITION graceful degrade (September 2026): the automatic
	// backfill writes cue segments through the canonical media writer
	// (TimedCueWriter over the PostgreSQL media SSOT). When the media
	// plane is NOT DEPLOYED the CueWriter port is nil — skip the backfill
	// registration instead of failing the whole composition (godlike/07:
	// degraded is honest; the backfill CLI remains available explicitly).
	if acquirePorts == nil || acquirePorts.CueWriter == nil {
		log.Warn("POSTGRES-MEDIA-CUTOVER: media PostgreSQL unavailable — automatic text-track backfill NOT registered (cues writer is the canonical media writer; graceful degrade, godlike/07)")
	} else {
		// POSTGRES-MEDIA-CUTOVER: the backfill's asset reader MUST be the
		// PostgreSQL media SSOT. Wiring the legacy SQLite facade here made
		// the direct YouTube fan-out read a catalog that does not contain
		// the clip it had just committed to PostgreSQL, so
		// `asset.text.materialize` dead-lettered with "asset not found for
		// acquisition" and the clip received no translations and no
		// subtitle artifacts.
		clipsLister := texttracks.MediaAssetLister(repos.ClipsRepo)
		if acquirePorts.MediaAssets != nil {
			clipsLister = acquirePorts.MediaAssets
		} else {
			log.Warn("POSTGRES-MEDIA-CUTOVER: backfill asset reader is the LEGACY SQLite facade (PostgreSQL media reader not wired) — a clip committed to the media SSOT will not be found by asset.text.materialize (graceful degrade, godlike/07)")
		}
		backfill, err := texttracks.NewBackfillService(texttracks.BackfillServiceDeps{
			Data: texttracks.BackfillDataDeps{
				Clips:      clipsLister,
				Repo:       repos.TextTrackRepo,
				Cues:       acquirePorts.CueWriter,
				SubArtRepo: repos.SubtitleArtifactRepo,
			},
			Pipeline: texttracks.BackfillPipelineDeps{
				Materializer: materializer,
				Acquirer:     acquireService,
			},
			Delivery: texttracks.BackfillDeliveryDeps{
				Publisher:       publisher,
				DriveFolderID:   cfg.Drive.ClipsFolder(),
				SubtitleFolders: acquirePorts.SubtitleFolders,
				// Timing-faithful alignment: each source cue is translated
				// individually so a translated segment keeps its source cue's
				// exact window (1:1). It goes through the SAME clipTranslator
				// every other consumer uses (Argos primary + Ollama fallback),
				// and a per-language failure degrades to CuesWithText instead
				// of leaving the language without an artifact.
				CueTranslator: texttracks.NewCueTranslator(clipTranslator, mlCfg.SourceLanguage, ollamaModel, texttracks.DefaultCueTranslationConcurrency, log),
			},
			Log: log,
		})
		if err != nil {
			return nil, fmt.Errorf("compose texttracks: automatic backfill: %w", err)
		}
		handler.WithBackfill(backfill)
	}

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

// SubtitleAcquisitionLanguages resolves the ACQUISITION language set for the
// subtitle fetcher: exactly ONE source-language track, never the configured
// translation set.
//
// ACQUISITION vs MATERIALIZATION (Sept 2026): wiring the whole registry here
// (it,en,pl,ru,de,es,pt-BR,fr,tr,id) made yt-dlp ask YouTube for ten subtitle
// tracks in a single call, get HTTP 429 on the first and abort, leaving the
// clip with no transcript at all. The nine translations are the
// MATERIALIZATION stage's output (Argos/Ollama) and are never requested from
// YouTube. godlike/06 SSOT: this helper is the sole owner of that rule.
func SubtitleAcquisitionLanguages(ml config.MultilingualConfig) string {
	lang := strings.TrimSpace(ml.SourceLanguage)
	if lang == "" {
		return "en"
	}
	return lang
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

// ResolveTranslationModel exposes the canonical policy → model mapping to the
// operator CLIs, so the CueTranslator they build routes the Ollama fallback to
// the same model the runtime bundle uses (godlike/06: one owner of the
// policy → model decision, never two).
func ResolveTranslationModel(cfg *config.Config) string {
	return resolveTranslationModel(ActiveMultilingualConfig(cfg).TranslationPolicy)
}

// buildBcp47CSV normalizes language codes into the canonical CSV form the
// yt-dlp subtitle flags expect, dropping anything that normalizes to "und"
// (undetermined) instead of emitting an invalid tag.
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
