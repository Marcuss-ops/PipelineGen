package wiring

import (
	"context"
	"fmt"
	"os"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	"go.uber.org/zap"
)

// defaultRenderingGenStoreURL is the fallback object-store endpoint used when
// the loaded config carries none (the local single-host deployment default).
// Owning it once keeps the overlay-asset prefetch, the overlay clip fetcher
// and the Chronon timing sidecar fetch pointed at the same store.
const defaultRenderingGenStoreURL = config.LoopbackURLPrefix + "9000"

// renderingGenStoreURL returns the object-store endpoint the config loader
// resolved once at startup (external.renderinggen_store_url, overridden by
// RENDERINGGEN_STORE_URL). The environment is never re-read here: the constant
// only guards a Config assembled without the loader.
func renderingGenStoreURL(cfg *config.Config) string {
	if cfg != nil {
		if store := strings.TrimSpace(cfg.External.RenderingGenStoreURL); store != "" {
			return store
		}
	}
	return defaultRenderingGenStoreURL
}

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

// TranslateBatch is the batched variant of Translate (WST-04): one provider
// request per scene chunk instead of one per scene. It is OPTIONAL, exactly
// like the application port it adapts — a chain without the batched capability
// reports the missing capability and the runner keeps the per-scene fan-out.
func (a *scriptGenerationTranslator) TranslateBatch(ctx context.Context, input scriptgen.TranslationBatchInput) ([]string, error) {
	if a == nil || a.port == nil {
		return nil, fmt.Errorf("script generation translator is not configured")
	}
	port := a.port
	if strings.EqualFold(strings.TrimSpace(input.Provider), "ollama") {
		port = a.ollama
		if port == nil {
			return nil, fmt.Errorf("script generation Ollama translator is not configured")
		}
	}
	if port == nil {
		return nil, fmt.Errorf("script generation translator provider %q is not configured", input.Provider)
	}
	batcher, ok := port.(translation.BatchTranslationPort)
	if !ok {
		return nil, fmt.Errorf("script generation translator provider %q does not support batched translation", input.Provider)
	}
	segments := make([]translation.BatchTranslationSegment, 0, len(input.Items))
	for _, item := range input.Items {
		segments = append(segments, translation.BatchTranslationSegment{ID: item.SceneID, Text: item.Text})
	}
	command := translation.BatchTranslationCommand{
		SourceLang: string(input.SourceLanguage),
		TargetLang: string(input.TargetLanguage),
		Segments:   segments,
		// The runner already sized the chunk; the provider must not split it
		// again or the 1:1 correlation with the scene ids would be lost.
		ChunkSize: len(segments),
	}
	if input.Model != "" || input.Provider != "" {
		command.ModelPolicy = &translation.ModelPolicy{Provider: input.Provider, Model: input.Model}
	}
	result, err := batcher.TranslateBatch(ctx, command)
	if err != nil {
		return nil, err
	}
	if len(result.Segments) != len(input.Items) {
		return nil, fmt.Errorf("batched translation returned %d segments for %d scenes", len(result.Segments), len(input.Items))
	}
	out := make([]string, len(result.Segments))
	for i, segment := range result.Segments {
		if segment.ID != input.Items[i].SceneID {
			return nil, fmt.Errorf("batched translation misaligned at %d: got %q want %q", i, segment.ID, input.Items[i].SceneID)
		}
		if strings.TrimSpace(segment.Text) == "" {
			return nil, fmt.Errorf("batched translation returned empty text for scene %q", segment.ID)
		}
		out[i] = segment.Text
	}
	return out, nil
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
	// The local join is a delivery artifact, not a renderer experiment: pin the
	// certified lane instead of inheriting a user-facing NVENC policy. The
	// assembler rejects any other policy anyway; wiring the right one here keeps
	// overlays-only jobs working on a GPU-configured host.
	runner.SetOverlayJoinEncoder(mediaexec.EncoderPolicy{
		Codec:  kernelmedia.CanonicalVideoCodec,
		Preset: kernelmedia.CanonicalVideoPreset,
		CRF:    kernelmedia.CanonicalVideoCRF,
	})
	// The local render lane produces one certified clip per overlay item; the
	// join needs those exact bytes back from the object store to place them on
	// the plan timeline. Wiring the fetcher is what turns a render-enabled job
	// into a job that owns its own MP4 instead of shipping audio only.
	runner.SetOverlayClipFetcher(renderinggen.NewOverlayClipFetcher(renderingGenStoreURL(cfg)))
	if err := wireArtifactCheckpointRollout(runner, root, committer, log); err != nil {
		return nil, err
	}
	semanticAnalyzer, err := wireScriptGenerationSemanticAnalyzer(runner, cfg, log)
	if err != nil {
		return nil, err
	}
	// TTSConcurrency is owned by script_generation_wiring.go (config field,
	// voiceover ceiling, then default). LocalizedRenderConcurrency governs the
	// parallel render fan-out, NOT the TTS pool: overriding one with the other
	// silently discarded VELOX_SCRIPTS_TTS_CONCURRENCY and made larger render
	// concurrency worsen TTS wall time (CR-15).
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
