package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	wiringaudio "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/audio"
	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/script"
	vowiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/voiceover"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	sqlitecheckpoint "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/checkpoint"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

import ()

// defaultRenderingGenStoreURL is the fallback object-store endpoint used when
// RENDERINGGEN_STORE_URL is unset (the local single-host deployment default).
// Owning it once keeps the overlay-asset prefetch and the Chronon timing
// sidecar fetch pointed at the same store.
const defaultRenderingGenStoreURL = "http://127.0.0.1:9000"

// scriptGenerationTranslator adapts the application translation port to the
// capability runtime without leaking provider DTOs into capabilities/scripts.
type scriptGenerationTranslator struct {
	port   translation.TranslationPort
	ollama translation.TranslationPort
}

func (a *scriptGenerationTranslator) Translate(ctx context.Context, input scriptgen.TranslationInput) (string, error) {
	if a == nil || a.port == nil {
		return "", fmt.Errorf("script generation translator is not configured")
	}
	port := a.port
	if strings.EqualFold(strings.TrimSpace(input.Provider), "ollama") {
		port = a.ollama
		if port == nil {
			return "", fmt.Errorf("script generation Ollama translator is not configured")
		}
	}
	if port == nil {
		return "", fmt.Errorf("script generation translator provider %q is not configured", input.Provider)
	}
	command := translation.TranslationCommand{
		SourceLang: string(input.SourceLanguage),
		TargetLang: string(input.TargetLanguage),
		Text:       input.SourceText,
	}
	if input.Model != "" || input.Provider != "" {
		command.ModelPolicy = &translation.ModelPolicy{Provider: input.Provider, Model: input.Model}
	}
	result, err := port.Translate(ctx, command)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.TranslatedText) == "" {
		return "", fmt.Errorf("script generation translator returned empty text")
	}
	return result.TranslatedText, nil
}

// scriptGenerationTranslationPort uses the provider chain selected by the
// canonical text-track bundle (Argos with Ollama fallback by default). The
// direct Ollama port remains the fallback for compositions that predate or
// intentionally omit that bundle.
func scriptGenerationTranslationPort(root *ComposeRoot) translation.TranslationPort {
	if root == nil {
		return nil
	}
	if root.TextTracks != nil && root.TextTracks.Translator != nil {
		return root.TextTracks.Translator
	}
	if root.AI != nil {
		return root.AI.OllamaTranslator
	}
	return nil
}

// artifactCheckpointFlag parses one localized_render rollout flag. Unknown
// values are a startup error: a rollout flag that silently does nothing is
// worse than a refused boot, and the flag must not accidentally enable a
// second kind before its loader exists.
func artifactCheckpointFlag(env string) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(env)))
	switch raw {
	case "", "0", "false", "off", "no", "disabled":
		return false, nil
	case "1", "true", "on", "yes", string(scriptgen.ArtifactCheckpointLocalizedRender):
		return true, nil
	default:
		return false, fmt.Errorf("%s=%q is not a supported rollout value (use 1/true or localized_render)", env, raw)
	}
}

func artifactCheckpointDualWriteFlag() (bool, error) {
	return artifactCheckpointFlag(scriptgen.ArtifactCheckpointDualWriteEnv)
}

func artifactCheckpointResumeFlag() (bool, error) {
	return artifactCheckpointFlag(scriptgen.ArtifactCheckpointResumeEnv)
}

func artifactCheckpointCutoverFlag() (bool, error) {
	return artifactCheckpointFlag(scriptgen.ArtifactCheckpointCutoverEnv)
}

func validateScriptGenerationRuntime(cfg *config.Config, root *ComposeRoot, runRepo scriptgen.RunRepository) error {
	if cfg == nil || root == nil || runRepo == nil {
		return fmt.Errorf("script generation runtime requires config, composition root, and run repository")
	}
	if root.AI == nil || root.AI.SceneTextGenerator == nil || root.AI.OllamaTranslator == nil {
		return fmt.Errorf("script generation runtime requires scene text and translation adapters")
	}
	if strings.TrimSpace(cfg.External.RustMusclesPath) == "" {
		return fmt.Errorf("script generation runtime requires Rust media executor path")
	}
	return nil
}

