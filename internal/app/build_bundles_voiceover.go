// Package app — voiceover service bundle construction (Wave 15 PR4d-final).
//
// Extracted from compose_media.go per architecture/current.yaml::Wave 15
// pending #1 ("Migrate the 3 shared helpers... into modules/voiceover.go,
// modules/content.go, modules/images.go"). The "modules/" path in the
// pending item is a forward reference; the canonical current target for
// per-capability helpers is `internal/app/build_bundles_<capability>.go`,
// matching the buildIngestService / buildHealthService / buildSyncTargets
// pattern already established by build_bundles_core.go / build_bundles_process.go
// / build_bundles_domain.go.
//
// Private-helper convention (lowercase `build*`) — these helpers are NOT
// standalone composable bundles; they are internal to BuildDomainBundle
// (build_bundles_domain.go) which aggregates their output into
// ComposeRoot.Domains.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildVoiceoverService sets up the voiceover service and its repository.
//
// PR4-H (June 2026): SetSemanticTagger / SetTranslator / SetClipIndexer
// setters have been removed from voiceover.NewService — the three callbacks
// required for promo generation, translation, and post-enrichment indexing
// are now passed as constructor arguments (semanticTagger, translator,
// clipIndexer). This helper builds them from the canonical dependencies
// (metaWriter + scriptGen) declared in the composition root.
func buildVoiceoverService(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	driveClient *gdrive.Service,
	driveUploader *drive.Uploader,
	assetIndexService *assetindex.Service,
	clipIndexerService *clipindexer.Service,
	destResolver asset.Resolver,
	metaWriter *semantic.MetadataWriter,
	scriptGen *ollama.Generator,
) (*voiceover.Service, *assets.VoiceoversRepository) {

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := assets.NewVoiceoversRepository(dbs.main.DB)

	// Voiceover registry adapter — wraps the SQLite vo repo as a
	// lifecycle.Registry so NewLifecycleFromDeps accepts it.
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	// Build semantic-tagger closure from metaWriter (used by promo
	// generation to enrich voiceover assets with search_text/tags).
	semanticTagger := func(ctx context.Context, prompt, style, mediaType, generator string) (*voiceover.SemanticTaggerResult, error) {
		if metaWriter == nil {
			return nil, fmt.Errorf("voiceover: metaWriter not wired (cannot enrich voiceover semantic metadata)")
		}
		payload, _, err := metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
			AssetID:   "",
			AssetType: "voiceover",
			MediaType: mediaType,
			Source:    "voiceover",
			Generator: generator,
			Style:     style,
			Prompt:    prompt,
		})
		if err != nil {
			return nil, err
		}
		return &voiceover.SemanticTaggerResult{
			SearchText: payload.SearchText,
			Tags:       payload.Tags,
			Subjects:   payload.Subjects,
			Mood:       payload.Mood,
		}, nil
	}

	// Build translator closure from scriptGen (used by promo generation
	// to translate voiceover text into target language). Graceful
	// degradation: if scriptGen is nil, return input unchanged so promo
	// generation can still proceed.
	translator := func(ctx context.Context, text, targetLanguage string) (string, error) {
		if scriptGen == nil {
			return text, nil
		}
		return scriptGen.TranslateText(ctx, text, targetLanguage)
	}

	// Build clip-indexer closure (optional) — used by post-enrichment
	// to trigger embedding generation + Qdrant upsert for the voiceover
	// asset. Wire only when the indexer service is enabled.
	var clipIndexFn voiceover.ClipIndexFunc
	if clipIndexerService != nil && clipIndexerService.IsEnabled() {
		clipIndexFn = func(ctx context.Context, assetID string) error {
			return clipIndexerService.IndexClip(ctx, assetID)
		}
		log.Info("clip indexer wired into voiceover service for semantic search")
	}

	voService := voiceover.NewService(
		cfg, dbs.main.DB, cfg.Paths.PythonScriptsDir, voDir, log,
		driveUploader, voLifecycle, destResolver,
		semanticTagger, translator, clipIndexFn,
	)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	return voService, voRepo
}
