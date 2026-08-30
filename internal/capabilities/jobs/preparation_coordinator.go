package jobs

import (
	"context"
	"fmt"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ClaimSnapshotter is the narrow port used by the WORKER claim path to capture
// a job's claim-time readiness KPI (required/ready/running/missing units,
// prepared_at_claim_ratio, estimated work saved). It fires on the real
// broker.Claim() — not the coordinator's queue peek — because only the claim
// instant preserves the pristine readiness photograph. Implemented by the
// preparation store; nil disables snapshotting without changing behavior.
type ClaimSnapshotter interface {
	SnapshotPreparationClaim(context.Context, job.PreparationClaimInput) (*job.PreparationClaimSnapshot, error)
}

// PreparedResultAdopter is the execution seam for checkpoint-first adoption.
// It returns true when the prepared result was successfully applied.
type PreparedResultAdopter interface {
	AdoptPreparedResult(context.Context, SpeculationCandidate) (bool, error)
}

// CheckpointFirstExecutor applies the official stage after checkpoint lookup
// and prepared-result adoption have both missed.
type CheckpointFirstExecutor interface {
	ExecuteOfficial(context.Context, SpeculationCandidate) error
}

// QueuedJobReader is the narrow read-only lookahead port used by the
// preparation coordinator. Implementations must not claim or mutate jobs.
type QueuedJobReader interface {
	PeekQueued(context.Context, int) ([]job.Job, error)
}

// PreparationCoordinator wakes on queue notifications, peeks a bounded
// lookahead, and delegates admission/execution. It does not poll on a tight
// loop and never changes job lifecycle state.
type PreparationCoordinator struct {
	reader   QueuedJobReader
	notifier interface {
		Subscribe() <-chan struct{}
	}
	registry  *JobPreparationRegistry
	scheduler *SpeculationScheduler
	lookahead int
	execute   func(context.Context, SpeculationCandidate) error
	metrics   *PreparationMetrics
	estimator *PreparationWorkEstimator
	warmModel func(context.Context, PreparationUnit) error
}

// WithModelWarmer attaches the optional queue-time model residency step. It
// runs only for admitted LLM units, before the candidate lease is acquired,
// so model load is outside the active job's critical path.
func (c *PreparationCoordinator) WithModelWarmer(warm func(context.Context, PreparationUnit) error) *PreparationCoordinator {
	if c != nil {
		c.warmModel = warm
	}
	return c
}

func NewPreparationCoordinator(reader QueuedJobReader, notifier interface{ Subscribe() <-chan struct{} }, registry *JobPreparationRegistry, scheduler *SpeculationScheduler, lookahead int, execute func(context.Context, SpeculationCandidate) error) (*PreparationCoordinator, error) {
	if reader == nil || notifier == nil || registry == nil || scheduler == nil || execute == nil {
		return nil, fmt.Errorf("preparation coordinator requires reader, notifier, registry, scheduler, and executor")
	}
	if lookahead <= 0 {
		lookahead = 3
	}
	return &PreparationCoordinator{reader: reader, notifier: notifier, registry: registry, scheduler: scheduler, lookahead: lookahead, execute: execute}, nil
}

// WithWorkEstimator wires the learned per-kind EMA work estimator. When set,
// candidate expected-work is derived from real attempt history (scaled by the
// unit's workload driver when available) instead of the static priority weight;
// when unset or not yet learned, the static estimate is kept.
func (c *PreparationCoordinator) WithWorkEstimator(estimator *PreparationWorkEstimator) *PreparationCoordinator {
	if c != nil {
		c.estimator = estimator
	}
	return c
}

// Start blocks until ctx cancellation. Initial inspection is performed once;
// subsequent inspections happen only after QueueNotifier broadcasts.
func (c *PreparationCoordinator) WithMetrics(metrics *PreparationMetrics) *PreparationCoordinator {
	if c != nil {
		c.metrics = metrics
	}
	return c
}

func (c *PreparationCoordinator) Start(ctx context.Context) error {
	if err := c.inspect(ctx); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.notifier.Subscribe():
			if err := c.inspect(ctx); err != nil && ctx.Err() == nil {
				return err
			}
		}
	}
}