// buildScriptGenerationMedia binds the sampler policy once so select and
// select_group share the same operator configuration, then constructs the
// combined audio renderer over the configured video processor.
func buildScriptGenerationMedia(cfg *config.Config, root *ComposeRoot, log *zap.Logger) (*rustexec.MediaSamplerAdapter, *rustexec.CombinedAudioRenderer, error) {
	executor := rustexec.NewExecutor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	mediaSamplerExecutor := rustexec.NewExecutor(cfg.External.RustMediaSamplerPath, cfg.External.FfmpegPath, log)
	mediaSampler, err := rustexec.NewMediaSamplerWithPolicy(mediaSamplerExecutor, rustexec.NewSamplerPolicy(cfg.Media.Sampler))
	if err != nil {
		return nil, nil, fmt.Errorf("build MediaSampler adapter: %w", err)
	}
	videoProcessor := rustexec.NewConfiguredVideoProcessorWithExecutor(executor, root.MediaExec.Policy, root.MediaExec.Profile, log)
	audioRenderer, err := rustexec.NewCombinedAudioRenderer(videoProcessor)
	if err != nil {
		return nil, nil, fmt.Errorf("build combined audio renderer: %w", err)
	}
	return mediaSampler, audioRenderer, nil
}

func BuildScriptGenerationRuntime(cfg *config.Config, root *ComposeRoot, runRepo scriptgen.RunRepository, committer assetspersistence.AssetCommitter, log *zap.Logger, vidRushProviders *documentadapters.VidRushAssetProviderRegistry, vidRushFinalizer scriptports.VidRushArtifactFinalizer, vidRushCache scriptports.VidRushCachePort) (*scriptgen.Runner, error) {
	if err := validateScriptGenerationRuntime(cfg, root, runRepo); err != nil {
		return nil, err
	}
	mediaSampler, audioRenderer, err := buildScriptGenerationMedia(cfg, root, log)
	if err != nil {
		return nil, err
	}
	runner := scriptgen.NewRunner(
		runRepo,
		root.AI.SceneTextGenerator,
		&scriptGenerationTranslator{port: scriptGenerationTranslationPort(root), ollama: root.AI.OllamaTranslator},
		root.AI.ScriptVoiceoverGenerator,
	)
	// Bind the run logger BEFORE any other wiring: NewRunner defaults to
	// zap.NewNop(), so without this call every runner decision (stage
	// transitions, prefetch outcome, skipped/failed units, checkpoint
	// reuse) was invisible in production logs while the same code logged
	// normally in tests. Nil-safe.
	runner.SetLogger(log)
	runner.SetOverlayJoinEncoder(root.MediaExec.Policy)
	// The local render lane produces one certified clip per overlay item; the
	// join needs those exact bytes back from the object store to place them on
	// the plan timeline. Wiring the fetcher is what turns a render-enabled job
	// into a job that owns its own MP4 instead of shipping audio only.
	storeURL := strings.TrimSpace(os.Getenv("RENDERINGGEN_STORE_URL"))
	if storeURL == "" {
		storeURL = defaultRenderingGenStoreURL
	}
	runner.SetOverlayClipFetcher(renderinggen.NewOverlayClipFetcher(storeURL))
	if err := wireArtifactCheckpointRollout(runner, root, committer, log); err != nil {
		return nil, err
	}
	semanticAnalyzer, err := wireScriptGenerationSemanticAnalyzer(runner, cfg, log)
	if err != nil {
		return nil, err
	}
	if cfg != nil && cfg.Scripts.LocalizedRenderConcurrency > 0 {
		runner.SetTTSConcurrency(cfg.Scripts.LocalizedRenderConcurrency)
	}
	if err := wireScriptGenerationAudioAssets(runner, cfg, root, committer, audioRenderer, log); err != nil {
		return nil, err
	}
	if err := wireScriptGenerationFinalJob(runner, cfg, root, log); err != nil {
		return nil, err
	}
	wireScriptGenerationCheckpointResolver(runner, root, committer, log)
	if err := wireScriptGenerationMediaPreflight(runner, cfg, root, vidRushProviders, log); err != nil {
		return nil, err
	}
	runner.SetOverlayRegistry(capmodel.DefaultChrononOverlayRegistry)
	if err := wireScriptGenerationOverlayQueue(runner, cfg, root, log); err != nil {
		return nil, err
	}
	if err := wireScriptGenerationPersistence(runner, root, log); err != nil {
		return nil, err
	}
	if err := wireScriptGenerationVidRush(runner, cfg, root, log, vidRushProviders, vidRushFinalizer, vidRushCache, mediaSampler, semanticAnalyzer); err != nil {
		return nil, err
	}
	wireScriptGenerationConcurrency(runner, cfg, root, log)

	return runner, nil
}

