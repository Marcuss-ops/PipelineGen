// Package wiring — script_generation_wiring.go: the individual adapters that
// BuildScriptGenerationRuntime hangs on the runner (checkpoint rollout,
// semantic analyzer, audio assets, checkpoint resolver, media preflight,
// persistence, concurrency).
//
// Split 2026-11-20 from script_generation_runtime.go (BONIFICA-04): the blocks
// are moved verbatim, no behaviour change.
package wiring

import (
	"database/sql"
	"fmt"
	"path/filepath"

	wiringaudio "github.com/Marcuss-ops/PipelineGen/internal/app/modules/audio"
	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/modules/media"
	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/script"
	vowiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/voiceover"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	sqlitecheckpoint "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/checkpoint"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

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
	runner.SetFinalAudioPublisher(vowiring.NewFinalAudioPublisher(vowiring.FinalAudioPublisherDeps{
		DB:        finalAudioDB,
		Publisher: finalAudioDrivePublisher,
		Committer: committer,
		Gate:      finalAudioDriveGate,
		Log:       log,
	}))
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
	runner.SetMediaPreflight(mediasub.NewPreflight(mediasub.PreflightDeps{
		Assets:               assetLookup,
		ImageProviderHealth:  imageProviderHealth,
		ProviderAvailability: providerAvailability,
	}))
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
		// The indexed SFX library (media SSOT) feeds the clip-start/overlay
		// selection pool. Without it the selector keeps the built-in whoosh
		// aliases, so an unindexed deployment never fails here.
		var sfxPool wiringaudio.SoundEffectCandidateLister
		if root.MediaPostgres != nil {
			sfxPool = wiringaudio.NewPostgresSoundEffectPool(root.MediaPostgres)
		}
		audioAdapter := wiringaudio.NewAudioAssetSourceAdapter(assetLookup, canonical, sfxPool)
		runner.SetAudioAssetSource(audioAdapter)
		runner.SetOverlayBackgroundSource(audioAdapter)
		// Add canonical audio resolvers to the already-wired preflight without
		// changing its unconditional provider-availability check.
		runner.SetMediaPreflight(mediasub.NewPreflight(mediasub.PreflightDeps{
			Assets:               assetLookup,
			AudioAssetSource:     audioAdapter,
			ClipAudioAssetSource: audioAdapter,
			ImageProviderHealth:  imageProviderHealth,
			ProviderAvailability: providerAvailability,
		}))
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
