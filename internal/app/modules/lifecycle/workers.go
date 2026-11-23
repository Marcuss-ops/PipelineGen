package lifecycle

import (
	"context"
	"database/sql"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jobs"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/jobs"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	scriptgenrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// WorkerDeps contains the runtime ports required by worker startup steps.
type WorkerDeps struct {
	JobsRepo          *sqlitejobs.SQLiteStore
	JobsService       *appjobs.Service
	ObservabilityDB   *sql.DB
	VoiceoverEnabled  bool
	ClipRenderEnabled bool
	ClipAggregator    *cliprender.ParentAggregator
	Log               *zap.Logger
}

// buildWorkerSteps returns the worker-mode StartupStep list:
// self-heartbeat + job-scanner + metrics-refresher + voiceover-parent-aggregator +
// script-parent-aggregator. The voiceover + script parent-aggregators
// MUST live under runWorker (NOT runScheduler) because the child job's
// terminal status only transitions when the job runner processes it —
// placing the aggregators under runScheduler would orphan parents on
// mode=worker machines (no aggregator ticks). Per the June 2026
// "Voiceover parent aggregator (Step 4 / micro-commit #5)" rationale.
func BuildWorkerSteps(deps WorkerDeps) []StartupStep {
	var steps []StartupStep
	var scriptRunRepo scriptgen.RunRepository
	if deps.ObservabilityDB != nil {
		if repo, err := scriptgenrepo.NewSQLiteRunRepository(deps.ObservabilityDB, deps.Log); err == nil {
			scriptRunRepo = repo
		} else if deps.Log != nil {
			deps.Log.Warn("script parent aggregator: run repository unavailable", zap.Error(err))
		}
	}

	// Self-heartbeat (July 2026): when the server runs in --mode all or
	// --mode worker without an external cmd/worker, the BrokerLastHeartbeat
	// atomic stays at zero forever → BrokerHeartbeatAge() returns
	// math.MaxInt64 → /ready returns 503 "broker heartbeat stale".
	// This goroutine calls appjobs.SetBrokerAlive() every 25s so the
	// built-in worker mode satisfies the liveness probe without needing
	// a separate worker binary. The 3.6x safety margin (25s tick vs 60s
	// staleness threshold) matches the HeartbeatLoop convention.
	steps = append(steps, StartupStep{
		Name: "self-heartbeat", Required: true,
		Start: func(startCtx context.Context) error {
			concurrent.SafeGo("self-heartbeat", func() {
				ticker := time.NewTicker(25 * time.Second)
				defer ticker.Stop()
				appjobs.SetBrokerAlive() // seed immediately
				for {
					select {
					case <-startCtx.Done():
						return
					case <-ticker.C:
						appjobs.SetBrokerAlive()
					}
				}
			})
			deps.Log.Info("Self-heartbeat started (interval=25s)")
			return nil
		},
		Stop: func(_ context.Context) error { return nil },
	})

	jobsRepo := deps.JobsRepo
	jobsService := deps.JobsService

	// Jobs system — Runner and Scanner. Reads from root.Jobs (PR4a).
	// The scanner + metrics refresher only need the jobs.Store
	// (*sqljobs.SQLiteStore), so the gate collapses to `jobsRepo != nil`.
	if jobsRepo != nil {
		sc := sqlitejobs.NewScanner(jobsRepo, deps.Log)
		// Job scanner: optional background service.
		steps = append(steps, StartupStep{
			Name: "job-scanner", Required: false,
			Start: func(startCtx context.Context) error {
				// The scanner still honors the configured lease TTL; this only
				// bounds detection latency once that TTL has elapsed.
				concurrent.SafeGo("job-scanner", func() { sc.Start(startCtx, 15*time.Second) })
				deps.Log.Info("Job scanner started")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})

		// Metrics refresher: optional background service.
		jr := jobsRepo
		steps = append(steps, StartupStep{
			Name: "metrics-refresher", Required: false,
			Start: func(startCtx context.Context) error {
				appjobs.StartMetricsRefresher(startCtx, jr, 30*time.Second, deps.Log)
				deps.Log.Info("Metrics refresher started")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Voiceover parent aggregator (Step 4 / micro-commit #5, June 2026):
	// re-finalises parent jobs once all children have reached terminal
	// status. MUST live under runWorker (NOT runScheduler) because the
	// child job's terminal status only transitions when the job runner
	// processes it — placing the aggregator under runScheduler would
	// orphan parents on mode=worker machines (no aggregator ticks).
	//
	// CANONICAL FIX (2026-07-06 startup nil-pointer regression):
	// The aggregator depends on a stable AggregatorJobsService contract
	// (JobsSvc.Get returning ErrNotFound OR a typed (job, nil) row).
	// A freshly-booted server can SIGSEGV in aggregateOne if orphan
	// parent references exist in the DB after a prior crash. The
	// canonical fix per reviewer recommendation (Option B — gate at
	// composition time) is to respect cfg.Features.VoiceoverEnabled:
	// if voiceover subsystem is operator-disabled, do NOT even
	// construct the aggregator. config.yaml default is false; the
	// stock pipeline user's task is voiceover-unrelated. Step is
	// also still guarded by recover() in parent_aggregator.go as
	// defense-in-depth (PR-VO-AGGREGATEORPHAN-GUARD forward-pointer
	// upgrades the broker to typed ErrChildNotFound).
	if jobsService != nil && deps.VoiceoverEnabled {
		voAgg := voiceoverjobs.NewParentAggregator(voiceoverjobs.AggregatorDeps{
			JobsSvc:      jobsService,
			Logger:       deps.Log,
			PollInterval: 30 * time.Second,
		})
		steps = append(steps, StartupStep{
			Name: "voiceover-parent-aggregator", Required: false,
			Start: func(startCtx context.Context) error {
				voAgg.Start(startCtx)
				deps.Log.Info("Voiceover parent aggregator started (interval=30s)")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Script parent aggregator (Commit 4 P0 #4 audit, July 2026):
	// lifecycle-owns the script.generate parent aggregator with the
	// server's runtime context (signal.NotifyContext). Previously
	// started during composition with context.Background() — the
	// goroutine had no shutdown signal and leaked on re-composition.
	// Mirrors the voiceover-parent-aggregator pattern above.
	if jobsService != nil {
		scriptAgg := scriptjobs.NewScriptParentAggregator(scriptjobs.ScriptAggregatorDeps{
			JobsSvc:      jobsService,
			RunRepo:      scriptRunRepo,
			Logger:       deps.Log,
			PollInterval: 30 * time.Second,
		})
		steps = append(steps, StartupStep{
			Name: "script-parent-aggregator", Required: false,
			Start: func(startCtx context.Context) error {
				scriptAgg.Start(startCtx)
				deps.Log.Info("Script parent aggregator started (interval=30s)")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Clip render parent RECOVERY sweeper. The healthy path is event-driven: the
	// settle child's terminal commit reports its parent directly (see
	// clip_render_parent_completion.go and Worker.WithParentCompletionNotifier),
	// so a finished clip flips its parent in the same instant. This ticker exists
	// only for the case that event cannot cover — a crash between the child
	// commit and the notification — which is why it runs at the recovery cadence
	// rather than as the primary completion mechanism.
	if jobsService != nil && deps.ClipRenderEnabled {
		clipAgg := deps.ClipAggregator
		if clipAgg == nil {
			deps.Log.Warn("Clip render parent recovery sweeper unavailable (jobs service missing)")
		} else {
			steps = append(steps, StartupStep{
				Name: "clip-render-parent-recovery-sweeper", Required: false,
				Start: func(startCtx context.Context) error {
					clipAgg.Start(startCtx)
					deps.Log.Info("Clip render parent recovery sweeper started",
						zap.Duration("interval", cliprender.DefaultParentAggregationInterval))
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}
	}

	return steps
}
