// Package wiring — script_generation_bindings.go: the leaf bindings the script
// generation wiring hands to the runner — artifact verification, final-audio
// cache directory, render-attempt recorder, VidRush adapters and the exported
// aliases.
//
// Split 2026-11-20 from script_generation_runtime.go (BONIFICA-04): the blocks
// are moved verbatim, no behaviour change.
package wiring

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/script"
	vidrushwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/vidrush"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
	"go.uber.org/zap"
)

func mediaCheckpointArtifactVerifier(committer assetspersistence.AssetCommitter, log *zap.Logger) capcheckpoint.ArtifactVerifier {
	if committer == nil {
		return nil
	}
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	verifier, err := handle.MediaArtifactVerifier()
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
		// One CAS read side for the whole VidRush image path: the fan-out and the
		// materializer must resolve a catalog candidate's local path from the
		// same content-addressed store.
		contentStore := vidrushwiring.BuildVidRushCASStore(cfg, log)
		registryMediaResolver := &documentadapters.VidRushRegistryMediaResolver{Registry: vidRushProviders}
		fanout := documentadapters.NewVidRushProviderFanoutWithResolver(
			registryMediaResolver,
			vidRushCache,
			entityImageCatalogRepo,
			vidRushMetrics,
		).WithLogger(log).WithCASStore(contentStore)
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
		vidRushMaterializer = documentadapters.NewVidRushMaterializationProcessorWithCatalog(vidRushProviders, vidRushFinalizer, vidRushCache, entityImageCatalogRepo, vidRushMetrics).WithMediaSampler(mediaSampler).WithMediaSamplerGroup(mediaSampler).WithLogger(log).WithCASStore(vidrushwiring.BuildVidRushCASStore(cfg, log))
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
