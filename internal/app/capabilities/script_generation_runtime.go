package capabilities

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/adapters"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
	"go.uber.org/zap"
)

// scriptGenerationTranslator adapts the application translation port to the
// capability runtime without leaking provider DTOs into capabilities/scripts.
type scriptGenerationTranslator struct {
	port translation.TranslationPort
}

func (a *scriptGenerationTranslator) Translate(ctx context.Context, input scriptgen.TranslationInput) (string, error) {
	if a == nil || a.port == nil {
		return "", fmt.Errorf("script generation translator is not configured")
	}
	result, err := a.port.Translate(ctx, translation.TranslationCommand{
		SourceLang: string(input.SourceLanguage),
		TargetLang: string(input.TargetLanguage),
		Text:       input.SourceText,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.TranslatedText) == "" {
		return "", fmt.Errorf("script generation translator returned empty text")
	}
	return result.TranslatedText, nil
}

// scriptGenerationDocumentPublisher adapts Drive's idempotent document
// contract to the durable generation capability. Drive remains the concrete
// provider; the capability sees only its typed port.
type scriptGenerationDocumentPublisher struct {
	client drive.DocClient
}

func (a *scriptGenerationDocumentPublisher) Preflight(_ context.Context, folderID string) error {
	if a == nil || a.client == nil {
		return scriptgen.ErrGoogleDocsUnavailable
	}
	if strings.TrimSpace(folderID) == "" {
		return scriptgen.ErrGoogleDocsUnavailable
	}
	return nil
}

// scriptGenerationDocumentRenderer is the composition-root adapter from the
// capability renderer port to the canonical application renderer. The
// capability runner never owns HTML formatting or a second document builder.
type scriptGenerationDocumentRenderer struct{}

// finalAudioArtifactForDocument converts the capability-owned master
// reference into the domain artifact consumed by the document renderer. It
// never invents fields: unavailable values remain their zero value and are
// omitted from the projected JSON.
func finalAudioArtifactForDocument(ref *scriptgen.FinalAudioReference) *scriptpkg.FinalAudioArtifact {
	if ref == nil {
		return nil
	}
	return &scriptpkg.FinalAudioArtifact{
		AssetID:              ref.AssetID,
		Path:                 ref.Path,
		DriveLink:            ref.DriveLink,
		Container:            ref.Container,
		AudioContractVersion: ref.AudioContractVersion,
		AudioPlanVersion:     ref.AudioPlanVersion,
		AudioPlanSHA256:      ref.PlanSHA256,
		FinalAudioSHA256:     ref.FinalAudioSHA256,
		Codec:                ref.Codec,
		Profile:              ref.Profile,
		SampleRate:           ref.SampleRate,
		Channels:             ref.Channels,
		ChannelLayout:        ref.ChannelLayout,
		Bitrate:              ref.Bitrate,
		DurationUS:           ref.DurationUS,
		DurationMS:           ref.DurationMS,
		StartPTS:             ref.StartPTS,
		SizeBytes:            ref.SizeBytes,
		FinalMix:             ref.FinalMix,
		CopyEligible:         ref.CopyEligible,
	}
}

func (scriptGenerationDocumentRenderer) DocumentRendererID() string {
	return documentadapters.CanonicalDocumentRendererID
}

func (scriptGenerationDocumentRenderer) RenderDocument(model *scriptpkg.ModelScriptOutputV1, opts scriptgen.DocumentRenderOptions) (string, error) {
	return documentadapters.BuildSpecSceneDocumentHTML(model, documentadapters.SpecSceneDocumentOptions{
		Title:              opts.Title,
		Language:           string(opts.Language),
		DefaultLanguage:    string(opts.DefaultLanguage),
		FullAudio:          opts.FullAudio,
		FinalAudio:         finalAudioArtifactForDocument(opts.FinalAudio),
		AudioTimeline:      opts.AudioTimeline,
		SceneSpeechTimings: opts.SceneSpeechTimings,
		ClipMetadata:       opts.ClipMetadata,
		AudioSummary:       opts.AudioSummary,
		Overlay:            opts.Overlay,
	}), nil
}

func (a *scriptGenerationDocumentPublisher) UpsertDocument(ctx context.Context, input scriptgen.DocumentInput) (scriptgen.DocumentReference, error) {
	if a == nil || a.client == nil {
		return scriptgen.DocumentReference{}, fmt.Errorf("script generation document publisher is not configured")
	}
	key := strings.TrimSpace(input.RunID) + ":" + strings.TrimSpace(string(input.Language))
	doc, err := a.client.CreateDocIdempotent(ctx, input.Title, input.Content, input.FolderID, key, false)
	if err != nil {
		return scriptgen.DocumentReference{}, err
	}
	if doc == nil || strings.TrimSpace(doc.ID) == "" || strings.TrimSpace(doc.URL) == "" {
		return scriptgen.DocumentReference{}, fmt.Errorf("drive document publisher returned incomplete reference")
	}
	return scriptgen.DocumentReference{ID: doc.ID, Link: doc.URL}, nil
}

// buildScriptGenerationRuntime creates the worker-owned durable runtime. The
// HTTP starter only creates/correlates the run; this runtime is invoked by the
// script.generate worker after the submission transaction has committed.
func BuildScriptGenerationRuntime(cfg *config.Config, root *wiring.ComposeRoot, runRepo scriptgen.RunRepository, committer assetspersistence.AssetCommitter, log *zap.Logger, vidRushProviders *documentadapters.VidRushAssetProviderRegistry, vidRushFinalizer scriptports.VidRushArtifactFinalizer, vidRushCache scriptports.VidRushCachePort) (*scriptgen.Runner, error) {
	if cfg == nil || root == nil || runRepo == nil {
		return nil, fmt.Errorf("script generation runtime requires config, composition root, and run repository")
	}
	if root.AI == nil || root.AI.SceneTextGenerator == nil || root.AI.OllamaTranslator == nil {
		return nil, fmt.Errorf("script generation runtime requires scene text and translation adapters")
	}
	if strings.TrimSpace(cfg.External.RustMusclesPath) == "" {
		return nil, fmt.Errorf("script generation runtime requires Rust media executor path")
	}

	executor := rustexec.NewExecutor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	videoProcessor := rustexec.NewConfiguredVideoProcessorWithExecutor(executor, root.MediaExec.Policy, root.MediaExec.Profile, log)
	audioRenderer, err := rustexec.NewCombinedAudioRenderer(videoProcessor)
	if err != nil {
		return nil, fmt.Errorf("build combined audio renderer: %w", err)
	}
	var docPublisher scriptgen.DocumentPublisher
	if root.Drive != nil && root.Drive.DocClient != nil {
		docPublisher = &scriptGenerationDocumentPublisher{client: root.Drive.DocClient}
	}
	runner := scriptgen.NewRunner(
		runRepo,
		root.AI.SceneTextGenerator,
		&scriptGenerationTranslator{port: root.AI.OllamaTranslator},
		root.AI.ScriptVoiceoverGenerator,
		docPublisher,
		scriptGenerationDocumentRenderer{},
	)
	runner.SetCombinedAudioRenderer(audioRenderer)
	runner.SetFinalAudioPublisher(newFinalAudioPublisher(root, committer, log))
	// Wire the canonical entity type→template registry so the
	// EntityOverlayPlanner runs in production: OverlayIntents (with their
	// resolved template_id) are created immediately after entity extraction
	// and persisted to the durable run payload before any render job is
	// enqueued.
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	// Wire the overlay.prepare job enqueuer: the pre-timing OverlayIntents
	// are persisted and then overlay.prepare starts in parallel with TTS.
	// When the RenderingGen queue URL is not configured, prepare is not
	// registered (a legitimate no-op); when configured, an enqueue error
	// fails the run fail-closed.
	if queueURL := strings.TrimSpace(cfg.External.RenderingGenQueueURL); queueURL != "" {
		prepareEnqueuer, err := scriptgen.NewQueuePrepareEnqueuer(renderinggen.New(queueURL))
		if err != nil {
			return nil, fmt.Errorf("build queue prepare enqueuer: %w", err)
		}
		runner.SetOverlayPrepareEnqueuer(prepareEnqueuer)
		renderEnqueuer, err := scriptgen.NewQueueRenderEnqueuer(renderinggen.New(queueURL))
		if err != nil {
			return nil, fmt.Errorf("build queue render enqueuer: %w", err)
		}
		runner.SetOverlayRenderEnqueuer(renderEnqueuer)
		log.Info("overlay.prepare enqueuer wired to RenderingGen queue", zap.String("url", queueURL))
	} else {
		log.Warn("overlay.prepare enqueue disabled: RENDERINGGEN_QUEUE_URL is not configured")
	}
	if root.Repos != nil && root.Repos.ScriptsRepo != nil {
		runner.SetScriptPersistence(newScriptGenerationPersistence(
			sqlitescripts.NewRepositoryAdapter(root.Repos.ScriptsRepo), log,
		))
	} else {
		log.Warn("script generation SQLite persistence is not wired")
	}
	runner.SetScriptDocsFolderID(cfg.Scripts.ScriptDocsFolderID)
	if root.Jobs != nil && root.Jobs.JobLedger != nil {
		runner.SetExecutionRecorder(scriptjobs.NewJobRegistryExecutionRecorder(root.Jobs.JobLedger))
		log.Info("script generation execution recorder wired to Job Registry")
	} else {
		return nil, fmt.Errorf("build script generation runtime: Job Registry is required for execution lineage")
	}

	// ── Incremental VidRush ────────────────────────────────────────────
	// Wire the run-scoped incremental coordinator so scene generation and
	// VidRush enrichment (entities → queries → provider fan-out) overlap in the
	// real flow instead of running as two sequential blocks. The Runner builds
	// a fresh coordinator per run from these immutable dependencies.
	var vidRushEntityExtractor documentadapters.EntityExtractor
	if root.AI != nil && root.AI.ScriptGen != nil && root.AI.ScriptGen.GetClient() != nil {
		primary := ollamaadapters.NewOllamaEntityExtractorAdapter(root.AI.ScriptGen.GetClient())
		// Keep the durable path source-grounded when the model returns an
		// empty/placeholder scene: the deterministic CPU extractor is the
		// fail-safe semantic fallback, never a fabricated success.
		vidRushEntityExtractor = documentadapters.NewFallbackEntityExtractor(primary, localnlp.NewHybridExtractor())
	} else {
		vidRushEntityExtractor = localnlp.NewHybridExtractor()
	}
	vidRushMetrics := observability.NewVidRushMetricsAdapter()
	vidRushEnricher := documentadapters.NewVidRushSegmentEnricher(vidRushEntityExtractor, vidRushCache, vidRushMetrics)
	var vidRushFanout scriptgen.SegmentProviderResolver
	if vidRushProviders != nil {
		vidRushFanout = documentadapters.NewVidRushProviderFanoutWithCache(
			&documentadapters.VidRushRegistryClipSearcher{Registry: vidRushProviders},
			&documentadapters.VidRushRegistryImageSearcher{Registry: vidRushProviders},
			vidRushCache,
			vidRushMetrics,
		)
	}
	// The materialization stage (acquire → verify → finalize) is wired through
	// the same processor the batch flow registers, so the incremental
	// coordinator reuses it under its own bounded materialization limit.
	var vidRushMaterializer scriptgen.SegmentMaterializer
	if vidRushProviders != nil && vidRushFinalizer != nil {
		vidRushMaterializer = documentadapters.NewVidRushMaterializationProcessorWithCache(vidRushProviders, vidRushFinalizer, vidRushCache, vidRushMetrics)
	}
	runner.SetVidRushPipeline(&scriptgen.VidRushPipeline{
		Enricher:         vidRushEnricher,
		ProviderResolver: vidRushFanout,
		Materializer:     vidRushMaterializer,
		Metrics:          vidRushMetrics,
		PlanResolver: scriptgen.VidRushPlanResolverFunc(func(ctx context.Context, req scriptgen.GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return root.AI.SceneTextGenerator.ResolveVidRushPlan(ctx, req)
		}),
		Backpressure: scriptgen.DefaultVidRushBackpressure(),
	})
	// Shared worker pools: the generation gate capacity matches the certified
	// NLP concurrency (VidRush extraction fans out to at most 4 concurrent
	// scenes), and the TTS voiceover pool defaults to 4 with the voiceover
	// MaxConcurrentTTS config as the operator override (so the runner pool
	// stays in sync with the lower-level provider bound). Docs publishing and
	// the Rust final-audio render remain single-threaded.
	runner.SetGenerationGate(scriptgen.NewGenerationGateWithCapacity(scriptgen.DefaultNLPConcurrency))
	ttsConcurrency := scriptgen.DefaultTTSConcurrency
	if cfg.Voiceover.MaxConcurrentTTS > 0 {
		ttsConcurrency = cfg.Voiceover.MaxConcurrentTTS
	}
	runner.SetTTSConcurrency(ttsConcurrency)
	log.Info("script generation incremental VidRush pipeline wired (extraction + provider fan-out overlap generation)",
		zap.Int("nlp_concurrency", scriptgen.DefaultNLPConcurrency),
		zap.Int("tts_concurrency", ttsConcurrency))

	return runner, nil
}

var _ scriptgen.Translator = (*scriptGenerationTranslator)(nil)
var _ scriptgen.DocumentPublisher = (*scriptGenerationDocumentPublisher)(nil)
var _ scriptgen.DocumentPublisherPreflight = (*scriptGenerationDocumentPublisher)(nil)
