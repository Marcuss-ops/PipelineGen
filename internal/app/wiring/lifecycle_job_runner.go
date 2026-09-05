// Package app — typed job-runner lifecycle (PR4.8, June 2026).
package wiring

import (
	"context"
	"os"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
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

// buildJobRunner constructs the canonical runner. Registry, dispatcher and
// service orchestration remain root-owned; persistence-specific completion
// classification is injected at the platform boundary.
func buildJobRunner(deps jobRunnerDeps) *appjobs.Runner {
	if deps.root == nil ||
		deps.root.Jobs.Service == nil ||
		deps.root.Jobs.Dispatcher == nil ||
		deps.root.Jobs.Repo == nil {
		return nil
	}

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
		Notifier: deps.root.Jobs.Repo,
	}
	deps.log.Info("Job runner created",
		zap.Int("workers", cfg.Workers),
		zap.Duration("poll_max_backoff", cfg.Backoff.MaxBackoff),
		zap.Float64("poll_jitter_fraction", cfg.Backoff.JitterFraction),
		zap.Int("poll_consecutive_empty_threshold", cfg.Backoff.ConsecutiveEmptyThreshold))

	runner := appjobs.NewRunner(
		deps.root.Jobs.Repo,
		deps.root.Jobs.Dispatcher,
		deps.log,
		cfg,
	)
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
	runner.WithObserver(kernobs.NewRunObserverWithCollector(recorder, obsmetrics.NewRunReportsCollector()))

	if deps.root.DB != nil && deps.root.DB.DB != nil {
		store, err := perfstore.NewResourceStore(deps.root.DB.DB)
		if err != nil {
			deps.log.Warn("resource sampler store unavailable; run resource telemetry disabled", zap.Error(err))
		} else {
			sampler, err := perfstore.NewSampler(procmetrics.New(procmetrics.Options{}), store)
			if err != nil {
				deps.log.Warn("resource sampler unavailable; run resource telemetry disabled", zap.Error(err))
			} else {
				host, _ := os.Hostname()
				runner.WithResourceSampler(sampler, host)
			}
		}
	}
	return runner
}

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
			deps.log.Info("Job runner started after full wiring", zap.Int("workers", workers))
			return nil
		},
		Stop: func(_ context.Context) error { return nil },
	}
}
