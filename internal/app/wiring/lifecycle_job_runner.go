// Package app — typed job-runner lifecycle (PR4.8, June 2026).
package wiring

import (
	"context"
	"os"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/platform/jobs/local"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/procmetrics"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

type jobRunnerDeps struct {
	root *ComposeRoot
	cfg  *config.Config
	log  *zap.Logger
}

// clipRenderPhaseScope derives the payload scope that SPLITS the general pool
// from the dedicated clip.render settle pool: the general pool excludes
// render_phase=settle, the settle pool claims nothing else.
//
// ONE owner for the derivation — both pools build from this function, so they
// cannot disagree about which phase they own. The key and the value are read
// from the capability that WRITES the payload (cliprender.PayloadKeyRenderPhase
// and cliprender.RenderPhaseSettle). A literal here would be a second
// declaration of the same wire fact, and renaming the key in the capability
// would then silently void the guardrail (every settle continuation claimed by
// the general pool, no error, no log). Pinned by
// TestClipRenderPhaseScopeMatchesCapabilityContract.
func clipRenderPhaseScope() (job.PayloadMatch, job.PayloadNotMatch) {
	match := job.PayloadMatch{cliprender.PayloadKeyRenderPhase: string(cliprender.RenderPhaseSettle)}
	exclude := job.PayloadNotMatch{cliprender.PayloadKeyRenderPhase: string(cliprender.RenderPhaseSettle)}
	return match, exclude
}

const (
	jobRunnerPoolGeneral = "general"
	jobRunnerPoolSettle  = "clip-render-settle"
)

func workerDefault(cfg *config.Config) int {
	if cfg == nil || cfg.Jobs.MaxParallelPerProject < 4 {
		return 4
	}
	return cfg.Jobs.MaxParallelPerProject
}

func leaseTTLDefault(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Jobs.LeaseTTLSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(cfg.Jobs.LeaseTTLSeconds) * time.Second
}

// settleWorkerBudget returns the dedicated clip.render settle budget, or 0 when
// the split is disabled (feature off or budget <= 0 — the pre-guardrail
// behaviour, deliberately reachable so an operator can roll the split back
// without a code change).
func settleWorkerBudget(cfg *config.Config) int {
	if cfg == nil || !cfg.Features.ClipRenderEnabled {
		return 0
	}
	if cfg.Jobs.ClipRenderSettleWorkers <= 0 {
		return 0
	}
	return cfg.Jobs.ClipRenderSettleWorkers
}

// jobRunnerBaseConfig is the shared runner configuration (poll policy, backoff,
// lease TTL, notifier) before the per-pool type/phase scoping is applied.
func jobRunnerBaseConfig(deps jobRunnerDeps) appjobs.RunnerConfig {
	pollMaxBackoff := 60 * time.Second
	if deps.cfg.Jobs.PollMaxBackoff != "" {
		if parsed, perr := time.ParseDuration(deps.cfg.Jobs.PollMaxBackoff); perr == nil && parsed > 0 {
			pollMaxBackoff = parsed
		} else if perr != nil {
			deps.log.Warn("invalid VELOX_POLL_MAX_BACKOFF; using default 60s",
				zap.String("raw", deps.cfg.Jobs.PollMaxBackoff), zap.Error(perr))
		}
	}
	pollJitter := deps.cfg.Jobs.PollJitterFraction
	if pollJitter < 0 {
		pollJitter = 0
	} else if pollJitter > 1 {
		pollJitter = 1
	}
	pollConsecutiveEmpty := deps.cfg.Jobs.PollConsecutiveEmptyBeforeBackoff
	if pollConsecutiveEmpty < 0 {
		pollConsecutiveEmpty = 0
	}

	return appjobs.RunnerConfig{
		Workers:   workerDefault(deps.cfg),
		PollEvery: 2 * time.Second,
		LeaseTTL:  leaseTTLDefault(deps.cfg),
		JobTypes:  nil,
		Backoff: appjobs.BackoffConfig{
			MaxBackoff:                pollMaxBackoff,
			JitterFraction:            pollJitter,
			ConsecutiveEmptyThreshold: pollConsecutiveEmpty,
		},
		Notifier: deps.root.Jobs.Repo,
	}
}

// buildJobRunnerObserver builds the RunObserver for one pool. It is built per
// pool so each pool owns its recorder; abandoned-run recovery is idempotent, so
// the second pool observing nothing to recover is a no-op.
func buildJobRunnerObserver(deps jobRunnerDeps) *kernobs.RunObserver {
	var recorder kernobs.Recorder
	if deps.root.ObservabilityDB != nil && deps.root.ObservabilityDB.DB != nil {
		recorder = obsmetrics.NewSQLiteRecorderWithLogger(deps.root.ObservabilityDB.DB, deps.log)
		if reconciler, ok := recorder.(kernobs.AbandonedRunReconciler); ok {
			if _, err := reconciler.RecoverAbandoned(context.Background(), time.Now().UTC()); err != nil {
				deps.log.Warn("observability abandoned-run recovery failed", zap.Error(err))
			}
		}
	} else {
		deps.log.Warn("observability recorder unavailable; using metrics-only projection")
	}
	return kernobs.NewRunObserverWithCollector(recorder, obsmetrics.NewRunReportsCollector())
}

// buildJobRunnerResourceSampler builds the run resource sampler for one pool
// (nil when unavailable), plus the host stamped on each observation.
func buildJobRunnerResourceSampler(deps jobRunnerDeps) (kernobs.RunResourceSampler, string) {
	if deps.root.DB == nil || deps.root.DB.DB == nil {
		return nil, ""
	}
	store, err := perfstore.NewResourceStore(deps.root.DB.DB)
	if err != nil {
		deps.log.Warn("resource sampler store unavailable; run resource telemetry disabled", zap.Error(err))
		return nil, ""
	}
	sampler, err := perfstore.NewSampler(procmetrics.New(procmetrics.Options{}), store)
	if err != nil {
		deps.log.Warn("resource sampler unavailable; run resource telemetry disabled", zap.Error(err))
		return nil, ""
	}
	host, _ := os.Hostname()
	return sampler, host
}

// newJobRunnerPool constructs one pool from an explicit config. Registry,
// dispatcher and service orchestration remain root-owned; persistence-specific
// completion classification is injected at the platform boundary.
func newJobRunnerPool(deps jobRunnerDeps, name string, cfg appjobs.RunnerConfig) *appjobs.Runner {
	runner := appjobs.NewRunner(deps.root.Jobs.Repo, deps.root.Jobs.Dispatcher, deps.log, cfg)
	runner.WithRegistry(appjobs.Compose())
	runner.WithClaimSnapshotter(deps.root.Jobs.Repo)
	if deps.root.Jobs.Broker != nil {
		// The raw local broker remains on JobsBundle for server-side APIs that
		// need its full concrete surface. Worker completion receives a narrow
		// classified port so SQLite BUSY/LOCKED never leaks into the jobs
		// capability as a driver-specific error shape.
		runner.WithBroker(localbroker.NewClassifiedCompletionPort(deps.root.Jobs.Broker))
	}
	if deps.root.Jobs.JobLedger != nil {
		runner.WithJobRegistry(deps.root.Jobs.JobLedger)
	}
	runner.WithObserver(buildJobRunnerObserver(deps))
	if sampler, host := buildJobRunnerResourceSampler(deps); sampler != nil {
		runner.WithResourceSampler(sampler, host)
	}
	if deps.cfg.Features.ClipRenderEnabled {
		if clipAgg := clipRenderParentAggregator(deps.root, deps.log); clipAgg != nil {
			runner.WithParentCompletionNotifier(&clipRenderParentNotifier{agg: clipAgg})
		}
	}
	return runner
}

func jobRunnerRootReady(deps jobRunnerDeps) bool {
	return deps.root != nil &&
		deps.root.Jobs.Service != nil &&
		deps.root.Jobs.Dispatcher != nil &&
		deps.root.Jobs.Repo != nil
}

// buildJobRunner constructs the GENERAL pool: every job type, but — when the
// settle budget is enabled — NOT the clip.render settle phase. The exclusion is
// the load-bearing half of the guardrail: without it the general pool would
// still claim the settle continuations it exists to avoid and a slow GPU
// backlog would keep starving unrelated jobs.
func buildJobRunner(deps jobRunnerDeps) *appjobs.Runner {
	if !jobRunnerRootReady(deps) {
		return nil
	}
	cfg := jobRunnerBaseConfig(deps)
	deps.log.Info("Job runner created",
		zap.String("pool", jobRunnerPoolGeneral),
		zap.Int("workers", cfg.Workers),
		zap.Duration("poll_max_backoff", cfg.Backoff.MaxBackoff),
		zap.Float64("poll_jitter_fraction", cfg.Backoff.JitterFraction),
		zap.Int("poll_consecutive_empty_threshold", cfg.Backoff.ConsecutiveEmptyThreshold))
	if settleWorkerBudget(deps.cfg) > 0 {
		_, exclude := clipRenderPhaseScope()
		cfg.PayloadNotMatch = exclude
	}
	return newJobRunnerPool(deps, jobRunnerPoolGeneral, cfg)
}

// buildClipRenderSettleRunner constructs the DEDICATED clip.render settle pool,
// or nil when the split is disabled. It claims only clip.render jobs whose
// payload carries render_phase=settle, so a remote render waiting on
// RenderingGen can never occupy a general worker slot.
func buildClipRenderSettleRunner(deps jobRunnerDeps) *appjobs.Runner {
	if !jobRunnerRootReady(deps) {
		return nil
	}
	budget := settleWorkerBudget(deps.cfg)
	if budget <= 0 {
		return nil
	}
	cfg := jobRunnerBaseConfig(deps)
	cfg.Workers = budget
	cfg.JobTypes = []string{cliprender.TypeClipRender}
	match, _ := clipRenderPhaseScope()
	cfg.PayloadMatch = match
	deps.log.Info("clip.render settle pool created",
		zap.String("pool", jobRunnerPoolSettle),
		zap.Int("workers", budget),
		zap.String("phase", string(cliprender.RenderPhaseSettle)),
		zap.String("excluded_from", jobRunnerPoolGeneral))
	return newJobRunnerPool(deps, jobRunnerPoolSettle, cfg)
}

type jobRunnerPool struct {
	name    string
	runner  *appjobs.Runner
	workers int
}

func buildJobRunnerStep(deps jobRunnerDeps) *StartupStep {
	general := buildJobRunner(deps)
	if general == nil {
		return nil
	}
	pools := []jobRunnerPool{{
		name:    jobRunnerPoolGeneral,
		runner:  general,
		workers: workerDefault(deps.cfg),
	}}
	if settle := buildClipRenderSettleRunner(deps); settle != nil {
		pools = append(pools, jobRunnerPool{
			name:    jobRunnerPoolSettle,
			runner:  settle,
			workers: settleWorkerBudget(deps.cfg),
		})
	}
	disp := deps.root.Jobs.Dispatcher
	return &StartupStep{
		Name: "job-runner", Required: true,
		Start: func(startCtx context.Context) error {
			disp.Freeze()
			for _, pool := range pools {
				pool := pool
				concurrent.SafeGo("job-runner-"+pool.name, func() { pool.runner.Start(startCtx) })
			}
			for _, pool := range pools {
				deps.log.Info("Job runner pool started after full wiring",
					zap.String("pool", pool.name), zap.Int("workers", pool.workers))
			}
			return nil
		},
		Stop: func(_ context.Context) error { return nil },
	}
}
