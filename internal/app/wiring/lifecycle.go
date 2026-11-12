// Package app — background lifecycle (PR4: takes *ComposeRoot).
//
// Before PR4 this file took the legacy `*services` struct. After PR4 it
// takes *ComposeRoot (the per-bundle decomposition). The body is the same
// `startBackgroundJobs(ctx, cfg, dbs, root, log, mode) (*backgroundJobs)`
// pattern as before, but reads from root.Domains, root.Repos, root.Process,
// root.Outbox, root.Jobs, root.Domains.RealtimeService, etc.
//
// PR4.8 (June 2026): the typed job-runner lifecycle (construction +
// START/STOP closure) now lives in internal/app/wiring/lifecycle/jobrunner.go;
// this file adapts ComposeRoot dependencies into that typed module. startBackgroundJobs is now
// orchestrator-only — the "var jobRunner := appjobs.NewRunner(...) +
// inline StartupStep literal" pattern is replaced by a single typed
// append at the end of the plan. See Wave 15 pending #2 in
// architecture/current.yaml for the canonical record.
//
// PR-LIFECYCLE-SPLIT-BY-CAPABILITY (2026-07-04, deadline 2026-08-15):
// the original 1191-LoC go was split into 5 capability-stable
// files per AGENTS.md Pattern 5 (one file per capability, no God files):
//
//   - go              (THIS FILE, thin orchestrator, ~280 LoC)
//   - lifecycle_worker.go       (worker capability, ~140 LoC)
//   - lifecycle_scheduler.go    (scheduler capability, ~180 LoC)
//   - lifecycle/maintenance.go (maintenance capability; typed subsystem deps)
//   - lifecycle_adapters.go     (composition-root adapters, ~400 LoC)
//
// Sister files RETAINED (NOT touched by this PR):
//   - lifecycle_sweepers.go     (259 LoC, the 4 sweeper helpers — research/gemma/clip-dedup/vlm-autotag)
//   - lifecycle/jobrunner.go   (worker-pool, scheduler and reporter lifecycle)
//   - server_lifecycle.go       (251 LoC, the server-lifecycle runtime)
//
// The orchestrator (startBackgroundJobs) is now a thin function that
// computes the mode flags (runWorker / runScheduler / runMaintenance)
// and delegates to the 3 new capability builders (worker / scheduler /
// maintenance) + lifecycle.BuildJobRunnerStep. The channel-monitor
// pointer is surfaced from buildSchedulerSteps for graceful teardown
// in shutdown.go (channelMonitor.Stop is the only explicit shutdown
// call in the lifecycle-runtime-ownership wave, June 2026).
//
// Three Qdrant-driven background-cleanup steps were removed:
//   - qdrant-stale-cleaner
//   - qdrant-ghost-sweeper
//   - qdrant-health-monitor
//
// PR-QDRANT-FINAL-DECISION (2026-07-04, decision: live, forward-cite
// PR-QDRANT-FINAL-DECISION-WAVE30-COORDINATION): Qdrant is the canonical
// data-path vector store (Wave 30 / QDRANT-003 + QDRANT-005). The data-path
// is owned by composition.go::ProcessBundle (8 typed Qdrant fields:
// CollectionManager, QdrantDeleter, QdrantRuntime, VectorSvc,
// LocatorCleaner, QdrantClient, QdrantHealthProbe, QdrantSearcher) +
// build_process_qdrant.go; the qdrant-collection EnsureSchema step is a
// REAL production startup step in wire_services.go. Embedding persistence
// is dual-write: SQLite (canonical metadata store) + Qdrant (derived
// projection per godlike/06 qdrant-projection). The 3 background-cleanup
// steps listed above were removed earlier (godlike/07 no-fake-availability:
// they no longer represented the canonical background-cleanup topology);
// Wave 30 BACKFILL will re-introduce them with the canonical scope pinned
// to the new ProcessBundle / wire_services.go
//
// The returned *backgroundJobs handle is consumed by shutdown.go for
// graceful teardown (channel-monitor.Stop, drive-sync-scheduler.Stop).
//
// Mode mapping (matches previous semantics):
//   - "all"        → runWorker + runScheduler + runMaintenance
//   - "worker"     → runWorker only
//   - "scheduler"  → runScheduler only
//   - "maintenance"→ runMaintenance only
//   - ""           → runWorker only (back-compat with InitCore callers)
package wiring