func (c *PreparationCoordinator) inspect(ctx context.Context) error {
	queued, err := c.reader.PeekQueued(ctx, c.lookahead)
	if err != nil {
		return fmt.Errorf("preparation coordinator peek queued: %w", err)
	}
	candidates := make([]SpeculationCandidate, 0, len(queued))
	for index := range queued {
		j := &queued[index]
		plan, planErr := c.registry.Plan(ctx, j)
		if planErr != nil {
			continue
		}
		// The claim-time KPI (prepared_at_claim_ratio) is NOT captured here:
		// peeking the queue is not claiming the job, and only the real
		// broker.Claim() instant preserves the pristine readiness photograph.
		// The worker claim path owns that snapshot; the coordinator only
		// plans and speculates.
		depth := SpeculationDepth(index + 1)
		for _, unit := range plan.Units {
			estimated := int64(unit.Priority)
			if c.estimator != nil {
				if want, ok := c.estimator.ExpectUnit(job.PreparationUnit{Kind: job.UnitKind(unit.Kind), Inputs: unit.Inputs}); ok && want.ExpectedWorkMS > 0 {
					estimated = want.ExpectedWorkMS
					if c.metrics != nil {
						c.metrics.RecordWorkEstimate(want)
					}
				}
			}
			candidates = append(candidates, SpeculationCandidate{Job: j, Depth: depth, Unit: unit, EstimatedTimeSavedMS: estimated, EstimatedCostMS: costEstimate(unit.CostClass)})
		}
	}
	return c.scheduler.Run(ctx, candidates, func(ctx context.Context, candidate SpeculationCandidate) error {
		if c.warmModel != nil && candidate.Unit.ResourceClass == string(job.ResourceLLM) {
			if err := c.warmModel(ctx, candidate.Unit); err != nil {
				return err
			}
		}
		if c.metrics != nil {
			_ = c.metrics.RecordAdoption(ctx, PreparationAdoptionEvent{JobID: candidate.Job.ID, UnitID: candidate.Unit.ID, Fingerprint: candidate.Unit.Fingerprint, Kind: candidate.Unit.Kind, PreparedBeforeClaim: false, Outcome: "speculative_started", EstimatedSavedMS: candidate.EstimatedTimeSavedMS})
		}
		return c.execute(ctx, candidate)
	})
}

// ExecuteCheckpointFirst implements the canonical order: checkpoint lookup,
// prepared result adoption, then official computation on MISS.
func ExecuteCheckpointFirst(ctx context.Context, checkpoints capcheckpoint.Store, adopter PreparedResultAdopter, official CheckpointFirstExecutor, candidate SpeculationCandidate) (bool, error) {
	if candidate.Job == nil || checkpoints == nil || official == nil {
		return false, fmt.Errorf("checkpoint-first execution requires job, checkpoint store, and official executor")
	}
	stage := candidate.Unit.Kind
	checkpoint, err := checkpoints.Get(ctx, candidate.Job.ID, stage, candidate.Unit.ID)
	if err != nil {
		return false, err
	}
	if checkpoint != nil && checkpoint.Status == capcheckpoint.StatusCompleted && checkpoint.InputFingerprint == candidate.Unit.Fingerprint {
		return true, nil
	}
	if adopter != nil {
		adopted, err := adopter.AdoptPreparedResult(ctx, candidate)
		if err != nil {
			return false, err
		}
		if adopted {
			return true, nil
		}
	}
	if err := official.ExecuteOfficial(ctx, candidate); err != nil {
		return false, err
	}
	return true, nil
}

func costEstimate(cost string) int64 {
	switch cost {
	case string(CostWarm):
		return 1
	case string(CostSpeculate):
		return 10
	default:
		return 3
	}
}