// wireArtifactCheckpointRollout applies the localized-render artifact
// checkpoint rollout flags. Extracted from the runtime builder so the cutover
// invariant (cutover requires dual-write AND resume) is one readable unit: a
// flag that is set without its prerequisite is a refused boot, never a silent
// half-migration.
func wireArtifactCheckpointRollout(runner *scriptgen.Runner, root *ComposeRoot, committer assetspersistence.AssetCommitter, log *zap.Logger) error {
	dualWrite, err := artifactCheckpointDualWriteFlag()
	if err != nil {
		return err
	}
	resumeCheckpoints, err := artifactCheckpointResumeFlag()
	if err != nil {
		return err
	}
	cutover, err := artifactCheckpointCutoverFlag()
	if err != nil {
		return err
	}
	if cutover && (!dualWrite || !resumeCheckpoints) {
		return fmt.Errorf("artifact checkpoint cutover requires both %s and %s", scriptgen.ArtifactCheckpointDualWriteEnv, scriptgen.ArtifactCheckpointResumeEnv)
	}
	if dualWrite || resumeCheckpoints || cutover {
		if root.ObservabilityDB == nil || root.ObservabilityDB.DB == nil {
			return fmt.Errorf("artifact checkpoints require observability DB")
		}
		artifactRepo, repoErr := sqlitescripts.NewSQLiteArtifactCheckpointRepository(root.ObservabilityDB.DB)
		if repoErr != nil {
			return fmt.Errorf("artifact checkpoints: %w", repoErr)
		}
		runner.SetArtifactCheckpointRepository(artifactRepo)
		if dualWrite {
			runner.SetArtifactCheckpointDualWrite(true)
		}
		var verifier capcheckpoint.ArtifactVerifier
		if resumeCheckpoints {
			runner.SetArtifactCheckpointResume(true)
			verifier = mediaCheckpointArtifactVerifier(committer, log)
			if verifier == nil {
				log.Warn("artifact checkpoint resume: published-artifact verifier NOT wired; only local RENDERED checkpoints are reusable")
			} else {
				runner.SetArtifactCheckpointVerifier(verifier)
			}
		}
		if cutover {
			if verifier == nil {
				return fmt.Errorf("artifact checkpoint cutover requires the media-SSOT artifact verifier")
			}
			runner.SetArtifactCheckpointCutover(true)
		}
		log.Info("artifact checkpoints enabled",
			zap.Strings("kinds", []string{
				string(scriptgen.ArtifactCheckpointLocalizedRender),
				string(scriptgen.ArtifactCheckpointTranslation),
			}),
			zap.Bool("dual_write", dualWrite),
			zap.Bool("resume", resumeCheckpoints),
			zap.Bool("cutover", cutover),
			zap.String("migration", "296_run_artifact_checkpoints"))
	}
	return nil
}

