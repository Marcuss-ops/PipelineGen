// Package app — wire_services_composition.go (COMPOSITION FLOW, July 2026
// split).
//
// Split rationale, see wire_services.go header.
//
// This file owns the COMPOSITION stage: the chain that:
//  1. Opens Databases
//  2. Runs migrations
//  3. Calls NewComposition (builds *ComposeRoot with 12 bundles)
//  4. Constructs the local broker (PR-WORKER-RUNNER-INPROCESS-MIGRATION,
//     July 2026)
//  5. Wires JobFinalizer for Path B artifact-producing jobs
//     (PR-COMPLETE-WORKER-BROAD-FIX, July 2026)
//  6. Starts background jobs (jobs.startupPlan captured for server
//     lifecycle)
//  7. Builds the cleanup func (LIFO)
//
// Cross-file deps (same package `app`, accessed without explicit imports):
//   - WireRegistry (in orchestration file + registry.go)
//   - NewComposition (composition.go)
//   - startBackgroundJobs (go)
//   - buildCleanup (shutdown.go)
//   - security.SetAllowedHosts (infra/security)
//   - workernodes.NewWorkerNodesRepository + localbroker.New +
//     localbroker.NewProgressCoalescer + assetfinalizer.NewAssetTxFinalizer
//   - jobsfinalizer.New
//
// The composition-buildable assets (broker, finalizer, textTracks.FanOut
// wiring) are stored on *ComposeRoot bundles so the orchestration stage
// (wire_services_orchestration.go) can re-cast them as typed ports.
//
// Why NOT extend the split inside WireServices for assetSvc +
// workerHandler construction: those two are built FROM root.* fields +
// broker AFTER initCompositionMinimal has returned, and they live
// exclusively in AppDeps.Handlers.WorkerHandler (not on ComposeRoot).
// Promoting them to composition would require adding fields to
// ComposeRoot, which violates the godlike/06 SSOT contract for this
// refactor (ComposeRoot contract must stay unchanged).
package wiring

