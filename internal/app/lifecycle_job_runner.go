// Package app — typed job-runner lifecycle (PR4.8, June 2026).
//
// W15 pending #2 (architecture/current.yaml): the jobRunner construction
// + inline StartupStep closure that previously lived at the bottom of
// lifecycle.go::startBackgroundJobs is fully extracted here into two
// typed helpers. The pre-PR4.8 surface inlined:
//
//   - configuration defaults (cfg.Jobs.MaxParallelPerProject → workers,
//     with 0 → 1 fallback; cfg.Jobs.LeaseTTLSeconds → leaseTTL with
//     0 → 5 minute fallback);
//   - the canonical appjobs.NewRunner(...) call (returns *appjobs.Runner);
//   - a StartupStep{Name: "job-runner", Required: true} literal that
//     froze the dispatcher synchronously and goroutine-launched the
//     runner via concurrent.SafeGo, appended at the END of the plan.
//
// After PR4.8:
//
//   - buildJobRunner(deps) constructs the canonical *appjobs.Runner and
//     returns nil when the jobs bundle lacks Service / Dispatcher / Repo.
//   - buildJobRunnerStep(deps) returns a *StartupStep (nil when the
//     runner cannot be built). The Start closure freezes the dispatcher
//     and goroutine-launches the runner; the Stop closure is a no-op
//     because the runner exits via context cancellation in
//     serverLifecycle.Stop (the LIFO stop reverses earlier steps and
//     cancels the parent ctx, which the runner poll-loop observes).
//
// The step must be appended LAST in backgroundJobs.startupPlan to satisfy
// the structural invariant asserted by TestLifecycle_JobRunnerLast
// (internal/app/lifecycle_test.go). lifecycle.go::startBackgroundJobs is
// the only caller and appends buildJobRunnerStep(...) at the very end of
// the function body — see the comment in that function.
//
// Why "typed":
//
//   - The "typed" qualifier follows the W15 helper-split convention: a
//     typed deps struct (jobRunnerDeps) feeds typed helpers that produce
//     the canonical concrete types (*appjobs.Runner, StartupStep). No
//     `any` carriers, no anonymous-only construction site.
//   - The runner is the canonical *appjobs.Runner (no struct shadow or
//     adapter), and the step is the canonical StartupStep (no lifecycle
//     abstraction drift).
//   - The deps struct has three typed fields (root *wiring.ComposeRoot,
//     cfg *config.Config, log *zap.Logger); no `any` carrier, no option
//     struct over-engineering.
package app

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// jobRunnerDeps holds the composition-root dependencies required to
// build the job runner and its lifecycle step. Typed, not any:
// every field is a concrete pointer that callers must provide.
type jobRunnerDeps struct {
	root *wiring.ComposeRoot
	cfg  *config.Config
	log  *zap.Logger
}

// workerDefault returns the configured worker count with a 0-safe
// fallback. 0 or negative cfg values map to 1 worker — the pre-PR4.8
// inline behaviour.
func workerDefault(cfg *config.Config) int {
	if cfg == nil || cfg.Jobs.MaxParallelPerProject < 4 {
		// Stock acquisition is I/O- and GPU-bound; a single worker silently
		// serialized every media.stock job despite bounded client parallelism.
		return 4
	}
	return cfg.Jobs.MaxParallelPerProject
}

// leaseTTLDefault returns the configured lease TTL with a 0-safe
// fallback. 0 or negative cfg values map to 5 minutes — the pre-PR4.8
// inline behaviour.
func leaseTTLDefault(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Jobs.LeaseTTLSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(cfg.Jobs.LeaseTTLSeconds) * time.Second
}