// wireScriptGenerationSemanticAnalyzer wires the ONE semantic engine every
// script path uses (batch analysis, phrase alignment, localized identity
// matching, overlay plan compilation) and the location sources that ride it.
func wireScriptGenerationSemanticAnalyzer(runner *scriptgen.Runner, cfg *config.Config, log *zap.Logger) (*rustexec.SemanticEngineAdapter, error) {
	phraseEmbedder := scriptwiring.WirePhraseImpactEmbedder(cfg)
	// Unified semantic path: ONE analyze call per script returns entities,
	// bullets, charts, dates and numbers from the same extraction. The worker
	// is a dedicated binary (bin/semantic-engine), not the media executor and
	// not the per-category VisualNER entry points.
	semanticExecutor := rustexec.NewSemanticEngineExecutor(cfg.External.RustSemanticEnginePath, cfg.External.FfmpegPath, log)
	semanticAnalyzer, err := rustexec.NewSemanticEngineAdapter(semanticExecutor)
	if err != nil {
		return nil, fmt.Errorf("build semantic engine adapter: %w", err)
	}
	semanticAnalyzer.SetPassageEmbedder(phraseEmbedder)
	runner.SetSceneSemanticAnalyzer(semanticAnalyzer)
	runner.SetPhraseAligner(semanticAnalyzer)
	runner.SetLocalizedIdentityMatcher(semanticAnalyzer)
	runner.SetOverlayPlanCompiler(semanticAnalyzer)
	// Location overlays use the real Nominatim adapter and the only accepted
	// basemap source: operator-certified local plates. The capability layer
	// still gates all lookups and map planning on request-level geocoding opt-in.
	if err := scriptwiring.WireScriptLocationSources(runner, cfg, log); err != nil {
		return nil, err
	}
	return semanticAnalyzer, nil
}

// wireScriptGenerationAudioAssets binds the combined audio renderer, the final
// audio publisher and the cross-run master cache.
func wireScriptGenerationAudioAssets(runner *scriptgen.Runner, cfg *config.Config, root *ComposeRoot, committer assetspersistence.AssetCommitter, audioRenderer *rustexec.CombinedAudioRenderer, log *zap.Logger) error {
	runner.SetCombinedAudioRenderer(audioRenderer)
	var finalAudioDB *sql.DB
	var finalAudioDrivePublisher delivery.Publisher
	var finalAudioDriveGate *concurrent.FairSemaphore
	if root != nil {
		if root.DB != nil && root.DB.DB != nil {
			finalAudioDB = root.DB.DB
			if root.MediaPostgres != nil {
				finalAudioDB = root.MediaPostgres
			}
		}
		if root.Drive != nil {
			finalAudioDrivePublisher = root.Drive.Publisher
		}
		finalAudioDriveGate = root.DriveUploadGate
	}
	runner.SetFinalAudioPublisher(vowiring.NewFinalAudioPublisher(finalAudioDB, finalAudioDrivePublisher, committer, finalAudioDriveGate, log))
	if cfg != nil {
		// Cross-run reuse of a certified master: the audio plan hash is the work
		// identity, so an unchanged plan must not be re-mixed and re-encoded.
		audioCacheDir := finalAudioCacheDir(cfg.Storage.DataDir)
		runner.SetFinalAudioCacheDir(audioCacheDir)
		if audioCacheDir != "" {
			log.Info("final-audio plan cache configured", zap.String("cache_dir", audioCacheDir))
		}
	}
	return nil
}

// wireScriptGenerationCheckpointResolver wires durable per-unit checkpoint
// resume against the artifact verifier derived from the canonical committer.
func wireScriptGenerationCheckpointResolver(runner *scriptgen.Runner, root *ComposeRoot, committer assetspersistence.AssetCommitter, log *zap.Logger) {
	// Durable per-unit checkpoint resume (the audio unit today). The resolver
	// gates reuse on (input fingerprint + recorded artifact still present +
	// processor version), so a crash-restart SKIPs a certified audio render
	// instead of recomputing it. Nil-safe: an unwired resolver keeps the
	// legacy best-effort path, and a nil artifact verifier blocks SKIP only
	// for units that recorded an artifact — never an unverified reuse.
	// The artifact verifier reads the SAME engine that published the
	// artifact (the canonical media SSOT), derived from the committer.
	if root.DB != nil && root.DB.DB != nil {
		if cpStore, cpErr := sqlitecheckpoint.New(root.DB.DB); cpErr != nil {
			log.Warn("durable checkpoint resolver NOT wired (checkpoint store unavailable)", zap.Error(cpErr))
		} else {
			runner.SetCheckpointResolver(capcheckpoint.NewResolver(cpStore, mediaCheckpointArtifactVerifier(committer, log)))
			log.Info("durable per-unit checkpoint resume wired (audio unit; media-SSOT artifact verification)")
		}
	} else {
		log.Warn("durable checkpoint resolver NOT wired (primary SQLite unavailable)")
	}
}