import (
	"context"
	"fmt"
	"time"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	jobsfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/platform/jobs/local"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/workernodes"
	"github.com/Marcuss-ops/PipelineGen/pkg/security"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// initCompositionMinimal is the public-name entry for servers and tests
// that do NOT need a parent ctx (defaults to context.Background()).
func initCompositionMinimal(cfg *config.Config, log *zap.Logger, mode string) (*ComposeRoot, *backgroundJobs, CleanupFunc, error) {
	return initCompositionMinimalWithContext(context.Background(), cfg, log, mode, context.Background())
}

func initCompositionMinimalWithContext(ctx context.Context, cfg *config.Config, log *zap.Logger, mode string, parent context.Context) (*ComposeRoot, *backgroundJobs, CleanupFunc, error) {
	ctx, cancel := context.WithCancel(parent)

	hosts := append(cfg.Security.AllowedDownloadHosts, "youtube.com", "youtu.be", "www.youtube.com")
	security.SetAllowedHosts(hosts)
	log.Info("Configured download host whitelist", zap.Int("hosts_count", len(hosts)))

	dbs, err := InitDatabases(ctx, cfg, log)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}

	partialCleanup := func() {
		cancel()
		if dbs.Main != nil {
			if err := dbs.Main.Close(); err != nil {
				log.Error("Failed to close main database during cleanup", zap.Error(err))
			}
		}
	}

	if err := RunAllMigrations(dbs, log); err != nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	root, err := NewComposition(ctx, cfg, dbs, log)
	if err != nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("failed to build composition root: %w", err)
	}

	// PR-WORKER-RUNNER-INPROCESS-MIGRATION (July 2026): construct the
	// local broker BEFORE startBackgroundJobs so the job-runner sees a
	// non-nil CompletionPort and routes artifact-producing jobs
	// through CompleteWithArtifacts instead of failing with
	// "CompletionPort not wired". The WireServices caller reuses this
	// same broker instance via type-assertion for the full
	// appjobs.Broker surface needed by the internal worker handler.
	workerNodesRepo := workernodes.NewWorkerNodesRepository(dbs.DualPool.Writer)

	progressCoalesceWindow := 100 * time.Millisecond
	if cfg.Jobs.ProgressCoalesceWindow != "" {
		if parsed, perr := time.ParseDuration(cfg.Jobs.ProgressCoalesceWindow); perr == nil && parsed >= 0 {
			progressCoalesceWindow = parsed
		} else if perr != nil {
			log.Warn("invalid VELOX_PROGRESS_COALESCE_WINDOW; using default 100ms",
				zap.String("raw", cfg.Jobs.ProgressCoalesceWindow), zap.Error(perr))
		}
	}
	// Coalescer is ALWAYS constructed — when Window=0 the coalescer
	// runs in passthrough mode (every Take → immediate SetProgress),
	// which keeps the broker-side Progress + FlushJob paths uniform.
	progressSink := root.Jobs.Repo
	progressCoalescer := localbroker.NewProgressCoalescer(progressSink, localbroker.ProgressCoalesceConfig{
		Window: progressCoalesceWindow,
	}, log)

	broker, err := localbroker.New(localbroker.Deps{
		Jobs:      root.Jobs.Repo,
		Workers:   workerNodesRepo,
		Progress:  progressSink,
		Coalescer: progressCoalescer,
		Log:       log,
	})
	if err != nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("wire broker: %w", err)
	}
	// TIGHTENING (July 2026, godlike/07): explicit nil-broker fail-closed guard. Today
	// localbroker.New never returns (nil, nil), so this branch is dead code; it is a
	// permanent composition-time assertion that any future factory-method mutation
	// cannot smuggle a nil pointer into root.Jobs.Broker. If tripped, the partialCleanup
	// path is identical to the err branch so the operator's runbook surface is uniform.
	if broker == nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("wire broker: constructed broker is nil (Deps produced nil pointer despite err=nil)")
	}
	root.Jobs.Broker = broker

	// PR-COMPLETE-WORKER-BROAD-FIX (July 2026): wire the canonical
	// JobFinalizer into the broker at construction time so Path B
	// artifact-producing jobs (script.generate, image.generate.google,
	// books.process, lessons.process) can complete via the single-TX
	// finalization spine. root.Outbox.EventsRepo is available because
	// NewComposition already ran BuildProcessBundle which populated it.
	if root.Outbox != nil && root.Outbox.EventsRepo != nil && root.DB != nil && root.DB.DB != nil {
		assetCommitter := newCanonicalAssetCommitter(root.DB.DB, root.Outbox.EventsRepo, log)
		assetTx := assetfinalizer.NewAssetTxFinalizer(log, assetCommitter)
		if root.TextTracks != nil {
			assetTx.WithFanOut(root.TextTracks.FanOut)
		}
		finalizer := jobsfinalizer.New(root.DB.DB, root.Outbox.EventsRepo, assetTx, log)
		broker.WithFinalizer(finalizer)
		if root.Drive != nil && root.Drive.Publisher != nil {
			preparation := assetfinalizer.NewArtifactPreparation(drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log), log)
			broker.WithArtifactPreparation(preparation)
			log.Info("wired canonical ArtifactPreparation into local broker; staged artifacts publish through Drive before finalization")
		} else {
			log.Warn("canonical ArtifactPreparation NOT wired: Drive publisher unavailable; staged artifact jobs will fail closed")
		}
		// RenderingGen overlays resolve their parent video's Drive folder
		// below the already-resolved video folder (/video/.../overlay/).
		broker.WithArtifactFolderResolver(newSQLiteArtifactFolderResolver(root.DB.DB))
		log.Info("wired JobFinalizer into local broker at construction time (Path B artifact-producing jobs can now complete via CompleteWithArtifacts)")
	} else {
		// PR-COMPLETE-WORKER-FAIL-BOOT (Aug 2026): if the canonical
		// registry declares ANY artifact-producing job type with
		// ArtifactOwnershipWorkerSpine (CompleteWithArtifacts required),
		// a missing finalizer wiring is a BOOT FAILURE, not a WARN.
		//
		// A server that cannot complete artifact-producing jobs MUST
		// NOT start — the deferred failure (ErrFinalizerNotConfigured at
		// CompleteWithArtifacts time) would orphan every such job in
		// RUNNING until lease expiry (~5 min), creating a silent outage
		// that is much harder to diagnose than a boot-time crash loop.
		spineTypes := appjobs.SpineJobTypes()
		if len(spineTypes) > 0 {
			partialCleanup()
			return nil, nil, nil, fmt.Errorf(
				"FAIL BOOT: cannot wire JobFinalizer for artifact-producing jobs (root.Outbox=%v, root.Outbox.EventsRepo=%v, root.DB=%v). %d registered job type(s) require CompleteWithArtifacts: %v. Fix: ensure the outbox, events repo, and DB are available at composition time.",
				root.Outbox == nil, root.Outbox != nil && root.Outbox.EventsRepo == nil, root.DB == nil,
				len(spineTypes), spineTypes,
			)
		}
		log.Warn("JobFinalizer NOT wired into local broker (one or more deps nil — root.Outbox, root.Outbox.EventsRepo, or root.DB). No worker-spine job types registered, so this is safe.",
			zap.Bool("Outbox_nil", root.Outbox == nil))
	}

	jobs := startBackgroundJobs(ctx, cfg, dbs, root, log, mode)
	cleanup := buildCleanup(dbs, root, jobs, cancel, log)

	return root, jobs, cleanup, nil
}