// buildJobRunner constructs the canonical *appjobs.Runner from the typed
// deps. PollEvery is fixed at 2 * time.Second (the previous inline
// default). JobTypes is nil so the runner accepts any job type, matching
// the pre-PR4.8 surface.
//
// PR-Polling / ADR-0002 §D6.5 (June 2026): the RunnerConfig now carries
// a Backoff sub-struct (MaxBackoff / JitterFraction /
// ConsecutiveEmptyThreshold) sourced from JobsConfig.PollMaxBackoff /
// PollJitterFraction / PollConsecutiveEmptyBeforeBackoff, plus a
// Notifier pointer (the in-process *SQLiteStore satisfies the
// application-side QueueNotifier port via the compile-time assertion
// at internal/application/jobs/notifier.go).
//
// Returns nil when the jobs bundle's Service / Dispatcher / Repo is
// missing — the caller (lifecycle.go) gates the StartupStep append on
// the returned pointer to preserve the partial-deploy safety net.
func buildJobRunner(deps jobRunnerDeps) *appjobs.Runner {
	if deps.root == nil ||
		deps.root.Jobs.Service == nil ||
		deps.root.Jobs.Dispatcher == nil ||
		deps.root.Jobs.Repo == nil {
		return nil
	}

	// PR-Polling: parse polling knobs (PollMaxBackoff is a duration
	// string per the project's YAML/env convention; falls back to 60s
	// on parse error or empty value). The composition root is the
	// single cfg-parse surface; the Worker receives parsed values.
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

	cfg := appjobs.RunnerConfig{
		Workers:   workerDefault(deps.cfg),
		PollEvery: 2 * time.Second,
		LeaseTTL:  leaseTTLDefault(deps.cfg),
		JobTypes:  nil,
		Backoff: appjobs.BackoffConfig{
			MaxBackoff:                pollMaxBackoff,
			JitterFraction:            pollJitter,
			ConsecutiveEmptyThreshold: pollConsecutiveEmpty,
		},
		// The in-process *SQLiteStore's Subscribe/Broadcast methods
		// satisfy the application-side QueueNotifier port; the
		// compile-time assertion at internal/application/jobs/
		// notifier.go::var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)
		// is the seam marker.
		Notifier: deps.root.Jobs.Repo,
	}
	deps.log.Info("Job runner created",
		zap.Int("workers", cfg.Workers),
		zap.Duration("poll_max_backoff", cfg.Backoff.MaxBackoff),
		zap.Float64("poll_jitter_fraction", cfg.Backoff.JitterFraction),
		zap.Int("poll_consecutive_empty_threshold", cfg.Backoff.ConsecutiveEmptyThreshold))
	// Issue 2 / P0 (June 2026): chain the canonical per-job-type
	// Registry so each Worker created by Runner.Start honors the
	// declared Timeout (e.g. script.generate=60min instead of the
	// literal 10min default) and DefaultMaxRetries (e.g. 2 instead
	// of literal 3). Without this, the typed-port contract is empty
	// and workers silently regress to the HC-0 hardcoded defaults.
	// The earlier PR7 split that introduced RunnerConfig.Notifier
	// preserved the literal-defaults behavior; this is the
	// follow-up that finally wires the Registry.
	runner := appjobs.NewRunner(
		deps.root.Jobs.Repo,
		deps.root.Jobs.Dispatcher,
		deps.log,
		cfg,
	)
	runner.WithRegistry(appjobs.Compose())
	if deps.root.Jobs.Broker != nil {
		runner.WithBroker(deps.root.Jobs.Broker)
	}
	// FASE 2 observability: every claimed job gets a kernel Run
	// (queue_wait_ms, wall_time_ms, status, attempts). The collector
	// exports the finished reports to Prometheus (job_run_duration /
	// queue_wait / retries) so the timings are observable immediately;
	// the durable SQLite recorder sink lands in the persistence phase
	// (FASE 5).
	runner.WithObserver(kernobs.NewRunObserverWithCollector(nil, obsmetrics.NewRunReportsCollector()))
	return runner
}

// buildJobRunnerStep returns the typed StartupStep that launches the
// job runner AFTER every prerequisite service. The dispatcher's Freeze()
// runs synchronously inside Start so no further handlers can register
// once the runner begins claiming jobs. The Stop closure is a no-op
// because the runner exits when serverLifecycle cancels the parent ctx.
//
// Returns nil when the runner cannot be constructed; the caller MUST
// skip the append in that case (preserves the partial-deploy safety
// net: a JobRunner-less mode means no job processing, no startup error).
func buildJobRunnerStep(deps jobRunnerDeps) *StartupStep {
	runner := buildJobRunner(deps)
	if runner == nil {
		return nil
	}
	disp := deps.root.Jobs.Dispatcher
	workers := workerDefault(deps.cfg)
	return &StartupStep{
		Name: "job-runner", Required: true,
		Start: func(startCtx context.Context) error {
			disp.Freeze()
			concurrent.SafeGo("job-runner", func() { runner.Start(startCtx) })
			deps.log.Info("Job runner started after full wiring",
				zap.Int("workers", workers))
			return nil
		},
		Stop: func(_ context.Context) error { return nil },
	}
}