// wireScriptGenerationMediaPreflight wires the media preflight (provider
// availability is part of the contract even when the asset resolver is absent)
// and, when the registry exists, the canonical audio/background asset source.
func wireScriptGenerationMediaPreflight(runner *scriptgen.Runner, cfg *config.Config, root *ComposeRoot, vidRushProviders *documentadapters.VidRushAssetProviderRegistry, log *zap.Logger) error {
	// MEDIA-SSOT: the BGM/SFX/overlay resolver and the media preflight read the
	// PostgreSQL media SSOT, and the engine selection is owned by the canonical
	// selector. Repeating the branch here made a SECOND engine decision point —
	// the thing RequireMediaPostgres exists to prevent — and kept the legacy
	// SQLite detail.Service reachable as a media read, so a clip committed by
	// the canonical committer could be reported as missing by the preflight
	// while the worker asset-transfer path (which uses the same selector) saw it.
	assetLookup := mediasub.AssetDetailsLookup(root.MediaAssetDetailsLookup())
	// Provider availability is part of the preflight contract even when the
	// media asset resolver is unavailable. Keep the preflight wired in that
	// degraded composition so an enabled but unregistered provider fails before
	// scene-text generation instead of falling through to late materialization.
	var imageProviderHealth scriptgen.ImageProviderHealthProbe
	if root.Domains != nil && root.Domains.ImageService != nil {
		imageProviderHealth = mediasub.NewImageProviderHealthProbe(root.Domains.ImageService)
	}
	var providerAvailability scriptgen.VidRushProviderAvailabilityProbe
	if vidRushProviders != nil {
		providerAvailability = mediasub.NewVidRushProviderAvailabilityProbe(vidRushProviders)
	}
	runner.SetMediaPreflight(mediasub.NewPreflightWithProviderAvailability(assetLookup, nil, nil, imageProviderHealth, providerAvailability))
	if assetLookup != nil {
		var driveReader drive.Reader
		if root.Drive != nil {
			driveReader = root.Drive.Reader
		}
		scratchDir := filepath.Join(cfg.Storage.TempPath(), "audioassets")
		canonical, matErr := drive.NewCanonicalAssetMaterializer(driveReader, scratchDir, log)
		if matErr != nil {
			return fmt.Errorf("audio asset resolver: wire canonical materializer: %w", matErr)
		}
		audioAdapter := wiringaudio.NewAudioAssetSourceAdapter(assetLookup, canonical)
		runner.SetAudioAssetSource(audioAdapter)
		runner.SetOverlayBackgroundSource(audioAdapter)
		// Add canonical audio resolvers to the already-wired preflight without
		// changing its unconditional provider-availability check.
		runner.SetMediaPreflight(mediasub.NewPreflightWithProviderAvailability(assetLookup, audioAdapter, audioAdapter, imageProviderHealth, providerAvailability))
		log.Info("audio asset resolver wired (BGM/SFX asset_id → local path) including P0.5 media preflight adapter")
	} else {
		log.Warn("audio asset resolver not wired: asset registry missing (BGM/SFX intents will fail closed)")
	}
	return nil
}

// wireScriptGenerationPersistence binds the run repository and the execution
// recorder. The Job Registry is REQUIRED: without it a run has no lineage, so
// its absence fails the boot instead of producing unattributable runs.
func wireScriptGenerationPersistence(runner *scriptgen.Runner, root *ComposeRoot, log *zap.Logger) error {
	if root.Repos != nil && root.Repos.ScriptsRepo != nil {
		runner.SetScriptPersistence(scriptwiring.NewGenerationPersistence(
			sqlitescripts.NewRepositoryAdapter(root.Repos.ScriptsRepo), log,
		))
	} else {
		log.Warn("script generation SQLite persistence is not wired")
	}
	if root.Jobs != nil && root.Jobs.JobLedger != nil {
		runner.SetExecutionRecorder(scriptjobs.NewJobRegistryExecutionRecorder(root.Jobs.JobLedger))
		log.Info("script generation execution recorder wired to Job Registry")
	} else {
		return fmt.Errorf("build script generation runtime: Job Registry is required for execution lineage")
	}
	return nil
}

