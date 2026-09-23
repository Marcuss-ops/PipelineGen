// registry_internal_modules_render.go — the clip.render module binding.
//
// Split from registry_internal_modules.go (max_lines_per_file_strict, 600):
// registerClipRender is one cohesive boundary (RenderingGen queue → Chronon
// certified artifact) and moves here verbatim. Pure code move, no behavior
// change.
package wiring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	renderingwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/rendering"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

func registerClipRender(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, idempotencyHandler gin.HandlerFunc) error {
	if !cfg.Features.ClipRenderEnabled {
		log.Info("registerClipRender: ClipRender feature is disabled; skipping HTTP route registration + job binding")
		return nil
	}
	if root.Jobs == nil || root.Jobs.Facade == nil {
		return fmt.Errorf("registerClipRender: root.Jobs.Facade is required when ClipRenderEnabled=true (the POST /clips/render enqueue path needs the Master job service)")
	}

	// Parallel-preparation adapters (composition root owns mechanics,
	// the capability owns the ports). Every adapter is fail-closed at
	// call time when a dependency is missing.
	resolver, err := newClipRenderMediaResolver(root, log)
	if err != nil {
		return fmt.Errorf("registerClipRender: build asset resolver: %w", err)
	}
	var driveReader drivepkg.Reader
	if root.Drive != nil {
		driveReader = root.Drive.Reader
	}
	materializer, err := clipadapters.NewClipRenderMaterializer(driveReader, assetMaterializationRoot(cfg), log)
	if err != nil {
		return fmt.Errorf("registerClipRender: build asset materializer: %w", err)
	}
	preparedResolver, err := cliprender.NewPreparedAssetResolver(assetMaterializationResolverRoot(cfg), materializer)
	if err != nil {
		return fmt.Errorf("registerClipRender: build prepared asset resolver: %w", err)
	}
	materializerPort := cliprender.AssetMaterializer(preparedResolver)
	transcriptResolver := clipadapters.NewClipRenderTranscriptResolver(log)
	if root.Repos != nil {
		transcriptResolver.SetRepo(root.Repos.TextTrackRepo)
	}
	if root.TextTracks != nil {
		transcriptResolver.SetAcquire(root.TextTracks.AcquireService)
	}
	if root.Domains != nil {
		transcriptResolver.SetCueWriter(root.Domains.CueWriter)
	}
	// Streaming PCM transcriber (spec §4: zero temp WAV). Construction is
	// fail-closed with a typed error when python3/ffmpeg/bridge are missing;
	// the resolver falls back to the canonical WAV-chain with a warning.
	if streaming, err := clipadapters.NewClipRenderStreamingTranscriber(cfg, log); err != nil {
		log.Warn("registerClipRender: streaming transcriber unavailable; transcript generation will use the WAV chain",
			zap.String("reason", err.Error()))
	} else {
		transcriptResolver.SetStreaming(streaming)
	}

	preparer, err := cliprender.NewPreparer(
		resolver,
		materializerPort,
		transcriptResolver,
		cliprender.NewContractResolver(),
		log,
	)
	if err != nil {
		return fmt.Errorf("registerClipRender: build preparer: %w", err)
	}
	worker, err := cliprender.NewWorker(preparer, filepath.Join(cfg.Storage.TempPath(), "cliprender"), log)
	if err != nil {
		return fmt.Errorf("registerClipRender: build worker: %w", err)
	}
	// Deterministic ASS compiler (canonical texttracks content generator —
	// single owner). Subtitles.enabled=true without a wired compiler fails
	// closed in the worker; this wiring makes burn+sidecar always available.
	subtitleCompiler := clipadapters.NewClipRenderSubtitleCompiler()
	if root.Repos != nil {
		subtitleCompiler.SetArtifactRepository(root.Repos.SubtitleArtifactRepo)
	}
	worker.WithSubtitleCompiler(subtitleCompiler)

	// Chronon timing-sidecar projection (2026-09-19). The render outcome carries
	// a content-addressed reference to the raw `<output>.timing.json` deep
	// profile RenderingGen preserved; this is the ONE place those bytes are
	// fetched, parsed (cliprender.ParseChrononSidecar) and recorded as measured
	// phases in performance_operations through the canonical
	// OperationReportProjectionRecorder seam. Without it the reference travels
	// on the job result and the phase history is never written — exactly the
	// gap the observability matrix used to claim as DONE. Best-effort by
	// construction: the projection can never fail a render.
	if root.DB != nil && root.DB.DB != nil {
		storeURL := strings.TrimSpace(os.Getenv("RENDERINGGEN_STORE_URL"))
		if storeURL == "" {
			storeURL = defaultRenderingGenStoreURL
		}
		switch fetcher, fErr := renderinggen.NewChrononTimingFetcher(storeURL); {
		case fErr != nil:
			log.Warn("registerClipRender: chronon timing metrics NOT wired (object store URL invalid)", zap.Error(fErr))
		default:
			if adapter := renderingwiring.NewChrononMetricsAdapter(root.DB.DB, log); adapter == nil {
				log.Warn("registerClipRender: chronon timing metrics NOT wired (performance store unavailable)")
			} else {
				worker.SetChrononMetrics(adapter, fetcher)
				log.Info("registerClipRender: chronon timing metrics wired (performance_operations)")
			}
		}
	} else {
		log.Warn("registerClipRender: chronon timing metrics NOT wired (primary SQLite unavailable)")
	}

	// RenderingGen/Chronon render boundary: the shared executor owns queue
	// submission; the remote worker owns Chronon execution.
	// config (encoder policy + profile owned by the composition root, never
	// by Rust). Fail-closed when the media config is missing, mirroring
	// WireStockPipeline. The Chronon clip executor is attached to the worker via the
	// RenderExecutor port; the worker selects Submit/Settle when the async
	// continuation ports are wired, retaining Render as a compatibility path.
	mediaConfig := root.MediaExec
	if mediaConfig == (mediaexec.ExecutionConfig{}) {
		return fmt.Errorf("registerClipRender: resolved media execution config is required when ClipRenderEnabled=true (root.MediaExec)")
	}
	renderRuntime, runtimeErr := BuildClipRenderRuntime(cfg, root, log)
	if runtimeErr != nil {
		return fmt.Errorf("registerClipRender: build shared render runtime: %w", runtimeErr)
	}
	worker.WithRenderExecutor(renderRuntime.RenderingGenExecutor)
	worker.WithContinuationStore(renderRuntime.ContinuationStore)
	worker.WithContinuationEnqueuer(&clipRenderContinuationEnqueuer{jobs: root.Jobs.Facade})
	if root.Drive == nil || root.Drive.Publisher == nil || root.DB == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("registerClipRender: Drive publisher, SQLite DB and outbox are required for rendered asset publication")
	}
	var committer assetspersistence.AssetCommitter
	{
		w, werr := newCanonicalAssetCommitter(root.MediaPostgres, log)
		if werr != nil {
			return fmt.Errorf("registerClipRender: canonical media writer: %w", werr)
		}
		if w == nil {
			return fmt.Errorf("registerClipRender: canonical media writer unavailable (media PostgreSQL not deployed)")
		}
		committer = w
	}
	publisher, publisherErr := clipadapters.NewClipRenderPublisher(root.Drive.Publisher, committer, log)
	if publisherErr != nil {
		return fmt.Errorf("registerClipRender: build clip render publisher: %w", publisherErr)
	}
	publisher.SetSubtitleArtifactRepository(root.Repos.SubtitleArtifactRepo)
	// clip.render Drive delivery is UNCONDITIONALLY asynchronous, so the
	// staging root is always required. The outbox consumer must exist or the
	// committed delivery intent would never drain — fail the wiring closed
	// instead of silently publishing into a void.
	publisher.SetAsyncDriveStagingRoot(filepath.Join(cfg.Storage.TempPath(), "cliprender", "staging"))
	if root.Outbox == nil || root.Outbox.MediaIndexWorker == nil {
		return fmt.Errorf("registerClipRender: PostgreSQL media outbox worker is required for asynchronous clip.render Drive delivery")
	}
	log.Info("registerClipRender: PostgreSQL media outbox Drive delivery handler is available",
		zap.String("event_type", cliprender.EventClipRenderDriveDeliveryRequested))
	worker.WithRenderPublisher(publisher)
	// Script/batch leaf-folder resolution (create-or-reuse under the caller's
	// root): routed through the SAME delivery.Publisher, so folder creation
	// has one canonical owner and the ClipRenderPublisher stays dumb (it
	// receives the fully-resolved leaf folder ID and never creates folders).
	destinationResolver, destinationResolverErr := clipadapters.NewClipRenderDestinationFolderResolver(root.Drive.Publisher, log)
	if destinationResolverErr != nil {
		return fmt.Errorf("registerClipRender: build clip render destination resolver: %w", destinationResolverErr)
	}
	worker.WithDestinationFolderResolver(destinationResolver)

	// Overlay hop (entity overlays): the segment resolver reads the SAME
	// content cache the overlay.render handler writes (the
	// RENDERINGGEN_CACHE_ROOT / default root BuildRenderingRuntime uses — a
	// plain directory, so a second cache handle is harmless). The resolved
	// segment is sealed into the plan and composited by Chronon INSIDE the
	// single render pass; there is no post-render compositor (the FFmpeg
	// second-transcode path was demolished and the CI gate rejects new
	// callers). Fail-closed at call time: an overlay declared without the
	// resolver is a typed worker error.
	cacheRoot := os.Getenv("RENDERINGGEN_CACHE_ROOT")
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "pipelinegen", "renderinggen", "cache")
	}
	overlayCache, cacheErr := infraoverlays.NewCache(cacheRoot)
	if cacheErr != nil {
		return fmt.Errorf("registerClipRender: build overlay cache: %w", cacheErr)
	}
	worker.WithOverlaySegmentResolver(clipadapters.NewOverlaySegmentResolver(overlayCache))

	// Deterministic render cache (fingerprint → certified locator): reuses
	// the same media PostgreSQL that backs the catalog. A repeated POST
	// with identical semantics returns in milliseconds without touching the
	// GPU; a batch collapses identical items to one slot.
	var renderCache cliprender.RenderCache
	if root.MediaPostgres != nil {
		if err := cliprender.EnsureRenderCacheTable(context.Background(), root.MediaPostgres); err != nil {
			log.Warn("registerClipRender: ensure render cache table failed; cache disabled",
				zap.Error(err))
		} else {
			renderCache = cliprender.NewPostgresRenderCache(root.MediaPostgres)
			log.Info("registerClipRender: deterministic render cache wired",
				zap.String("table", "clip_render_cache"))
		}
	} else {
		log.Info("registerClipRender: media PostgreSQL unavailable; deterministic render cache disabled")
	}
	worker.WithRenderCache(renderCache)

	log.Info("registerClipRender: clip render boundary wired (RenderingGen queue → Chronon certified artifact)",
		zap.String("renderinggen_queue", cfg.External.RenderingGenQueueURL),
		zap.String("overlay_compositing", "single-pass (sealed into the Chronon plan)"),
		zap.String("encoder", mediaConfig.Policy.Codec),
		zap.String("preset", mediaConfig.Policy.Preset),
		zap.Int("crf", mediaConfig.Policy.CRF),
		zap.Int("profile_width", mediaConfig.Profile.Width),
		zap.Int("profile_height", mediaConfig.Profile.Height),
		zap.Int("profile_fps_num", mediaConfig.Profile.FPSNum),
		zap.Int("profile_fps_den", mediaConfig.Profile.FPSDen),
	)

	descriptor, err := cliprender.Build(cliprender.Dependencies{
		Jobs:        root.Jobs.Facade,
		EnabledFunc: func() bool { return cfg.Features.ClipRenderEnabled },
		Idempotency: idempotencyHandler,
		RenderCache: renderCache,
		Logger:      log,
		ModuleOpts:  nil,
	})
	if err != nil {
		return fmt.Errorf("registerClipRender: cliprender.Build: %w", err)
	}

	// Canonical worker binding: parallel preparation, asynchronous
	// RenderingGen submission/settle, and parent aggregation are wired here;
	// missing execution wiring still fails closed.
	if err := root.Jobs.Facade.RegisterHandler(cliprender.TypeClipRender, appjobs.HandlerFunc(worker.Handle)); err != nil {
		return fmt.Errorf("registerClipRender: bind clip.render handler: %w", err)
	}

	log.Info("created ClipRender module via cliprender.Build (canonical clip post-processing, parallel preparation wired)")
	return tryRegisterModuleStrict(registry, log, descriptor, WithRegistrationPoint("register.ClipRender"))
}
