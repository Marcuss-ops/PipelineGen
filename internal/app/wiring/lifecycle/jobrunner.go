// Package app — typed job-runner lifecycle (PR4.8, June 2026).
package lifecycle

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	instaeditcalendar "github.com/Marcuss-ops/PipelineGen/internal/platform/instaeditcalendar"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/platform/jobs/local"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/procmetrics"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// JobRunnerExecutionDeps groups the ports that execute, claim and complete
// work. Keeping these together makes the lifecycle boundary explicit while
// limiting the top-level composition input to named subsystems.
type JobRunnerExecutionDeps struct {
	Repo                     job.Store
	QueueNotifier            job.QueueNotifier
	ClaimSnapshotter         appjobs.ClaimSnapshotter
	Dispatcher               *appjobs.Dispatcher
	ServiceReady             bool
	Broker                   appjobs.CompletionPort
	JobLedger                capjobregistry.Registry
	ParentCompletionNotifier appjobs.ParentCompletionNotifier
}

// JobRunnerTelemetryDeps are optional read-side data sources used for worker
// instrumentation and abandoned-run recovery.
type JobRunnerTelemetryDeps struct {
	ObservabilityDB *sql.DB
	PerformanceDB   *sql.DB
}

type JobRunnerDeps struct {
	Execution JobRunnerExecutionDeps
	Telemetry JobRunnerTelemetryDeps
	Cfg       *config.Config
	Log       *zap.Logger
}

func normalizeJobRunnerDeps(deps JobRunnerDeps) JobRunnerDeps {
	if deps.Cfg == nil {
		deps.Cfg = &config.Config{}
	}
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	return deps
}

const (
	JobRunnerPoolGeneral     = "general"
	JobRunnerPoolSettle      = "clip-render-settle"
	JobRunnerPoolInteractive = "interactive"
	JobRunnerPoolFeeder      = "feeder"
)

func workerDefault(cfg *config.Config) int {
	if cfg == nil || cfg.Jobs.MaxParallelPerProject < 4 {
		return 4
	}
	return cfg.Jobs.MaxParallelPerProject
}

func LeaseTTLDefault(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Jobs.LeaseTTLSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(cfg.Jobs.LeaseTTLSeconds) * time.Second
}