import (
	"context"

	clipswiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/clips"
	lifecyclewiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/lifecycle"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/monitor"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"

	"go.uber.org/zap"
)

// StartupStep is a root alias kept for the composition root files that
// still declare steps in package-private shape. Canonical ownership lives
// in wiring/lifecycle (lifecycle-runtime-ownership closure).
type StartupStep = lifecyclewiring.StartupStep

// jobRunnerDeps is the ComposeRoot-facing input. The lifecycle module receives
// only its explicit ports through lifecycleJobRunnerDeps.
type jobRunnerDeps struct {
	root *ComposeRoot
	cfg  *config.Config
	log  *zap.Logger
}

func lifecycleJobRunnerDeps(deps jobRunnerDeps) lifecyclewiring.JobRunnerDeps {
	cfg := deps.cfg
	if cfg == nil {
		cfg = &config.Config{}
	}
	log := deps.log
	if log == nil {
		log = zap.NewNop()
	}
	out := lifecyclewiring.JobRunnerDeps{Cfg: cfg, Log: log}
	if deps.root == nil || deps.root.Jobs == nil {
		return out
	}
	jobs := deps.root.Jobs
	out.Execution.Dispatcher, out.Execution.ServiceReady = jobs.Dispatcher, jobs.Service != nil
	if jobs.Repo != nil {
		out.Execution.Repo = jobs.Repo
		out.Execution.QueueNotifier = jobs.Repo
		out.Execution.ClaimSnapshotter = jobs.Repo
	}
	out.Execution.Broker, out.Execution.JobLedger = jobs.Broker, jobs.JobLedger
	if deps.root.ObservabilityDB != nil {
		out.Telemetry.ObservabilityDB = deps.root.ObservabilityDB.DB
	}
	if deps.root.DB != nil {
		out.Telemetry.PerformanceDB = deps.root.DB.DB
	}
	if cfg.Features.ClipRenderEnabled {
		if agg := clipRenderParentAggregator(deps.root, log); agg != nil {
			out.Execution.ParentCompletionNotifier = clipswiring.NewClipRenderParentNotifier(agg)
		}
	}
	return out
}

// lifecycleSchedulerDeps adapts scheduler runtime services from the composition root.
func lifecycleSchedulerDeps(cfg *config.Config, root *ComposeRoot, log *zap.Logger) lifecyclewiring.SchedulerDeps {
	deps := lifecyclewiring.SchedulerDeps{Config: cfg, Log: log}
	if root == nil {
		return deps
	}
	if root.DB != nil {
		deps.MainDB = root.DB.DB
	}
	if root.Jobs != nil {
		deps.JobsService = root.Jobs.Service
	}
	if root.AI != nil {
		deps.OllamaClient = root.AI.OllamaClient
	}
	deps.YoutubeClipsEnabled = root.Domains != nil && root.Domains.YoutubeClipService != nil
	return deps
}

// lifecycleWorkerDeps adapts worker-owned runtime services from the composition root.
// The lifecycle package receives only the job ports, database handle and shared
// clip aggregator it needs; it cannot inspect unrelated ComposeRoot bundles.
func lifecycleWorkerDeps(cfg *config.Config, root *ComposeRoot, log *zap.Logger) lifecyclewiring.WorkerDeps {
	deps := lifecyclewiring.WorkerDeps{Log: log}
	if cfg != nil {
		deps.VoiceoverEnabled = cfg.Features.VoiceoverEnabled
		deps.ClipRenderEnabled = cfg.Features.ClipRenderEnabled
	}
	if root == nil {
		return deps
	}
	if root.Jobs != nil {
		deps.JobsRepo = root.Jobs.Repo
		deps.JobsService = root.Jobs.Service
	}
	if root.ObservabilityDB != nil {
		deps.ObservabilityDB = root.ObservabilityDB.DB
	}
	if deps.ClipRenderEnabled {
		deps.ClipAggregator = clipRenderParentAggregator(root, log)
	}
	return deps
}

