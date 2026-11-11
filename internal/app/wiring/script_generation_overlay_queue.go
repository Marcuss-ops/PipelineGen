package wiring

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/script"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	"go.uber.org/zap"
)

func wireScriptGenerationOverlayQueue(runner *scriptgen.Runner, cfg *config.Config, root *ComposeRoot, log *zap.Logger) error {
	if queueURL := strings.TrimSpace(cfg.External.RenderingGenQueueURL); queueURL != "" {
		queueClient := renderinggen.New(queueURL)
		storeURL := strings.TrimSpace(os.Getenv("RENDERINGGEN_STORE_URL"))
		if storeURL == "" {
			storeURL = defaultRenderingGenStoreURL
		}
		queueClient.SetAssetPrefetcher(renderinggen.NewHTTPAssetPrefetcher(storeURL))
		prepareEnqueuer, err := scriptgen.NewQueuePrepareEnqueuer(queueClient)
		if err != nil {
			return fmt.Errorf("build queue prepare enqueuer: %w", err)
		}
		runner.SetOverlayPrepareEnqueuer(prepareEnqueuer)
		renderEnqueuer, err := scriptgen.NewQueueRenderEnqueuer(queueClient)
		if err != nil {
			return fmt.Errorf("build queue render enqueuer: %w", err)
		}
		renderEnqueuer.SetDynamicMapVideoCacheDir(cfg.Storage.FullPath("dynamic-map-video-cache"))
		mapGPUGate, gateErr := scriptgen.NewDynamicMapGPUAdmission()
		if gateErr != nil {
			return fmt.Errorf("build shared dynamic-map GPU admission gate: %w", gateErr)
		}
		renderEnqueuer.SetDynamicMapGPUGate(mapGPUGate)
		if cfg.External.RenderingGenPollIntervalMS > 0 {
			renderEnqueuer.SetPollInterval(time.Duration(cfg.External.RenderingGenPollIntervalMS) * time.Millisecond)
		}
		// Reuse the canonical queue identity for retries/replays. A new render is
		// requested explicitly by a new plan/force-refresh request; making every
		// submission fresh here caused identical overlays to be rendered and
		// published repeatedly.
		renderEnqueuer.SetFreshRender(false)
		// Pipelining depth for the per-item overlay renders. This is not a GPU
		// bound: the worker's worker.gpu_lanes remains the only authority on
		// concurrent GPU work, so this overlaps the per-item pre/post chain
		// without oversubscribing the device.
		renderEnqueuer.SetItemRenderPool(cfg.Scripts.SeparateItemRenderWorkers)
		scriptwiring.WireOverlayRenderCache(scriptCacheDB(root), renderEnqueuer, log)
		// Generated semantic overlays have their own canonical root. Clip
		// renders use a separate routing contract and must not influence this
		// destination: <Overlay Chronon>/<JobID>/<language>/overlay.
		overlayParentFolderID := cfg.Drive.OverlayRenderFolder()
		if root.Drive != nil && root.Drive.Publisher != nil && strings.TrimSpace(overlayParentFolderID) != "" {
			drivePublisher := drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log)
			overlayPublisher := renderinggen.NewDriveOverlayArtifactPublisher(drivePublisher)
			overlayPublisher.SetRootFolderID(overlayParentFolderID)
			// Every generated semantic overlay is routed by JobID/PlanID and
			// language. The configured folder is only the root; an empty
			// ScriptsGenerateFolder must not silently flatten the overlay tree.
			overlayPublisher.SetScriptLanguageRouting(true)
			publicationPublisher, pubErr := wireDurableOverlayPublisher(root, overlayPublisher)
			if pubErr != nil {
				return pubErr
			}
			renderEnqueuer.SetArtifactPublisher(publicationPublisher)
			// RenderingGen has already certified immutable bytes when the
			// enqueuer returns. Drive publication and analytics therefore run on
			// the bounded post-render pool; the runner joins it before COMPLETE.
			renderEnqueuer.SetPublicationWorkers(cfg.Scripts.OverlayPublicationWorkers)
			renderEnqueuer.SetAsyncPublication(true)
			runner.SetOverlayPublicationDrainer(renderEnqueuer)
			log.Info("overlay artifact Drive publisher wired",
				zap.String("parent_folder_id", overlayParentFolderID),
				zap.String("child_folder", "overlay"),
				zap.Bool("durable_outbox", publicationPublisher != overlayPublisher))
		} else {
			// Ordinary certified video renders do not need a Drive copy. The
			// queue fails closed if a final-job delivery explicitly requires one.
			// Single publication path (TICKET-MAX-THROUGHPUT-OVERLAY-
			// ARCHITECTURE §9.3.3 punto 5): the bounded post-render pool owns
			// any publication work in EVERY configuration — Drive-backed or
			// analytics-only — so no submit-side wait can ever serialize a
			// render lane behind Drive/analytics latency.
			renderEnqueuer.SetPublicationWorkers(cfg.Scripts.OverlayPublicationWorkers)
			renderEnqueuer.SetAsyncPublication(true)
			runner.SetOverlayPublicationDrainer(renderEnqueuer)
			log.Info("overlay Drive publisher unavailable; local certified renders remain enabled",
				zap.Int("publication_workers", cfg.Scripts.OverlayPublicationWorkers))
		}
		var analyticsDB *sql.DB
		if root.DB != nil {
			analyticsDB = root.DB.DB
		}
		renderEnqueuer.SetRecorder(wireRenderAttemptRecorder(analyticsDB, log))
		runner.SetOverlayRenderEnqueuer(renderEnqueuer)
		// Bounded multilingual render fan-out. It replaces the serial
		// per-language render loop, so N languages no longer wait on Chronon
		// one after another. The RenderingGen worker still owns gpu_lanes, so
		// this bounds pipelining, not device load.
		runner.SetOverlayRenderConcurrency(cfg.Scripts.OverlayRenderConcurrency)
		log.Info("overlay.prepare enqueuer wired to RenderingGen queue",
			zap.String("url", queueURL),
			zap.Int("overlay_render_concurrency", runner.OverlayRenderConcurrency()))
	} else {
		log.Warn("overlay.prepare enqueue disabled: RENDERINGGEN_QUEUE_URL is not configured")
	}
	return nil
}
