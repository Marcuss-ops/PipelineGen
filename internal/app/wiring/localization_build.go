package wiring

import (
	"context"
	"fmt"

	clipswiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring/localizedrender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	localizationadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/localizationreuse"
	"go.uber.org/zap"
)

func BuildLocalizationService(cfg *config.Config, root *ComposeRoot, log *zap.Logger) (*localizedrender.LocalizationService, error) {
	if root == nil {
		return nil, fmt.Errorf("localization service: composition root is nil")
	}
	if root.Repos == nil {
		return nil, fmt.Errorf("localization service: repo bundle is required")
	}
	// MEDIA-SSOT P1-5: the media read surface is the PostgreSQL SSOT, and only
	// it. The legacy SQLite asset registry was removed as a valid read surface
	// (there is no second media catalog), so localization fails closed when the
	// media plane is absent instead of degrading onto a divergent reader.
	if root.MediaPostgres == nil {
		return nil, fmt.Errorf("localization service: media PostgreSQL SSOT is required (no second media catalog)")
	}
	if root.Drive == nil || root.Drive.Publisher == nil {
		return nil, fmt.Errorf("localization service: Drive publisher is required")
	}
	if root.Repos.TextTrackRepo == nil {
		return nil, fmt.Errorf("localization service: text-track repository is required")
	}
	if root.MediaExec == (mediaexec.ExecutionConfig{}) {
		return nil, fmt.Errorf("localization service: resolved media execution config is required")
	}

	trackStore, ok := root.Repos.TextTrackRepo.(localizedrender.LocalizationTrackStore)
	if !ok {
		return nil, fmt.Errorf("localization service: text-track repository %T does not expose FindByID (subtitle PK lookup)", root.Repos.TextTrackRepo)
	}

	scratchDir := clipswiring.AssetMaterializationRoot(cfg)
	resolver, resolverErr := clipswiring.NewClipRenderMediaResolver(root.MediaPostgres, log)
	if resolverErr != nil {
		return nil, fmt.Errorf("localization service: build asset resolver: %w", resolverErr)
	}
	var driveReader drive.Reader
	if root.Drive != nil {
		driveReader = root.Drive.Reader
	}
	materializer, materializerErr := clipadapters.NewClipRenderMaterializer(driveReader, scratchDir, log)
	if materializerErr != nil {
		return nil, fmt.Errorf("localization service: build asset materializer: %w", materializerErr)
	}
	prober := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	sources := localizationadapters.NewSourceResolver(resolver, materializer, prober)

	subtitleResolver := localizationadapters.NewSubtitleResolver(trackStore)
	subtitleCompiler := localizationadapters.NewSubtitleCompiler()

	renderRuntime, runtimeErr := clipswiring.BuildClipRenderRuntime(clipswiring.ClipRenderRuntimeDeps{
		Config:    cfg,
		MediaExec: root.MediaExec,
		Existing:  root.ClipRenderRuntime,
	})
	if runtimeErr != nil {
		return nil, fmt.Errorf("localization service: build shared render runtime: %w", runtimeErr)
	}
	root.ClipRenderRuntime = renderRuntime
	executor := localizationadapters.NewRenderPlanExecutor(renderRuntime.RenderingGenExecutor, root.MediaExec.Profile, log)

	uploader := localizationadapters.NewDriveUploader(root.Drive.Publisher)

	svcCfg := localizedrender.LocalizationConfigFromConfig(cfg)

	// Content-addressed reuse of localized renders: the same plan fingerprint
	// must not pay the GPU twice. Boot-ensured next to the canonical render
	// cache; an ensure failure degrades to "always render" with a warning
	// instead of failing the whole service, because the cache is an
	// optimization and the render path is the authority.
	var renderReuse localization.RenderReuseCache
	if ensureErr := localizationreuse.EnsureLocalizedRenderReuseTable(context.Background(), root.MediaPostgres); ensureErr != nil {
		log.Warn("localization service: render reuse cache unavailable; every plan will render",
			zap.String("subsystem", "localization_service"),
			zap.Error(ensureErr))
	} else {
		renderReuse = localizationreuse.NewLocalizedRenderReuse(root.MediaPostgres, log)
	}

	svc, err := localizedrender.NewLocalizationService(localizedrender.LocalizationDeps{
		Sources:          sources,
		TrackResolver:    localizationadapters.NewTrackResolver(trackStore),
		SubtitleResolver: subtitleResolver,
		SubtitleCompiler: subtitleCompiler,
		Executor:         executor,
		Uploader:         uploader,
		RenderReuse:      renderReuse,
	}, svcCfg)
	if err != nil {
		return nil, err
	}
	svc.SetLogger(log)
	return svc, nil
}