// settleWorkerBudget returns the dedicated clip.render settle budget, or 0 when
// the split is disabled (feature off or budget <= 0 — the pre-guardrail
// behaviour, deliberately reachable so an operator can roll the split back
// without a code change).
func SettleWorkerBudget(cfg *config.Config) int {
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
func jobRunnerBaseConfig(deps JobRunnerDeps) appjobs.RunnerConfig {
	pollMaxBackoff := 60 * time.Second
	if deps.Cfg.Jobs.PollMaxBackoff != "" {
		if parsed, perr := time.ParseDuration(deps.Cfg.Jobs.PollMaxBackoff); perr == nil && parsed > 0 {
			pollMaxBackoff = parsed
		} else if perr != nil {
			deps.Log.Warn("invalid VELOX_POLL_MAX_BACKOFF; using default 60s",
				zap.String("raw", deps.Cfg.Jobs.PollMaxBackoff), zap.Error(perr))
		}
	}
	pollJitter := deps.Cfg.Jobs.PollJitterFraction
	if pollJitter < 0 {
		pollJitter = 0
	} else if pollJitter > 1 {
		pollJitter = 1
	}
	pollConsecutiveEmpty := deps.Cfg.Jobs.PollConsecutiveEmptyBeforeBackoff
	if pollConsecutiveEmpty < 0 {
		pollConsecutiveEmpty = 0
	}

	return appjobs.RunnerConfig{
		Workers:   workerDefault(deps.Cfg),
		PollEvery: 2 * time.Second,
		LeaseTTL:  LeaseTTLDefault(deps.Cfg),
		JobTypes:  nil,
		Backoff: appjobs.BackoffConfig{
			MaxBackoff:                pollMaxBackoff,
			JitterFraction:            pollJitter,
			ConsecutiveEmptyThreshold: pollConsecutiveEmpty,
		},
		Notifier: deps.Execution.QueueNotifier,
	}
}

// buildJobRunnerObserver builds the RunObserver for one pool. It is built per
// pool so each pool owns its recorder; abandoned-run recovery is idempotent, so
// the second pool observing nothing to recover is a no-op.
func buildJobRunnerObserver(deps JobRunnerDeps) *kernobs.RunObserver {
	var recorder kernobs.Recorder
	if deps.Telemetry.ObservabilityDB != nil {
		recorder = obsmetrics.NewSQLiteRecorderWithLogger(deps.Telemetry.ObservabilityDB, deps.Log)
		if reconciler, ok := recorder.(kernobs.AbandonedRunReconciler); ok {
			if _, err := reconciler.RecoverAbandoned(context.Background(), time.Now().UTC()); err != nil {
				deps.Log.Warn("observability abandoned-run recovery failed", zap.Error(err))
			}
		}
	} else {
		deps.Log.Warn("observability recorder unavailable; using metrics-only projection")
	}
	return kernobs.NewRunObserverWithCollector(recorder, obsmetrics.NewRunReportsCollector())
}

// buildJobRunnerResourceSampler builds the run resource sampler for one pool
// (nil when unavailable), plus the host stamped on each observation.
func buildJobRunnerResourceSampler(deps JobRunnerDeps) (kernobs.RunResourceSampler, string) {
	if deps.Telemetry.PerformanceDB == nil {
		return nil, ""
	}
	store, err := perfstore.NewResourceStore(deps.Telemetry.PerformanceDB)
	if err != nil {
		deps.Log.Warn("resource sampler store unavailable; run resource telemetry disabled", zap.Error(err))
		return nil, ""
	}
	sampler, err := perfstore.NewSampler(procmetrics.New(procmetrics.Options{}), store)
	if err != nil {
		deps.Log.Warn("resource sampler unavailable; run resource telemetry disabled", zap.Error(err))
		return nil, ""
	}
	host, _ := os.Hostname()
	return sampler, host
}

// newJobRunnerPool constructs one pool from an explicit config. Registry,
// dispatcher and service orchestration remain root-owned; persistence-specific
// completion classification is injected at the platform boundary.
//
// It returns the pool's InstaEdit calendar Reporter alongside the runner, or a
// nil reporter when the calendar is unconfigured. The reporter is returned
// because attaching it to the workers only SPOOLS reports: somebody has to run
// its drain loop, and that is the job-runner startup step. Dropping the return
// value silently reproduces the defect this signature exists to prevent — a
// spool that fills up forever while the calendar shows nothing.
func newJobRunnerPool(deps JobRunnerDeps, name string, cfg appjobs.RunnerConfig) (*appjobs.Runner, *instaeditcalendar.Reporter) {
	runner := appjobs.NewRunner(deps.Execution.Repo, deps.Execution.Dispatcher, deps.Log, cfg)
	var reporter *instaeditcalendar.Reporter
	calendarURL, calendarKey := instaeditcalendar.LoadConfig()
	if calendarURL != "" && calendarKey != "" {
		client, err := instaeditcalendar.NewClient(calendarURL, calendarKey)
		if err != nil {
			deps.Log.Warn("InstaEdit calendar reporter disabled: invalid configuration", zap.Error(err))
		} else {
			outboxDir := os.Getenv("INSTAEDIT_CALENDAR_OUTBOX")
			if outboxDir == "" {
				outboxDir = filepath.Join(os.Getenv("HOME"), ".local", "state", "pipelinegen", "instaedit-calendar", name)
			}
			reporter, err = instaeditcalendar.NewReporter(client, outboxDir, 2*time.Second)
			if err != nil {
				deps.Log.Warn("InstaEdit calendar reporter disabled: outbox unavailable", zap.Error(err))
				reporter = nil
			} else {
				runner.WithCalendarReporter(reporter)
			}
		}
	} else if calendarURL != "" || calendarKey != "" {
		deps.Log.Warn("InstaEdit calendar reporter disabled: both URL and API key are required")
	}
	runner.WithRegistry(appjobs.Compose())
	runner.WithClaimSnapshotter(deps.Execution.ClaimSnapshotter)
	if deps.Execution.Broker != nil {
		// The raw local broker remains on JobsBundle for server-side APIs that
		// need its full concrete surface. Worker completion receives a narrow
		// classified port so SQLite BUSY/LOCKED never leaks into the jobs
		// capability as a driver-specific error shape.
		runner.WithBroker(localbroker.NewClassifiedCompletionPort(deps.Execution.Broker))
	}
	if deps.Execution.JobLedger != nil {
		runner.WithJobRegistry(deps.Execution.JobLedger)
	}
	runner.WithObserver(buildJobRunnerObserver(deps))
	if sampler, host := buildJobRunnerResourceSampler(deps); sampler != nil {
		runner.WithResourceSampler(sampler, host)
	}
	if deps.Cfg.Features.ClipRenderEnabled {
		if deps.Execution.ParentCompletionNotifier != nil {
			runner.WithParentCompletionNotifier(deps.Execution.ParentCompletionNotifier)
		}
	}
	return runner, reporter
}

// buildJobSchedulerStep constructs the deferred-job scheduler step, or nil
// when scheduling is unavailable (partial deploy, or a broker that does not
// implement job.ScheduleStore).
//
// The scheduler is the ONLY writer of the SCHEDULED → QUEUED promotion, so it
// runs as its own background step rather than inside a worker pool. The
// admission policy comes from config (daily quota + concurrency cap); the
// zero-value policy is unlimited.
func BuildJobSchedulerStep(deps JobRunnerDeps) *StartupStep {
	deps = normalizeJobRunnerDeps(deps)
	if deps.Execution.Repo == nil {
		return nil
	}
	store, ok := any(deps.Execution.Repo).(job.ScheduleStore)
	if !ok {
		deps.Log.Warn("job scheduler disabled: wired broker does not support deferred scheduling")
		return nil
	}
	interval := 30 * time.Second
	if raw := deps.Cfg.Jobs.SchedulerInterval; raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			interval = parsed
		} else if err != nil {
			deps.Log.Warn("invalid VELOX_SCHEDULER_INTERVAL; using default 30s", zap.String("raw", raw), zap.Error(err))
		}
	}
	scheduler, err := jobscheduling.NewScheduler(jobscheduling.SchedulerDeps{
		Store: store,
		Policy: jobscheduling.AdmissionPolicy{
			MaxConcurrent: deps.Cfg.Jobs.SchedulerMaxConcurrent,
			DailyQuota:    deps.Cfg.Jobs.SchedulerDailyQuota,
		},
		Interval: interval,
		Log:      deps.Log,
	})
	if err != nil {
		deps.Log.Warn("job scheduler disabled: invalid policy", zap.Error(err))
		return nil
	}
	return &StartupStep{
		Name: "job-scheduler", Required: false,
		Start: func(startCtx context.Context) error {
			concurrent.SafeGo("job-scheduler", func() { _ = scheduler.Run(startCtx) })
			deps.Log.Info("Job scheduler started",
				zap.Int("daily_quota", deps.Cfg.Jobs.SchedulerDailyQuota),
				zap.Int("max_concurrent", deps.Cfg.Jobs.SchedulerMaxConcurrent),
				zap.Duration("interval", interval))
			return nil
		},
		Stop: func(_ context.Context) error { return nil },
	}
}

