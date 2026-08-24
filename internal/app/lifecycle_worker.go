// Package app — lifecycle worker capability (PR-LIFECYCLE-SPLIT-BY-CAPABILITY, July 2026).
//
// Extracted from internal/app/lifecycle.go per AGENTS.md Pattern 5
// (capability-stable file split). Owns the worker-mode startup steps:
//
//   - job-scanner            (sqlitejobs.Scanner ticker)
//   - metrics-refresher      (appjobs.StartMetricsRefresher)
//   - voiceover-parent-aggregator (voiceoverjobs.NewParentAggregator)
//   - script-parent-aggregator   (scriptjobs.NewScriptParentAggregator)
//
// Sister file to lifecycle_scheduler.go + lifecycle_maintenance.go
// (the 3 capability files) + lifecycle_adapters.go (composition-root
// adapters). Sister file RETAINED from prior waves:
// lifecycle_job_runner.go (the job-runner step builder).
//
// The orchestrator (lifecycle.go::startBackgroundJobs) calls
// buildWorkerSteps when mode is "all" or "worker". The returned
// steps are appended to the startup plan BEFORE the scheduler +
// maintenance steps + the job-runner step.
package app

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jobs"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/jobs"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	scriptgenrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// workerDeps holds the composition-root dependencies required to build
// the worker-mode startup steps. Typed, not any: every field
// is a concrete pointer that callers must provide. Mirrors the
// jobRunnerDeps pattern in lifecycle_job_runner.go (PR4.8, June 2026).
// cfg is added so buildWorkerSteps can gate voiceover-by-default-off
// steps (canonical fix for the 2026-07-06 startup nil-pointer on
// aggregateOne crash: voiceover_enabled=false in config.yaml already
// says "don't run voiceover's background goroutines" — composition root
// must respect that flag, not just initialize-under-noisy-crash).
type workerDeps struct {
	root *wiring.ComposeRoot
	cfg  *config.Config
	log  *zap.Logger
}

// buildWorkerSteps returns the worker-mode StartupStep list:
// self-heartbeat + job-scanner + metrics-refresher + voiceover-parent-aggregator +
// script-parent-aggregator. The voiceover + script parent-aggregators
// MUST live under runWorker (NOT runScheduler) because the child job's
// terminal status only transitions when the job runner processes it —
// placing the aggregators under runScheduler would orphan parents on
// mode=worker machines (no aggregator ticks). Per the June 2026
// "Voiceover parent aggregator (Step 4 / micro-commit #5)" rationale.
func buildWorkerSteps(deps workerDeps) []StartupStep {
	var steps []StartupStep
	var scriptRunRepo scriptgen.RunRepository
	if deps.root != nil && deps.root.ObservabilityDB != nil {
		if repo, err := scriptgenrepo.NewSQLiteRunRepository(deps.root.ObservabilityDB.DB, deps.log); err == nil {
			scriptRunRepo = repo
		} else if deps.log != nil {
			deps.log.Warn("script parent aggregator: run repository unavailable", zap.Error(err))
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
			deps.log.Info("Self-heartbeat started (interval=25s)")
			return nil
		},
		Stop: func(_ context.Context) error { return nil },
	})

	jobsRepo := deps.root.Jobs.Repo
	jobsService := deps.root.Jobs.Service

	// Jobs system — Runner and Scanner. Reads from root.Jobs (PR4a).
	// The scanner + metrics refresher only need the jobs.Store
	// (*sqljobs.SQLiteStore), so the gate collapses to `jobsRepo != nil`.
	if jobsRepo != nil {
		sc := sqlitejobs.NewScanner(jobsRepo, deps.log)
		// Job scanner: optional background service.
		steps = append(steps, StartupStep{
			Name: "job-scanner", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("job-scanner", func() { sc.Start(startCtx, 5*time.Minute) })
				deps.log.Info("Job scanner started")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})

		// Metrics refresher: optional background service.
		jr := jobsRepo
		steps = append(steps, StartupStep{
			Name: "metrics-refresher", Required: false,
			Start: func(startCtx context.Context) error {
				appjobs.StartMetricsRefresher(startCtx, jr, 30*time.Second, deps.log)
				deps.log.Info("Metrics refresher started")
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
	if jobsService != nil && deps.cfg.Features.VoiceoverEnabled {
		voAgg := voiceoverjobs.NewParentAggregator(voiceoverjobs.AggregatorDeps{
			JobsSvc:      jobsService,
			Logger:       deps.log,
			PollInterval: 30 * time.Second,
		})
		steps = append(steps, StartupStep{
			Name: "voiceover-parent-aggregator", Required: false,
			Start: func(startCtx context.Context) error {
				voAgg.Start(startCtx)
				deps.log.Info("Voiceover parent aggregator started (interval=30s)")
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
			Logger:       deps.log,
			PollInterval: 30 * time.Second,
		})
		steps = append(steps, StartupStep{
			Name: "script-parent-aggregator", Required: false,
			Start: func(startCtx context.Context) error {
				scriptAgg.Start(startCtx)
				deps.log.Info("Script parent aggregator started (interval=30s)")
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	return steps
}