// lifecycleMaintenanceDeps adapts the composed root to the maintenance
// subsystem's explicit dependencies. The subsystem receives database handles
// and service ports only; it never sees ComposeRoot or bundle internals.
func lifecycleMaintenanceDeps(cfg *config.Config, root *ComposeRoot, log *zap.Logger) lifecyclewiring.MaintenanceDeps {
	deps := lifecyclewiring.MaintenanceDeps{Config: cfg, Log: log}
	if root == nil {
		return deps
	}
	if root.Jobs != nil {
		deps.Services.Jobs = root.Jobs.Service
		deps.Services.StaleJobs = root.Jobs.Repo
	}
	if root.Maint != nil {
		deps.Services.EntityImageRecertification = root.Maint.EntityImageRecertification
	}
	if root.CacheDB != nil {
		deps.Databases.ResearchCache = root.CacheDB.DB
	}
	if root.AI != nil {
		deps.Services.MemoryRepo = root.AI.MemoryRepo
	}
	if root.ObservabilityDB != nil {
		deps.Databases.Observability = root.ObservabilityDB.DB
	}
	if root.DB != nil {
		deps.Databases.Operational = root.DB.DB
	}
	if root.Outbox != nil {
		deps.Services.OutboxDispatcher = root.Outbox.Dispatcher
	}
	if root.Drive != nil {
		deps.Services.DriveLifecycle = root.Drive.Lifecycle
	}
	deps.Services.Autotag = root.Domains.AutotagService

	committer, err := canonicalCommitterForRoot(root, log)
	if err != nil {
		deps.Media.ResolutionError = err
	} else if committer != nil {
		deps.Media.DuplicateReader = mediaDuplicateGroupReaderFromCommitter(committer)
		deps.Media.Retirer = assetspersistence.CanonicalAssetSoftDeleter(committer)
	}
	return deps
}

// ErrCapabilityDisabled is the typed sentinel surface for startup
// steps that are intentionally NOT running per operator-facing policy
// (e.g. capability pending a future phase, feature-flag gated off,
// dependency not yet wired). Per godlike/07 no-fake-availability:
// a step returning nil while loading NOTHING is a fake success —
// the operator's view of the system would otherwise omit the
// suppressed capability. Returning ErrCapabilityDisabled from a
// Required:false step preserves the "startup survives" semantics
// (server_lifecycle.Start's optional-failure branch in
// server_lifecycle.go log+continues on any non-nil error) while
// making the disabled state typed-queryable via
// `errors.Is(step.Err(), ErrCapabilityDisabled)` from any caller
// wanting to enumerate disabled-at-startup
//
// Wire shape: errors.New (godlike/07 typed-error contract —
// composable via fmt.Errorf("%w"), reachable via errors.Is from
// any wrapping context). Single surface, no typed-data envelope:
// the actionable context lives in the wrap message (e.g.
// "yt-cache-prewarm: capability disabled pending Phase 2+").
var ErrCapabilityDisabled = lifecyclewiring.ErrCapabilityDisabled

// backgroundJobs holds references to services started by startBackgroundJobs
// that need explicit Stop() during shutdown, plus the startup plan that
// defers ALL goroutine launches to serverLifecycle.Start.
//
// After lifecycle-runtime-ownership (June 2026), only channelMonitor needs
// explicit Stop (via buildCleanup in shutdown.go). All other services
// (job runner, scanner, sweepers) stop via context cancellation.
//
// The startupPlan field encodes every background worker, scanner, monitor,
// sweeper, and the job runner as a StartupStep so zero goroutines start
// during composition. The job-runner StartupStep is built by
// internal/app/wiring/lifecycle/jobrunner.go::BuildJobRunnerStep (PR4.8) and
// is appended LAST in the plan (asserted by TestLifecycle_JobRunnerLast
// in internal/app/lifecycle_test.go).
type backgroundJobs struct {
	channelMonitor *monitor.ChannelMonitor
	// startupPlan is the ordered list of services to start during
	// serverLifecycle.Start. The job runner is the last required step.
	startupPlan []StartupStep
}