func jobRunnerRootReady(deps JobRunnerDeps) bool {
	return deps.Execution.ServiceReady && deps.Execution.Dispatcher != nil && deps.Execution.Repo != nil
}

// buildJobRunner constructs the GENERAL pool: every job type, but — when the
// settle budget is enabled — NOT the clip.render settle phase, and — when the
// interactive lane is enabled — NOT the interactive generation family (which
// gets its own workers so batch work cannot delay it). The exclusions are the
// load-bearing half of the guardrail: without them the general pool would
// still claim the jobs the dedicated pools exist to own and a slow batch
// backlog would keep starving the latency-critical family.
func BuildJobRunner(deps JobRunnerDeps) (*appjobs.Runner, *instaeditcalendar.Reporter) {
	deps = normalizeJobRunnerDeps(deps)
	if !jobRunnerRootReady(deps) {
		return nil, nil
	}
	cfg := jobRunnerBaseConfig(deps)
	if InteractiveWorkerBudget() > 0 || FeederWorkerBudget() > 0 {
		if scoped := GeneralPoolJobTypes(); scoped != nil {
			cfg.JobTypes = scoped
			deps.Log.Info("general pool scoped away from the dedicated lanes",
				zap.String("pool", JobRunnerPoolGeneral),
				zap.Int("claimed_types", len(scoped)))
		}
	}
	deps.Log.Info("Job runner created",
		zap.String("pool", JobRunnerPoolGeneral),
		zap.Int("workers", cfg.Workers),
		zap.Duration("poll_max_backoff", cfg.Backoff.MaxBackoff),
		zap.Float64("poll_jitter_fraction", cfg.Backoff.JitterFraction),
		zap.Int("poll_consecutive_empty_threshold", cfg.Backoff.ConsecutiveEmptyThreshold))
	if SettleWorkerBudget(deps.Cfg) > 0 {
		_, exclude := ClipRenderPhaseScope()
		cfg.PayloadNotMatch = exclude
	}
	return newJobRunnerPool(deps, JobRunnerPoolGeneral, cfg)
}

