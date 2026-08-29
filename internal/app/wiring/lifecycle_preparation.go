package wiring

import (
	"context"
	"errors"
	"sync/atomic"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"

	"go.uber.org/zap"
)

// activeWorkGate is the composition-root adapter that gives the preparation
// scheduler a REAL view of active job work: it queries the canonical job
// store stats and reports whether any job is currently claimed/running
// (status RUNNING / LEASED / FINALIZING). Active work has absolute priority
// over speculation — the scheduler consults this gate before every unit and
// stops speculative work the moment a job becomes active.
//
// The atomic bool is a cheap read-side cache: GetStats is only invoked once
// per coordinator inspection cycle, not per candidate.
type activeWorkGate struct {
	store *sqljobs.SQLiteStore

	// cached is the last observed active flag; refreshed by Refresh().
	cached atomic.Bool
}

func newActiveWorkGate(store *sqljobs.SQLiteStore) *activeWorkGate {
	g := &activeWorkGate{store: store}
	g.cached.Store(true) // fail-open before the first refresh; the coordinator refreshes on its first inspection
	return g
}

// Refresh re-reads the store and updates the cached active flag.
func (g *activeWorkGate) Refresh(ctx context.Context) {
	if g == nil || g.store == nil {
		return
	}
	stats, err := g.store.GetStats(ctx)
	if err != nil {
		// Fail-open on stats errors: prefer a brief speculative burst over
		// stalling the coordinator; next refresh will correct.
		g.cached.Store(true)
		return
	}
	g.cached.Store(stats != nil && hasActiveJob(stats))
}

// ActiveWorkAvailable implements appjobs.ActiveWorkGate.
func (g *activeWorkGate) ActiveWorkAvailable() bool {
	return g == nil || g.cached.Load()
}

// hasActiveJob reports whether any job is currently claimed or running.
func hasActiveJob(stats *sqljobs.JobStats) bool {
	if stats == nil || stats.ByStatus == nil {
		return false
	}
	for _, status := range []job.Status{job.StatusRunning, job.StatusLeased, job.StatusFinalizing} {
		if stats.ByStatus[status] > 0 {
			return true
		}
	}
	return false
}

// artifactCacheClaimer adapts the concrete derived-artifact cache's ClaimStore
// to the narrow jobs.ArtifactClaimer port. It maps capcache.ErrLeaseBusy to
// jobs.ErrPreparationLeaseBusy so the preparation lease router treats a
// contended build (another worker owns the BUILDING row past its bounded wait)
// as a benign back-off rather than tearing down the coordinator cycle.
type artifactCacheClaimer struct {
	inner capcache.ClaimStore
}

func (a artifactCacheClaimer) Claim(ctx context.Context, req appjobs.ArtifactClaimRequest) (appjobs.ArtifactClaimResult, error) {
	claim, err := a.inner.Claim(ctx, capcache.Key{
		SourceSHA256:     req.SourceSHA256,
		Operation:        req.Operation,
		ParametersJSON:   req.ParametersJSON,
		ProcessorVersion: req.ProcessorVersion,
	}, req.Lease, req.ExpectedWorkMS)
	if err != nil {
		if errors.Is(err, capcache.ErrLeaseBusy) {
			return appjobs.ArtifactClaimResult{}, appjobs.ErrPreparationLeaseBusy
		}
		return appjobs.ArtifactClaimResult{}, err
	}
	return appjobs.ArtifactClaimResult{Acquired: claim.Acquired, LeaseID: claim.LeaseID}, nil
}

func buildPreparationCoordinatorStep(deps jobRunnerDeps) *StartupStep {
	if deps.root == nil || deps.root.Jobs == nil || deps.root.Jobs.Repo == nil || deps.root.Jobs.Dispatcher == nil {
		return nil
	}
	registry, err := appjobs.ComposeJobPreparationRegistry()
	if err != nil {
		deps.log.Error("preparation registry construction failed", zap.Error(err))
		return nil
	}
	// Real active-work gate backed by the canonical job store stats.
	gate := newActiveWorkGate(deps.root.Jobs.Repo)
	scheduler := appjobs.NewSpeculationScheduler(appjobs.DefaultSpeculationConfig(), gate)
	// Lease router: every planned unit is routed through the CORRECT existing
	// singleflight — artifact-producing units reuse the artifact cache's Claim()
	// (durable BUILDING-lease singleflight); everything else reuses the
	// preparation_units scheduler_owner/lease_until lease (AcquirePreparationUnit).
	// No new singleflight is introduced. The artifact-cache adapter is wired
	// best-effort/fail-open: when the cache is unavailable the router simply
	// routes every unit onto the preparation_units lease.
	var claimer appjobs.ArtifactClaimer
	if deps.root.DB != nil && deps.root.DB.DB != nil {
		if cache, cacheErr := NewArtifactCache(deps.cfg, deps.root.DB.DB, deps.log); cacheErr == nil {
			// *platformcache.Cache implements capcache.ClaimStore; convert to the
			// interface so the adapter can type-assert the singleflight surface.
			claimer = artifactCacheClaimer{inner: capcache.ClaimStore(cache)}
		} else {
			deps.log.Warn("preparation artifact-claim adapter disabled; routing via preparation_units lease", zap.Error(cacheErr))
		}
	}
	router := appjobs.NewPreparationLeaseRouter(deps.root.Jobs.Repo, claimer, "preparation-coordinator", leaseTTLDefault(deps.cfg))
	coordinator, err := appjobs.NewPreparationCoordinator(
		deps.root.Jobs.Repo,
		deps.root.Jobs.Repo,
		registry,
		scheduler,
		3,
		func(ctx context.Context, candidate appjobs.SpeculationCandidate) error {
			// Route the candidate's lease through the correct existing singleflight
			// mechanism — win it (callers run the unit) or learn another worker owns
			// it (back off). Concrete unit execution remains owned by domain adapters.
			return router.Acquire(ctx, candidate)
		},
	)
	// The claim-time KPI snapshot (prepared_at_claim_ratio) is owned by the
	// WORKER claim path, not this coordinator: it fires on the real
	// broker.Claim() instant, when the pristine readiness photograph is still
	// available. buildJobRunner wires runner.WithClaimSnapshotter(Repo) — the
	// coordinator only plans and speculates.

	// Wire the learned per-kind work estimator, replacing static expected_work_ms
	// guesses with a real EMA over completed preparation_attempts. Bootstrap is
	// best-effort (fail-open); an empty history simply means estimates stay static
	// until the first observed runs land.
	estimator := appjobs.NewPreparationWorkEstimator(0)
	if err := estimator.Bootstrap(context.Background(), deps.root.Jobs.Repo, 1000); err != nil {
		deps.log.Warn("preparation work estimator bootstrap failed; using static estimates", zap.Error(err))
	}
	coordinator = coordinator.WithWorkEstimator(estimator)
	if err != nil {
		deps.log.Warn("preparation coordinator disabled", zap.Error(err))
		return nil
	}
	return &StartupStep{
		Name:     "preparation-coordinator",
		Required: false,
		Start: func(ctx context.Context) error {
			// Refresh the gate before the first inspection so the coordinator
			// starts with a truthful active-work snapshot.
			gate.Refresh(ctx)
			go func() {
				if err := coordinator.Start(ctx); err != nil && ctx.Err() == nil {
					deps.log.Warn("preparation coordinator stopped", zap.Error(err))
				}
			}()
			return nil
		},
		Stop: func(context.Context) error { return nil },
	}
}