// startBackgroundJobs creates the per-mode background workers and returns a
// startup plan WITHOUT launching any goroutines. All runtime startups are
// deferred to serverLifecycle.Start via the returned StartupStep list.
//
// PR-LIFECYCLE-SPLIT-BY-CAPABILITY (2026-07-04): the body is now a thin
// orchestrator that delegates to worker and scheduler builders plus the
// lifecycle module's maintenance builder and the existing
// buildJobRunnerStep. The startup plan ordering (left-to-right):
//
//  1. Worker steps (optional) — job-scanner + metrics-refresher +
//     voiceover-parent-aggregator + script-parent-aggregator
//     (see lifecycle_worker.go)
//  2. Scheduler steps (optional) — channel-monitor (with FASE 3.7
//     composition-root adapter wiring) + yt-cache-prewarm +
//     yt-nightly-prewarm (see lifecycle_scheduler.go)
//  3. Maintenance steps (optional) — maintenance-scheduler-maintenance +
//     maintenance-scheduler-backup + 4 sweepers (research + gemma +
//     clip-dedup + vlm-autotag) + deletion-reconciler + orphan-sweeper
//     (see lifecycle/maintenance.go)
//  4. Job runner (REQUIRED, always last) — see lifecycle/jobrunner.go
func startBackgroundJobs(ctx context.Context, cfg *config.Config, dbs *Databases, root *ComposeRoot, log *zap.Logger, mode string) *backgroundJobs {
	if root == nil {
		log.Warn("startBackgroundJobs called with nil ComposeRoot — skipping")
		return &backgroundJobs{}
	}

	if !cfg.Jobs.EnableBackgroundJobs {
		log.Info("Background jobs disabled via config")
		return &backgroundJobs{}
	}

	runWorker := mode == "all" || mode == "worker"
	runScheduler := mode == "all" || mode == "scheduler"
	runMaintenance := mode == "all" || mode == "maintenance"

	log.Info("Background jobs mode", zap.String("mode", mode),
		zap.Bool("worker", runWorker),
		zap.Bool("scheduler", runScheduler),
		zap.Bool("maintenance", runMaintenance))

	var steps []StartupStep
	var channelMon *monitor.ChannelMonitor

	if runWorker {
		if step := buildOllamaWarmStep(cfg, root, log); step != nil {
			steps = append(steps, *step)
		}
		steps = append(steps, lifecyclewiring.BuildWorkerSteps(lifecycleWorkerDeps(cfg, root, log))...)
	}
	if runScheduler {
		// Use plain `=` (not `:=`) so the outer channelMon is reassigned,
		// not shadowed by a new local. The `:=` would create a new local
		// channelMon inside the if-block that is assigned but never read,
		// which Go's "declared and not used" check would flag.
		var schedSteps []StartupStep
		channelMon, schedSteps = lifecyclewiring.BuildSchedulerSteps(lifecycleSchedulerDeps(cfg, root, log))
		steps = append(steps, schedSteps...)
	}
	if runMaintenance {
		steps = append(steps, lifecyclewiring.BuildMaintenanceSteps(lifecycleMaintenanceDeps(cfg, root, log))...)
	}
	if step := buildPreparationCoordinatorStep(jobRunnerDeps{root: root, cfg: cfg, log: log}); step != nil {
		steps = append(steps, *step)
	}
	// Deferred-job scheduler: promotes due SCHEDULED jobs to QUEUED under the
	// admission policy. Scheduler-mode work (runScheduler), so it is omitted
	// in worker-only mode.
	if runScheduler {
		if step := lifecyclewiring.BuildJobSchedulerStep(lifecycleJobRunnerDeps(jobRunnerDeps{root: root, cfg: cfg, log: log})); step != nil {
			steps = append(steps, *step)
		}
	}

	// Job runner: REQUIRED, always LAST in the plan.
	// Construction + step closure delegated to lifecycle.BuildJobRunnerStep. The frozen dispatcher
	// guarantees no further handlers can register once Start is invoked;
	// the runner exits via context cancellation in serverLifecycle.Stop.
	// Returns nil when the jobs bundle lacks Service / Dispatcher / Repo
	// (partial-deploy safety net): the runner is then skipped, not failed.
	if step := lifecyclewiring.BuildJobRunnerStep(lifecycleJobRunnerDeps(jobRunnerDeps{root: root, cfg: cfg, log: log})); step != nil {
		steps = append(steps, *step)
	}

	return &backgroundJobs{
		channelMonitor: channelMon,
		startupPlan:    steps,
	}
}