// buildInteractiveRunner constructs the DEDICATED interactive lane for the
// script generation family (script.generate + script.generate_item), or nil
// when the lane is disabled (budget 0 — the documented rollback switch). It
// claims ONLY its family, so a user-waiting generation job never queues behind
// asset.text.materialize / youtube_clip.extract / cleanup work in the general
// pool — the measured 3.6h head-of-line blocking this lane exists to prevent.
func BuildInteractiveRunner(deps JobRunnerDeps) (*appjobs.Runner, *instaeditcalendar.Reporter) {
	deps = normalizeJobRunnerDeps(deps)
	budget := InteractiveWorkerBudget()
	if budget <= 0 || !jobRunnerRootReady(deps) {
		return nil, nil
	}
	cfg := jobRunnerBaseConfig(deps)
	cfg.Workers = budget
	cfg.JobTypes = InteractiveJobTypes()
	deps.Log.Info("interactive pool created",
		zap.String("pool", JobRunnerPoolInteractive),
		zap.Int("workers", budget),
		zap.Strings("job_types", cfg.JobTypes),
		zap.String("excluded_from", JobRunnerPoolGeneral))
	return newJobRunnerPool(deps, JobRunnerPoolInteractive, cfg)
}

// buildFeederRunner constructs the DEDICATED feeder lane for the family that
// feeds user-visible work (youtube_clip.extract + asset.text.materialize), or
// nil when the lane is disabled (budget 0 — the documented rollback switch).
// It claims ONLY its family, so a general backlog of cleanup/batch work can
// starve neither the feeders nor the generation waiting on their output (the
// measured 13.26x materialize starvation this lane exists to prevent).
func BuildFeederRunner(deps JobRunnerDeps) (*appjobs.Runner, *instaeditcalendar.Reporter) {
	deps = normalizeJobRunnerDeps(deps)
	budget := FeederWorkerBudget()
	if budget <= 0 || !jobRunnerRootReady(deps) {
		return nil, nil
	}
	cfg := jobRunnerBaseConfig(deps)
	cfg.Workers = budget
	cfg.JobTypes = FeederJobTypes()
	deps.Log.Info("feeder pool created",
		zap.String("pool", JobRunnerPoolFeeder),
		zap.Int("workers", budget),
		zap.Strings("job_types", cfg.JobTypes),
		zap.String("excluded_from", JobRunnerPoolGeneral))
	return newJobRunnerPool(deps, JobRunnerPoolFeeder, cfg)
}