// wireScriptGenerationConcurrency resolves and applies the per-stage
// concurrency budgets, including the TTS ceiling warning.
func wireScriptGenerationConcurrency(runner *scriptgen.Runner, cfg *config.Config, root *ComposeRoot, log *zap.Logger) {
	nlpConcurrency := cfg.Scripts.NLPConcurrency
	if nlpConcurrency <= 0 {
		nlpConcurrency = scriptgen.DefaultNLPConcurrency
	}
	scriptGenerationConcurrency := cfg.Scripts.ScriptGenerationConcurrency
	if scriptGenerationConcurrency <= 0 {
		scriptGenerationConcurrency = scriptgen.DefaultGenerationConcurrency
	}
	ollamaScriptGate := scriptgen.NewGenerationGateWithCapacity(scriptGenerationConcurrency)
	// Rust semantic extraction has its own bounded fan-out and does not contend
	// for the local Ollama model used by scene-text generation.
	runner.SetNLPConcurrency(nlpConcurrency)
	if root.AI != nil && root.AI.SceneTextGenerator != nil {
		root.AI.SceneTextGenerator.SetSegmentConcurrency(scriptGenerationConcurrency)
		root.AI.SceneTextGenerator.Engine.SetGenerationGate(ollamaScriptGate)
	}

	ttsConcurrency := cfg.Scripts.TTSConcurrency
	if ttsConcurrency <= 0 {
		ttsConcurrency = cfg.Voiceover.MaxConcurrentTTS
	}
	if ttsConcurrency <= 0 {
		ttsConcurrency = scriptgen.DefaultTTSConcurrency
	}
	// The provider semaphore is the hard ceiling on synthesis: a TTS pool wider
	// than it only over-schedules (the comment on scripts.tts_concurrency calls
	// this out), so surface the mismatch instead of letting the operator
	// believe the wider pool took effect.
	if ceiling := cfg.Voiceover.MaxConcurrentTTS; ceiling > 0 && ttsConcurrency > ceiling {
		log.Warn("script generation: tts_concurrency exceeds the voiceover provider ceiling; the effective synthesis pool is the provider bound",
			zap.Int("tts_concurrency", ttsConcurrency),
			zap.Int("voiceover_max_concurrent_tts", ceiling))
	}
	translationConcurrency := cfg.Scripts.TranslationConcurrency
	if translationConcurrency <= 0 {
		translationConcurrency = scriptgen.DefaultTranslationConcurrency
	}
	runner.SetTTSConcurrency(ttsConcurrency)
	runner.SetTranslationConcurrency(translationConcurrency)
	if root.Domains != nil && root.Domains.VoiceoverPublishPool != nil {
		runner.SetVoiceoverPublishDrainer(root.Domains.VoiceoverPublishPool)
	}
	log.Info("script generation incremental VidRush pipeline wired (extraction + provider fan-out overlap generation)",
		zap.Int("nlp_concurrency", nlpConcurrency),
		zap.Int("script_generation_concurrency", scriptGenerationConcurrency),
		zap.Int("tts_concurrency", ttsConcurrency),
		zap.Int("voiceover_max_concurrent_tts", cfg.Voiceover.MaxConcurrentTTS),
		zap.Int("translation_concurrency", translationConcurrency))
}

// mediaCheckpointArtifactVerifier resolves the durable checkpoint resume
// artifact port from the canonical committer's OWN engine.
//
// WHY THE COMMITTER AND NOT A SECOND HANDLE. The runner's audio unit records
// the published voiceover artifact in its checkpoint (ArtifactSHA256 =
// finalAudio.FinalAudioSHA256, runner_phase_audio.go), and
// final_audio_publisher.go registers that artifact through the canonical
// media writer into media_assets.content_sha256. The verifier must therefore
// read the same engine the write landed on — a verifier pointed at the
// operational SQLite mirror could only ever answer "missing" and would
// silently disable resume. This is the same derive-the-read-from-the-writer
// rule the other *FromCommitter helpers follow.
//
// nil means the media plane is closed (or the committer does not expose its
// engine). The resolver then degrades to EXECUTE for artifact-bearing units —
// never an unverified SKIP, and never a fabricated availability.
func mediaCheckpointArtifactVerifier(committer assetspersistence.AssetCommitter, log *zap.Logger) capcheckpoint.ArtifactVerifier {
	if committer == nil {
		return nil
	}
	getter, ok := committer.(interface{ DB() *sql.DB })
	if !ok || getter == nil {
		return nil
	}
	db := getter.DB()
	if db == nil {
		return nil
	}
	verifier, err := pgmedia.NewMediaArtifactVerifier(db)
	if err != nil {
		if log != nil {
			log.Warn("checkpoint artifact verifier NOT wired (media SSOT verifier unavailable)", zap.Error(err))
		}
		return nil
	}
	return verifier
}