// LifecycleDeps holds the dependencies needed to create a lifecycle service.
//
// FASE 9 Step 7 (June 2026): DriveUploader (*drive.Uploader) migrated to
// DriveAdmin (drive.Admin port). Callers pass *drive.Uploader which satisfies
// drive.Admin structurally. NewLifecycleFromDeps extracts a drive.Reader via
// safe type-assertion for the verifier + reconcile services.
//
// F2.7 (June 2026): DriveAdmin REMOVED. Publisher (delivery.Publisher) is
// the canonical Pattern 0 port for Drive writes; DriveReader
// (drive.Reader) is the canonical read-side port for the reconcile
// service's DriveIsNotTrashed check. The composition root threads
// both directly — no unsafe type-assertion needed.
type AssetLifecycleDeps struct {
	Registry      artifacts.Registry
	Publisher     delivery.Publisher
	AssetIndex    *assetindex.Service
	DriveVerifier artifacts.DriveVerifier
	DriveReader   drive.Reader
	Finalizer     *artifacts.Finalizer
	Store         lifecycle.AssetRecordStore
}

// NewLifecycleFromDeps creates a lifecycle Service using the provided dependencies.
//
// FASE 9 Step 7: DriveAdmin (drive.Admin) replaces the former *drive.Uploader.
// A drive.Reader is extracted via safe type-assertion for verifier + reconcile.
// All production callers pass *drive.Uploader which satisfies both interfaces.
//
// F2.7 (June 2026): DriveAdmin REMOVED. NewLifecycleFromDeps now takes
// Publisher (delivery.Publisher) + DriveReader (drive.Reader) — the
// application-layer holds ZERO references to the legacy drive.Admin
// port. Service uses Publisher for Drive writes (closes P0
// #7) and DriveReader for the read-only reconcile/verify surface.
func NewLifecycleFromDeps(
	deps *AssetLifecycleDeps,
	log *zap.Logger,
) *lifecycle.Service {
	driveReader := deps.DriveReader

	if deps.DriveVerifier == nil && driveReader != nil {
		deps.DriveVerifier = drive.NewDriveVerifierAdapter(driveReader)
	}

	if deps.Finalizer == nil && deps.Registry != nil && deps.DriveVerifier != nil && deps.AssetIndex != nil {
		deps.Finalizer = artifacts.NewFinalizerWithPorts(
			deps.Registry,
			deps.DriveVerifier,
			newArtifactAssetIndexAdapter(deps.AssetIndex),
			semantic.NewArtifactsMetadataAdapter(),
			log,
		)
	}

	if deps.Store == nil && deps.Registry != nil {
		deps.Store = lifecycle.NewRegistryStoreAdapter(deps.Registry)
	}

	return lifecycle.NewService(lifecycle.ServiceDeps{
		Store:       deps.Store,
		Publisher:   deps.Publisher,
		DriveReader: driveReader,
		Registry:    deps.Registry,
		AssetIndex:  deps.AssetIndex,
		Finalizer:   deps.Finalizer,
		Log:         log,
	}, lifecycle.DefaultConfig())
}
