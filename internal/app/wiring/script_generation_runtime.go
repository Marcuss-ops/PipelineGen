package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

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
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
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

// RenderDocumentSkeleton / InjectDocumentLateBound implement the
// early/late split so the runner can render the scene-text-only skeleton at
// SceneTextReady (overlapping TTS/NLP) and fill the late-bound markers after
// the audio join.
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

// buildScriptGenerationRuntime creates the worker-owned durable runtime. The
// HTTP starter only creates/correlates the run; this runtime is invoked by the
// script.generate worker after the submission transaction has committed.
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
	visualNER, err := rustexec.NewVisualNERAdapter(executor)
	if err != nil {
		return nil, fmt.Errorf("build VisualNER adapter: %w", err)
	}
	mediaSampler, err := rustexec.NewMediaSamplerAdapter(executor)
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
	runner.SetCombinedAudioRenderer(audioRenderer)
	runner.SetFinalAudioPublisher(newFinalAudioPublisher(root, committer, log))
	// This runtime materializes localized clips and related artifacts only.
	// Complete-video assembly is outside the script-generation capability.
	// Wire the BGM/SFX asset resolver: asset_id → verified local path via
	// the canonical asset registry (+ Drive materialization into scratch).
	// The audio layer resolver consumes it when the run carries an audio
	// intent block; absent intents never touch it. Fail-closed: a run with
	// intents and no wired resolver fails in the audio-compile phase.
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

		// P0.5: wire the MediaPreflight. Runs fail-fast asset verification
		// (clip existence, original audio, BGM/SFX/watermark) in parallel
		// with Gemma scene text generation.
		runner.SetMediaPreflight(&mediaPreflightAdapter{
			clipProber:           &assetServiceClipProber{assets: root.Repos.Assets},
			audioAssetSource:     audioAdapter,
			clipAudioAssetSource: audioAdapter,
		})

		log.Info("audio asset resolver wired (BGM/SFX asset_id → local path) including P0.5 media preflight adapter")
	} else {
		log.Warn("audio asset resolver not wired: asset registry missing (BGM/SFX intents will fail closed)")
	}
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
		// Attach the SQLite render-attempt analytics recorder so the coarse
		// per-attempt row (render_ms/encode_ms) is persisted in parallel with
		// the granular chronon.* phases the Chronon Metrics Adapter projects
		// into performance_operations. Both are projections of the same render
		// into their own existing tables — never new ones. Best-effort: a
		// missing DB skips analytics, never fails the run.
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

	// ── Incremental VidRush ────────────────────────────────────────────
	// Wire the run-scoped incremental coordinator so scene generation and
	// VidRush enrichment (entities → queries → provider fan-out) overlap in the
	// real flow instead of running as two sequential blocks. The Runner builds
	// a fresh coordinator per run from these immutable dependencies.
	// VidRush entity decisions are deterministic and source-grounded. LLM
	// extraction is intentionally not wired here: Ollama may enrich script
	// copy, but it must not own the visual entities that drive media search.
	var vidRushEntityExtractor entityports.EntityExtractor = visualNER

	// ── Image Search Intent resolver (capabilities/imagesearch) ────────
	// The deterministic editorial/visual decision layer the golden battery
	// certifies (typed entities → canonical ids → ordered queries → no-image
	// gate → negation → coreference). It is built over the SAME entity
	// extractor that feeds the VidRush pipeline, so production consumes the
	// exact path the battery certifies — never a second, ad-hoc extractor.
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
		)
	}
	// The materialization stage (acquire → verify → finalize) is wired through
	// the same processor the batch flow registers, so the incremental
	// coordinator reuses it under its own bounded materialization limit.
	var vidRushMaterializer scriptgen.SegmentMaterializer
	if vidRushProviders != nil && vidRushFinalizer != nil {
		var entityImageCatalogRepo entitycatalog.Repository
		if root.Repos != nil {
			entityImageCatalogRepo = root.Repos.EntityImageCatalog
		}
		vidRushMaterializer = documentadapters.NewVidRushMaterializationProcessorWithCatalog(vidRushProviders, vidRushFinalizer, vidRushCache, entityImageCatalogRepo, vidRushMetrics).WithMediaSampler(mediaSampler)
	}
	pipeline := &scriptgen.VidRushPipeline{
		// SceneIRSegmentEnricher is constructed by Runner.beginVidRush from
		// NERPort. SceneIR is the only semantic enrichment path wired here.
		Enricher:         nil,
		ProviderResolver: vidRushFanout,
		Materializer:     vidRushMaterializer,
		Metrics:          vidRushMetrics,
		PlanResolver: scriptgen.VidRushPlanResolverFunc(func(ctx context.Context, req scriptgen.GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return root.AI.SceneTextGenerator.ResolveVidRushPlan(ctx, req)
		}),
		Backpressure: scriptgen.DefaultVidRushBackpressure(),
		NERPort:      visualNER,
		SamplerPort:  mediaSampler,
		CertifierPort: scriptgen.MediaCertifierFunc(func(_ context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error) {
			return mediacert.Certify(spec, result), nil
		}),
		CertSpecResolver: scriptgen.MediaCertSpecResolverFunc(buildRuntimeMediaCertSpec),
	}
	// Local Stock Intelligence is the canonical resolver path. Qdrant is
	// search projection, SQLite media_assets is truth, and the provider
	// registry is consulted only by the resolver's explicit fallback policy.
	if root.Process != nil && root.Process.QdrantSearcher != nil && root.Repos != nil && root.Repos.AssetsStore != nil && vidRushProviders != nil {
		var embedder qdrantsearch.TextEmbedder
		if cfg.ClipIndexer.ServerURL != "" {
			embedder = qdrantsearch.NewTextEmbedderAdapter(embeddings.NewHTTPTextEmbedder(cfg.ClipIndexer.ServerURL))
		}
		if embedder != nil {
			local := stockintelligence.QdrantLocalSearchAdapter{Searcher: root.Process.QdrantSearcher, Embedder: embedder, VectorName: "text"}
			hydrator := stockintelligence.SQLiteAssetHydrator{Store: root.Repos.AssetsStore}
			provider := stockintelligence.RegistryProviderClient{Registry: vidRushProviders}
			sampler := func(ctx context.Context, candidates []stockintelligence.Candidate, segmentID, subject string, terms []string) (string, error) {
				converted := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
				for _, candidate := range candidates {
					converted = append(converted, scriptpkg.SegmentAssetCandidate{AssetID: candidate.AssetID, Entity: candidate.Label, RelevanceScore: float64(candidate.GenericSimilarity), SegmentID: candidate.OwnerSegmentID})
				}
				return mediaSampler.Sample(ctx, segmentID, subject, terms, converted, false)
			}
			if resolver, resolverErr := stockintelligence.NewResolver(local, hydrator, provider, sampler); resolverErr == nil {
				if service, serviceErr := stockintelligence.NewService(resolver); serviceErr == nil {
					pipeline.StockResolverPort = service
				}
			}
		}
	}
	runner.SetVidRushPipeline(pipeline)
	// Shared worker pools: NLP/entity extraction and script text generation
	// have independent gates and tunables. The TTS voiceover pool defaults to
	// 4. Docs publishing and the Rust final-audio render remain single-threaded.
	nlpConcurrency := cfg.Scripts.NLPConcurrency
	if nlpConcurrency <= 0 {
		nlpConcurrency = scriptgen.DefaultNLPConcurrency
	}
	scriptGenerationConcurrency := cfg.Scripts.ScriptGenerationConcurrency
	if scriptGenerationConcurrency <= 0 {
		// Certified default: match the measured OLLAMA_NUM_PARALLEL baseline on
		// A4000/e4b (3). Client slots beyond the server parallelism only deepen
		// the server queue — they never add throughput.
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
	// P0.4: wire the async voiceover publish pool drainer so the runner
	// waits for Drive uploads + timing publishes + DB commits before
	// audio compile and docs stages. Nil pool (synchronous path) is safe.
	if root.Domains != nil && root.Domains.VoiceoverPublishPool != nil {
		runner.SetVoiceoverPublishDrainer(root.Domains.VoiceoverPublishPool)
	}
	// Serial mode is the controlled-benchmark "before" toggle: entities →
	// voiceover (no overlap) with single-slot NLP/TTS pools.
	runner.SetSerialMode(cfg.Scripts.SerialMode)
	log.Info("script generation incremental VidRush pipeline wired (extraction + provider fan-out overlap generation)",
		zap.Int("nlp_concurrency", nlpConcurrency),
		zap.Int("script_generation_concurrency", scriptGenerationConcurrency),
		zap.Int("tts_concurrency", ttsConcurrency),
		zap.Bool("serial_mode", cfg.Scripts.SerialMode))

	return runner, nil
}

