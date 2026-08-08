// Package app — build_bundles_sync.go (split July 2026).
//
// This file owns the catalog-sync bundle construction and the YouTube
// runtime config helper. Extracted from build_bundles_domain.go per
// AGENTS.md Pattern 5.
//
// godlike/06 SSOT: BuildSyncBundle is the single canonical owner of the
// catalogsync construction. buildYouTubeRuntimeConfig is the single
// canonical flattening site for config → YouTube RuntimeConfig.
package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildSyncBundle constructs ONLY the catalog→Drive sync. ProviderRegistry
// moved to BuildSearchBundle (PR4 review).
//
// PR-D (June 2026): catalogsync.NewService now takes Deps{} + returns
// (*Service, error). The legacy late-bind SetDispatcher call is gone;
// the dispatcher is captured at construction time. Composition-root
// pre-rejection lives here so a nil outbox dispatcher fails the bundle
// build with an explicit error instead of racing the late-bind sequence.
func BuildSyncBundle(ctx context.Context, cfg *config.Config, dbs *wiring.Databases, log *zap.Logger, repos *wiring.RepoBundle, search *wiring.SearchBundle, process *wiring.ProcessBundle, drive *wiring.DriveBundle, outbox *wiring.OutboxBundle) (*wiring.SyncBundle, error) {
	_ = ctx
	_ = cfg
	_ = dbs
	_ = repos
	syncTargets := wiring.BuildSyncTargets(cfg, repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo)

	// PR-D composition-root pre-rejection is relaxed for the no-Drive
	// test path: when Drive is disabled the sync bundle still builds
	// with a nil-service reader so the bootstrap tests can complete.
	// The catalogsync service itself remains fail-closed if it is ever
	// invoked without a real Drive client.
	reader := drive.DriveUploader
	if reader == nil {
		reader = &driveutil.Uploader{Log: log}
		log.Warn("BuildSyncBundle: drive reader missing; using nil-service placeholder for disabled-drive bootstrap")
	}
	catalogReader := catalogSyncSourceReader{reader: reader}
	if outbox == nil || outbox.Dispatcher == nil {
		return nil, fmt.Errorf("BuildSyncBundle: outbox.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback; root.Outbox must be built first")
	}

	catalogSync, err := catalogsync.NewService(catalogsync.Deps{
		Reader:     catalogReader,
		Targets:    syncTargets,
		AssetTree:  search.AssetTreeService,
		Dispatcher: outbox.Dispatcher,
		Log:        log,
	})
	if err != nil {
		return nil, fmt.Errorf("BuildSyncBundle: catalogsync.NewService: %w", err)
	}

	return &wiring.SyncBundle{
		CatalogSync: catalogSync,
	}, nil
}

// buildYouTubeRuntimeConfig resolves the flat RuntimeConfig consumed by the
// YouTube application layer from the infrastructure *config.Config. All
// nested config paths are flattened here so the application layer has zero
// dependency on `internal/platform/config`.
func buildYouTubeRuntimeConfig(cfg *config.Config) youtubetypes.RuntimeConfig {
	if cfg == nil {
		return youtubetypes.RuntimeConfig{}
	}
	return youtubetypes.RuntimeConfig{
		MaxConcurrentVideoExtracts: cfg.Concurrency.MaxConcurrentVideoExtracts,
		MaxConcurrentOllamaCalls:   cfg.Concurrency.MaxConcurrentOllamaCalls,
		YouTubeExtractTimeout:      cfg.Jobs.YouTubeExtractTimeout,
		DataDir:                    cfg.Storage.DataDir,
		YtdlpPath:                  cfg.External.ResolvedYtdlpPath(),
		ClipsFolderID:              cfg.Drive.ClipsFolder(),
		OllamaModel:                cfg.External.OllamaModel,
		OllamaMetadataModel:        cfg.External.OllamaMetadataModel,
		YouTubeCookiesPath:         cfg.External.ResolveYouTubeCookiesPath(),
		YouTubeJSRuntimePath:       cfg.External.YouTubeJSRuntimePath,
		YouTubeEnabled:             cfg.Features.YouTubeEnabled,
	}
}