// buildClipRenderSettleRunner constructs the DEDICATED clip.render settle pool,
// or nil when the split is disabled. It claims only clip.render jobs whose
// payload carries render_phase=settle, so a remote render waiting on
// RenderingGen can never occupy a general worker slot.
func BuildClipRenderSettleRunner(deps JobRunnerDeps) (*appjobs.Runner, *instaeditcalendar.Reporter) {
	deps = normalizeJobRunnerDeps(deps)
	if !jobRunnerRootReady(deps) {
		return nil, nil
	}
	budget := SettleWorkerBudget(deps.Cfg)
	if budget <= 0 {
		return nil, nil
	}
	cfg := jobRunnerBaseConfig(deps)
	cfg.Workers = budget
	cfg.JobTypes = []string{cliprender.TypeClipRender}
	match, _ := ClipRenderPhaseScope()
	cfg.PayloadMatch = match
	deps.Log.Info("clip.render settle pool created",
		zap.String("pool", JobRunnerPoolSettle),
		zap.Int("workers", budget),
		zap.String("phase", string(cliprender.RenderPhaseSettle)),
		zap.String("excluded_from", JobRunnerPoolGeneral))
	return newJobRunnerPool(deps, JobRunnerPoolSettle, cfg)
}

type JobRunnerPool struct {
	Name    string
	Runner  *appjobs.Runner
	Workers int
	// reporter is the pool's InstaEdit calendar spool, nil when the calendar is
	// unconfigured. The step DRAINS it: attaching it to the workers only spools.
	Reporter *instaeditcalendar.Reporter
}

// startCalendarDrains launches the drain loop of every pool that carries an
// InstaEdit calendar reporter.
//
// The split matters: the workers SPOOL reports durably and this loop is what
// DELIVERS them. A reporter wired to the workers but never drained leaves every
// report in ~/.local/state/pipelinegen/instaedit-calendar/<pool> and the
// calendar dark forever, so the drain is started by the same step that starts
// the runner instead of by a separate opt-in step an operator cannot verify.
func StartCalendarDrains(pools []JobRunnerPool, ctx context.Context, log *zap.Logger) {
	if log == nil {
		log = zap.NewNop()
	}
	for _, pool := range pools {
		if pool.Reporter == nil {
			continue
		}
		pool := pool
		concurrent.SafeGo("calendar-reporter-"+pool.Name, func() { _ = pool.Reporter.Run(ctx) })
		log.Info("InstaEdit calendar reporter draining spooled reports", zap.String("pool", pool.Name))
	}
}

func BuildJobRunnerStep(deps JobRunnerDeps) *StartupStep {
	deps = normalizeJobRunnerDeps(deps)
	general, generalReporter := BuildJobRunner(deps)
	if general == nil {
		return nil
	}
	pools := []JobRunnerPool{{
		Name:     JobRunnerPoolGeneral,
		Runner:   general,
		Workers:  workerDefault(deps.Cfg),
		Reporter: generalReporter,
	}}
	if interactive, interactiveReporter := BuildInteractiveRunner(deps); interactive != nil {
		pools = append(pools, JobRunnerPool{
			Name:     JobRunnerPoolInteractive,
			Runner:   interactive,
			Workers:  InteractiveWorkerBudget(),
			Reporter: interactiveReporter,
		})
	}
	if feeder, feederReporter := BuildFeederRunner(deps); feeder != nil {
		pools = append(pools, JobRunnerPool{
			Name:     JobRunnerPoolFeeder,
			Runner:   feeder,
			Workers:  FeederWorkerBudget(),
			Reporter: feederReporter,
		})
	}
	if settle, settleReporter := BuildClipRenderSettleRunner(deps); settle != nil {
		pools = append(pools, JobRunnerPool{
			Name:     JobRunnerPoolSettle,
			Runner:   settle,
			Workers:  SettleWorkerBudget(deps.Cfg),
			Reporter: settleReporter,
		})
	}
	disp := deps.Execution.Dispatcher
	return &StartupStep{
		Name: "job-runner", Required: true,
		Start: func(startCtx context.Context) error {
			disp.Freeze()
			for _, pool := range pools {
				pool := pool
				concurrent.SafeGo("job-runner-"+pool.Name, func() { pool.Runner.Start(startCtx) })
			}
			StartCalendarDrains(pools, startCtx, deps.Log)
			for _, pool := range pools {
				deps.Log.Info("Job runner pool started after full wiring",
					zap.String("pool", pool.Name), zap.Int("workers", pool.Workers))
			}
			return nil
		},
		Stop: func(_ context.Context) error { return nil },
	}
}