// buildRuntimeMediaCertSpec projects only the resolved plan contract into
// MediaCert. Semantic rules remain exclusively in mediacert; this function
// supplies run identity and policy, never a second rule engine.
func buildRuntimeMediaCertSpec(plan *scriptpkg.ResolvedGenerationPlan) mediacert.Spec {
	spec := mediacert.Spec{VideoProvider: scriptpkg.VidRushProviderArtlist}
	if plan == nil {
		return spec
	}
	spec.Segments = len(plan.Segments)
	spec.EntitiesPerSegment = plan.MediaPlan.Extraction.MaxEntitiesPerSegment
	spec.ImagesPerSegment = plan.ImagesPerScene
	for _, segment := range plan.Segments {
		id := strings.TrimSpace(segment.ID)
		if id == "" {
			continue
		}
		subject := strings.TrimSpace(segment.Topic)
		spec.SegmentsExpected = append(spec.SegmentsExpected, mediacert.SpecSegment{
			ID: id, Subject: subject, WinnerSubjectMatch: subject,
		})
	}
	return spec
}

// wireRenderAttemptRecorder builds the SQLite render-attempt analytics
// recorder over the primary DB (render_attempt_analytics lives in the primary
// database). Best-effort wiring mirroring wireChrononMetricsAdapter: a nil DB
// or a construction error logs a Warn and returns nil — the render enqueuer
// then skips analytics instead of aborting boot.
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
