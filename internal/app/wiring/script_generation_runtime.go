package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	capabilityimagesearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/imagesearch"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
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
// capability renderer port to the canonical capability renderer. The
// capability runner owns the HTML formatting; this adapter only binds the
// port so the runner cannot tell the difference between a stub and the
// canonical renderer.
type scriptGenerationDocumentRenderer struct{}

func (scriptGenerationDocumentRenderer) DocumentRendererID() string {
	return scriptgen.CanonicalDocumentRendererID
}

func (scriptGenerationDocumentRenderer) RenderDocument(model *scriptpkg.ModelScriptOutputV1, opts scriptgen.DocumentRenderOptions) (string, error) {
	return scriptgen.RenderDocument(model, opts)
}

func (scriptGenerationDocumentRenderer) RenderDocumentSkeleton(in scriptgen.DocumentSkeletonInput) string {
	return scriptgen.RenderDocumentSkeleton(in)
}

func (scriptGenerationDocumentRenderer) InjectDocumentLateBound(skeleton string, model *scriptpkg.ModelScriptOutputV1, opts scriptgen.DocumentRenderOptions) string {
	return scriptgen.InjectDocumentLateBound(skeleton, model, opts)
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

func BuildScriptGenerationRuntime(cfg *config.Config, root *ComposeRoot, runRepo scriptgen.RunRepository, committer assetspersistence.AssetCommitter, log *zap.Logger, vidRushProviders *documentadapters.VidRushAssetProviderRegistry, vidRushFinalizer scriptports.VidRushArtifactFinalizer, vidRushCache scriptports.VidRushCachePort) (*scriptgen.Runner, error) {
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
	visualNERExecutor := rustexec.NewExecutor(cfg.External.RustVisualNERPath, cfg.External.FfmpegPath, log)
	mediaSamplerExecutor := rustexec.NewExecutor(cfg.External.RustMediaSamplerPath, cfg.External.FfmpegPath, log)
	visualNER, err := rustexec.NewVisualNERAdapter(visualNERExecutor)
	if err != nil {
		return nil, fmt.Errorf("build VisualNER adapter: %w", err)
	}
	mediaSampler, err := rustexec.NewMediaSamplerAdapter(mediaSamplerExecutor)
	if err != nil {
		return nil, fmt.Errorf("build MediaSampler adapter: %w", err)
	}
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
	if cfg != nil && cfg.Scripts.LocalizedRenderConcurrency > 0 {
		runner.SetTTSConcurrency(cfg.Scripts.LocalizedRenderConcurrency)
	}
	runner.SetCombinedAudioRenderer(audioRenderer)
	runner.SetFinalAudioPublisher(newFinalAudioPublisher(root, committer, log))
	if root.Repos != nil && root.Repos.Assets != nil {
		var driveReader drive.Reader
		if root.Drive != nil {
			driveReader = root.Drive.Reader
		}
		scratchDir := filepath.Join(cfg.Storage.TempPath(), "audioassets")
		canonical, matErr := drive.NewCanonicalAssetMaterializer(driveReader, scratchDir, log)
		if matErr != nil {
			return nil, fmt.Errorf("audio asset resolver: wire canonical materializer: %w", matErr)
		}
		audioAdapter := &audioAssetSourceAdapter{
			assets:    root.Repos.Assets,
			canonical: canonical,
		}
		runner.SetAudioAssetSource(audioAdapter)
		runner.SetOverlayBackgroundSource(audioAdapter)
		runner.SetMediaPreflight(mediasub.NewPreflight(root.Repos.Assets, audioAdapter, audioAdapter))
		log.Info("audio asset resolver wired (BGM/SFX asset_id → local path) including P0.5 media preflight adapter")
	} else {
		log.Warn("audio asset resolver not wired: asset registry missing (BGM/SFX intents will fail closed)")
	}
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	if queueURL := strings.TrimSpace(cfg.External.RenderingGenQueueURL); queueURL != "" {
		queueClient := renderinggen.New(queueURL)
		storeURL := strings.TrimSpace(os.Getenv("RENDERINGGEN_STORE_URL"))
		if storeURL == "" {
			storeURL = "http://127.0.0.1:9000"
		}
		queueClient.SetAssetPrefetcher(renderinggen.NewHTTPAssetPrefetcher(storeURL))
		prepareEnqueuer, err := scriptgen.NewQueuePrepareEnqueuer(queueClient)
		if err != nil {
			return nil, fmt.Errorf("build queue prepare enqueuer: %w", err)
		}
		runner.SetOverlayPrepareEnqueuer(prepareEnqueuer)
		renderEnqueuer, err := scriptgen.NewQueueRenderEnqueuer(queueClient)
		if err != nil {
			return nil, fmt.Errorf("build queue render enqueuer: %w", err)
		}
		if cfg.External.RenderingGenPollIntervalMS > 0 {
			renderEnqueuer.SetPollInterval(time.Duration(cfg.External.RenderingGenPollIntervalMS) * time.Millisecond)
		}
		// Reuse only input assets (entity images, backgrounds and fonts). Every
		// overlay video gets a fresh queue identity so RenderingGen/Chronon is
		// invoked for each new artifact, including repeated test runs.
		renderEnqueuer.SetFreshRender(true)
		if root.Drive != nil && root.Drive.Publisher != nil {
			drivePublisher := drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log)
			renderEnqueuer.SetArtifactPublisher(renderinggen.NewDriveOverlayArtifactPublisher(drivePublisher))
			log.Info("overlay artifact Drive publisher wired (script/language/overlay)")
		} else {
			log.Warn("overlay artifact Drive publisher disabled: Drive publisher is not configured")
		}
		var analyticsDB *sql.DB
		if root.DB != nil {
			analyticsDB = root.DB.DB
		}
		renderEnqueuer.SetRecorder(wireRenderAttemptRecorder(analyticsDB, log))
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

	var vidRushEntityExtractor entityports.EntityExtractor = visualNER
	imageSearchResolver := capabilityimagesearch.NewResolver(vidRushEntityExtractor)
	runner.SetImageSearchResolver(imageSearchResolver)
	log.Info("image search intent resolver wired (capabilities/imagesearch, deterministic path)")

	vidRushMetrics := observability.NewVidRushMetricsAdapter()
	var vidRushFanout scriptgen.SegmentProviderResolver
	if vidRushProviders != nil {
		var entityImageCatalogRepo entitycatalog.Repository
		if root.Repos != nil {
			entityImageCatalogRepo = root.Repos.EntityImageCatalog
		}
		registryMediaResolver := &documentadapters.VidRushRegistryMediaResolver{Registry: vidRushProviders}
		vidRushFanout = documentadapters.NewVidRushProviderFanoutWithResolver(
			registryMediaResolver,
			vidRushCache,
			entityImageCatalogRepo,
			vidRushMetrics,
		).WithLogger(log)
	}
	var vidRushMaterializer scriptgen.SegmentMaterializer
	if vidRushProviders != nil && vidRushFinalizer != nil {
		var entityImageCatalogRepo entitycatalog.Repository
		if root.Repos != nil {
			entityImageCatalogRepo = root.Repos.EntityImageCatalog
		}
		vidRushMaterializer = documentadapters.NewVidRushMaterializationProcessorWithCatalog(vidRushProviders, vidRushFinalizer, vidRushCache, entityImageCatalogRepo, vidRushMetrics).WithMediaSampler(mediaSampler).WithLogger(log)
	}
	pipeline := &scriptgen.VidRushPipeline{
		Enricher:         nil,
		ProviderResolver: vidRushFanout,
		Materializer:     vidRushMaterializer,
		Metrics:          vidRushMetrics,
		PlanResolver: scriptgen.VidRushPlanResolverFunc(func(ctx context.Context, req scriptgen.GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			plan, err := root.AI.SceneTextGenerator.ResolveVidRushPlan(ctx, req)
			return plan, err
		}),
		Backpressure: scriptgen.DefaultVidRushBackpressure(),
		NERPort:      visualNER,
		SamplerPort:  mediaSampler,
		CertifierPort: scriptgen.MediaCertifierFunc(func(_ context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error) {
			return mediacert.Certify(spec, result), nil
		}),
		CertSpecResolver: scriptgen.MediaCertSpecResolverFunc(buildRuntimeMediaCertSpec),
	}
	if root.MediaPostgres != nil && strings.TrimSpace(cfg.ClipIndexer.ServerURL) != "" && vidRushProviders != nil {
		searcher := pgmedia.NewMediaSearcher(root.MediaPostgres)
		embedder := stockintelligence.NewHTTPTextEmbedderAdapter(func(ctx context.Context, text string) ([]float32, error) {
			res, err := embeddings.NewHTTPTextEmbedder(cfg.ClipIndexer.ServerURL).Embed(ctx, text)
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
			if service, serviceErr := stockintelligence.NewService(resolver); serviceErr == nil {
				pipeline.StockResolverPort = service
			}
		}
	}
	runner.SetVidRushPipeline(pipeline)
	nlpConcurrency := cfg.Scripts.NLPConcurrency
	if nlpConcurrency <= 0 {
		nlpConcurrency = scriptgen.DefaultNLPConcurrency
	}
	scriptGenerationConcurrency := cfg.Scripts.ScriptGenerationConcurrency
	if scriptGenerationConcurrency <= 0 {
		scriptGenerationConcurrency = scriptgen.DefaultGenerationConcurrency
	}
	ollamaScriptGate := scriptgen.NewGenerationGateWithCapacity(scriptGenerationConcurrency)
	ollamaNLPGate := scriptgen.NewGenerationGateWithCapacity(nlpConcurrency)
	runner.SetGenerationGate(ollamaScriptGate)
	runner.SetNLPGenerationGate(ollamaNLPGate)
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
	runner.SetTTSConcurrency(ttsConcurrency)
	if root.Domains != nil && root.Domains.VoiceoverPublishPool != nil {
		runner.SetVoiceoverPublishDrainer(root.Domains.VoiceoverPublishPool)
	}
	runner.SetSerialMode(cfg.Scripts.SerialMode)
	log.Info("script generation incremental VidRush pipeline wired (extraction + provider fan-out overlap generation)",
		zap.Int("nlp_concurrency", nlpConcurrency),
		zap.Int("script_generation_concurrency", scriptGenerationConcurrency),
		zap.Int("tts_concurrency", ttsConcurrency),
		zap.Bool("serial_mode", cfg.Scripts.SerialMode))

	return runner, nil
}

func buildRuntimeMediaCertSpec(plan *scriptpkg.ResolvedGenerationPlan) mediacert.Spec {
	spec := mediacert.Spec{}
	if plan == nil {
		return spec
	}
	if plan.MediaMode == scriptpkg.MediaModeClipOnly || plan.MediaMode == scriptpkg.MediaModeMixed {
		spec.VideoProvider = scriptpkg.VidRushProviderArtlist
	}
	// Only authored plan segments define an external scene-identity contract.
	// Free-form text generation may legitimately produce multiple structured
	// scenes, so do not invent a synthetic scene-0 expectation here.
	spec.Segments = len(plan.Segments)
	spec.EntitiesPerSegment = plan.MediaPlan.Extraction.MaxEntitiesPerSegment
	spec.ImagesPerSegment = plan.ImagesPerScene
	// Entity image assets are canonical by entity identity and are expected to
	// be reused when the same person/place is mentioned in multiple scenes.
	// The generic stock-video certification forbids cross-scene reuse, but that
	// rule must not reject the entity cache contract.
	spec.AllowCrossSceneAssetReuse = plan.MediaPlan.Extraction.EntityImages.Enabled
	for i, segment := range plan.Segments {
		id := strings.TrimSpace(segment.ID)
		if id == "" {
			id = fmt.Sprintf("scene-%d", i)
		}
		subject := strings.TrimSpace(segment.Topic)
		spec.SegmentsExpected = append(spec.SegmentsExpected, mediacert.SpecSegment{
			ID: id, Subject: subject, WinnerSubjectMatch: subject,
		})
	}
	return spec
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
var _ scriptgen.DocumentPublisher = (*scriptGenerationDocumentPublisher)(nil)
var _ scriptgen.DocumentPublisherPreflight = (*scriptGenerationDocumentPublisher)(nil)