// finalAudioCacheDir resolves where certified masters are reused between runs.
// It mirrors the fragment-cache boundary: "off" disables reuse (an A/B run
// needs a way to measure the cache instead of trusting it) and the default
// lives under the same durable data directory as the rest of the state.
func finalAudioCacheDir(dataDir string) string {
	raw := strings.TrimSpace(knobs.String("VELOX_FINAL_AUDIO_CACHE_DIR"))
	switch strings.ToLower(raw) {
	case "off", "disabled", "none":
		return ""
	case "":
		if strings.TrimSpace(dataDir) == "" {
			return ""
		}
		return filepath.Join(dataDir, "final-audio")
	default:
		return raw
	}
}

func wireRenderAttemptRecorder(db *sql.DB, log *zap.Logger) scriptgen.RenderAttemptRecorder {
	if db == nil {
		return nil
	}
	reg, err := rendermetrics.New(db)
	if err != nil {
		log.Warn("render attempt analytics recorder NOT wired (registry unavailable)", zap.Error(err))
		return nil
	}
	return reg
}

var _ scriptgen.Translator = (*scriptGenerationTranslator)(nil)

func wireScriptGenerationVidRush(runner *scriptgen.Runner, cfg *config.Config, root *ComposeRoot, log *zap.Logger, vidRushProviders *documentadapters.VidRushAssetProviderRegistry, vidRushFinalizer scriptports.VidRushArtifactFinalizer, vidRushCache scriptports.VidRushCachePort, mediaSampler *rustexec.MediaSamplerAdapter, semanticAnalyzer *rustexec.SemanticEngineAdapter) error {
	// SceneIR entity extraction and image search share the same semantic
	// engine as the script batch analyzer. Exact source matches reuse the
	// batch entity projection; translated/different text is analyzed by that
	// same engine on demand.
	semanticCandidateScorer := scriptVisualCandidateScorer(root)
	scriptwiring.WireSemanticSearchPlanner(runner, cfg, scriptCacheDB(root), vidrushInternetImageSearcher(root, log), semanticCandidateScorer, log)

	vidRushMetrics := observability.NewVidRushMetricsAdapter()
	var vidRushFanout scriptgen.SegmentProviderResolver
	if vidRushProviders != nil {
		var entityImageCatalogRepo entitycatalog.Repository
		if root.Repos != nil {
			entityImageCatalogRepo = root.Repos.EntityImageCatalog
		}
		registryMediaResolver := &documentadapters.VidRushRegistryMediaResolver{Registry: vidRushProviders}
		fanout := documentadapters.NewVidRushProviderFanoutWithResolver(
			registryMediaResolver,
			vidRushCache,
			entityImageCatalogRepo,
			vidRushMetrics,
		).WithLogger(log)
		// Opt-in media_manifest fast-path on the fan-out (the discovery owner
		// for plans without the internet_images postprocessor). Fail-open: a
		// broken manifest logs a warning and never fails a run.
		if pins, pinsErr := documentadapters.LoadMediaManifestPinsFromEnv(); pinsErr != nil {
			log.Warn("VidRush image fanout media_manifest fast-path disabled: manifest load failed", zap.String("error", pinsErr.Error()))
		} else if pins != nil {
			fanout.WithMediaManifestPins(pins)
			log.Info("VidRush image fanout media_manifest fast-path enabled", zap.Int("pinned_entries", pins.EntryCount()))
		}
		if semanticCandidateScorer != nil {
			fanout.WithSemanticScorer(semanticCandidateScorer)
			log.Info("VidRush image fanout and semantic planner wired with visual identity verification")
		}
		vidRushFanout = fanout
	}
	var vidRushMaterializer scriptgen.SegmentMaterializer
	if vidRushProviders != nil && vidRushFinalizer != nil {
		var entityImageCatalogRepo entitycatalog.Repository
		if root.Repos != nil {
			entityImageCatalogRepo = root.Repos.EntityImageCatalog
		}
		vidRushMaterializer = documentadapters.NewVidRushMaterializationProcessorWithCatalog(vidRushProviders, vidRushFinalizer, vidRushCache, entityImageCatalogRepo, vidRushMetrics).WithMediaSampler(mediaSampler).WithMediaSamplerGroup(mediaSampler).WithLogger(log)
	}
	pipeline := &scriptgen.VidRushPipeline{
		ProviderResolver: vidRushFanout,
		Materializer:     vidRushMaterializer,
		Metrics:          vidRushMetrics,
		PlanResolver: scriptgen.VidRushPlanResolverFunc(func(ctx context.Context, req scriptgen.GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			plan, err := root.AI.SceneTextGenerator.ResolveVidRushPlan(ctx, req)
			return plan, err
		}),
		Backpressure:   scriptgen.DefaultVidRushBackpressure(),
		NERPort:        semanticAnalyzer,
		PhraseSelector: semanticAnalyzer,
		SamplerPort:    mediaSampler,
		CertifierPort: scriptgen.MediaCertifierFunc(func(_ context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error) {
			return mediacert.Certify(spec, result), nil
		}),
		CertSpecResolver: scriptgen.MediaCertSpecResolverFunc(scriptwiring.BuildRuntimeMediaCertSpec),
	}
	if root.MediaPostgres != nil && strings.TrimSpace(cfg.ClipIndexer.ServerURL) != "" && vidRushProviders != nil {
		searcher := pgmedia.NewMediaSearcher(root.MediaPostgres)
		embedder := stockintelligence.NewHTTPTextEmbedderAdapter(func(ctx context.Context, text string) ([]float32, error) {
			res, err := embeddings.NewHTTPTextEmbedderWithTimeout(cfg.ClipIndexer.ServerURL, cfg.ClipIndexer.EmbedTimeout()).Embed(ctx, text)
			if err != nil {
				return nil, err
			}
			return res.Vector, nil
		})
		local := stockintelligence.PostgresLocalSearchAdapter{Searcher: searcher, Embedder: embedder}
		hydrator := stockintelligence.PostgresAssetHydrator{Searcher: searcher}
		provider := stockintelligence.RegistryProviderClient{Registry: vidRushProviders}
		sampler := func(candidates []stockintelligence.Candidate, segmentID, subject string, terms []string) (string, error) {
			converted := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
			for _, candidate := range candidates {
				converted = append(converted, scriptpkg.SegmentAssetCandidate{AssetID: candidate.AssetID, Entity: candidate.Label, RelevanceScore: float64(candidate.GenericSimilarity), SegmentID: candidate.OwnerSegmentID})
			}
			return mediaSampler.Sample(context.Background(), segmentID, subject, terms, converted, false)
		}
		if resolver, resolverErr := stockintelligence.NewResolver(local, hydrator, provider, sampler); resolverErr == nil {
			// Content-addressed resolve replay cache (L1 LRU + durable L2):
			// repeated entity/query discovery — across segments and across
			// runs — is served without re-embedding, re-searching or hitting
			// the provider live path. The sampler stays per-request.
			resolver.WithCache(documentadapters.NewStockResolveCache(vidRushCache))
			if service, serviceErr := stockintelligence.NewService(resolver); serviceErr == nil {
				pipeline.StockResolverPort = service
			}
		}
	}
	runner.SetVidRushPipeline(pipeline)
	return nil
}

func scriptCacheDB(root *ComposeRoot) *sql.DB {
	if root == nil || root.CacheDB == nil {
		return nil
	}
	return root.CacheDB.DB
}

// SceneTextGenerator remains a composition-root facade over the script
// subsystem implementation.
type SceneTextGenerator = scriptwiring.SceneTextGenerator

type ClipAssetResolver = scriptwiring.ClipAssetResolver
type ClipProber = scriptwiring.ClipProber
