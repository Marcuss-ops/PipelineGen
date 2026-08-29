package jobs

import (
	"context"
	"fmt"
	"sync"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// PreparationWorkEstimator replaces static expected_work_ms guesses with a
// learned per-unit-kind EMA over real preparation_attempts. It answers "how
// long does this unit kind usually take?" (kind EMA) and, when a unit carries a
// workload amount (chars / frames / bytes / tokens), "how long at THIS size?"
// (per-dimension rate EMA: ms per char/frame/byte/token).
//
// Learning model:
//   - per kind  : EMA of wall_ms over completed READY/HIT attempts.
//   - per kind* : EMA of the RATE (wall_ms / amount) for size-scaling kinds,
//     keyed by workload dimension, so scaled estimates track changes without
//     refitting every observation.
//
// The alpha is the standard EMA smoothing factor (higher = more recent weight).
type PreparationWorkEstimator struct {
	mu sync.Mutex

	alpha  float64
	byKind map[job.UnitKind]*kindModel
}

// kindModel holds the per-kind average EMA plus per-dimension RATE EMAs.
type kindModel struct {
	// avgMS is the EMA of observed wall_ms (fallback when no workload amount).
	avgMS    float64
	avgCount int
	// rates[dimension] is the EMA of wall_ms/amount for that size-scaling axis.
	rates map[job.WorkloadDimension]rateModel
}

type rateModel struct {
	rate  float64 // ms per unit of amount
	count int
}

// NewPreparationWorkEstimator returns a ready-to-learn estimator with the given
// smoothing factor. alpha is clamped to (0,1]; 0 selects the default 0.4.
func NewPreparationWorkEstimator(alpha float64) *PreparationWorkEstimator {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.4
	}
	return &PreparationWorkEstimator{alpha: alpha, byKind: make(map[job.UnitKind]*kindModel)}
}

// Observe folds one measured execution into the EMA. wallMS must be > 0 for a
// meaningful estimate; zero/negative observations are ignored (a completed-run
// cost of ~0 would drag every future estimate toward nothing).
func (e *PreparationWorkEstimator) Observe(obs job.WorkObservation) {
	if e == nil || obs.WallMS <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	model := e.byKind[obs.Kind]
	if model == nil {
		model = &kindModel{rates: make(map[job.WorkloadDimension]rateModel)}
		e.byKind[obs.Kind] = model
	}

	// Per-kind average EMA.
	if model.avgCount == 0 {
		model.avgMS = float64(obs.WallMS)
	} else {
		model.avgMS = e.alpha*float64(obs.WallMS) + (1-e.alpha)*model.avgMS
	}
	model.avgCount++

	// Per-dimension rate EMA when the observation carries a workload amount.
	if obs.Dimension != job.WorkloadNone && obs.Amount > 0 {
		rate := float64(obs.WallMS) / obs.Amount
		r := model.rates[obs.Dimension]
		if r.count == 0 {
			r.rate = rate
		} else {
			r.rate = e.alpha*rate + (1-e.alpha)*r.rate
		}
		r.count++
		model.rates[obs.Dimension] = r
	}
}

// Expect returns the learned expected work for the given kind WITHOUT a
// workload amount (the per-kind average EMA). ok=false when there is not yet
// enough signal. Include count > minimalSamples to avoid guessing on a single
// noisy sample? We accept >=1; callers may gate on count.
func (e *PreparationWorkEstimator) Expect(kind job.UnitKind) (job.WorkEstimate, bool) {
	if e == nil {
		return job.WorkEstimate{Kind: kind}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	model, ok := e.byKind[kind]
	if !ok || model.avgCount == 0 {
		return job.WorkEstimate{Kind: kind}, false
	}
	return job.WorkEstimate{
		Kind:           kind,
		ExpectedWorkMS: int64(model.avgMS + 0.5),
		Source:         job.WorkloadNone,
		Observations:   model.avgCount,
	}, true
}

// ExpectUnit estimates expected work for a concrete unit: it prefers a
// size-scaled estimate (unit.Driver() amount × learned rate), else the per-kind
// average. ok=false when the estimator has no signal for the kind at all.
func (e *PreparationWorkEstimator) ExpectUnit(u job.PreparationUnit) (job.WorkEstimate, bool) {
	if e == nil {
		return job.WorkEstimate{Kind: u.Kind}, false
	}
	driver := u.Driver()

	e.mu.Lock()
	model, ok := e.byKind[u.Kind]
	if !ok || model.avgCount == 0 {
		e.mu.Unlock()
		return job.WorkEstimate{Kind: u.Kind}, false
	}
	// Scaled estimate preferred when we have a learned rate for the axis.
	if driver.Dimension != job.WorkloadNone {
		if r, hasRate := model.rates[driver.Dimension]; hasRate && r.count > 0 && r.rate > 0 {
			scaled := int64(0)
			ms := r.rate * driver.Amount
			if ms < float64(int64(^uint64(0)>>1)) {
				scaled = int64(ms + 0.5)
			}
			if scaled > 0 {
				e.mu.Unlock()
				return job.WorkEstimate{
					Kind:           u.Kind,
					ExpectedWorkMS: scaled,
					Source:         driver.Dimension,
					Observations:   r.count,
				}, true
			}
		}
	}
	avg := job.WorkEstimate{
		Kind:           u.Kind,
		ExpectedWorkMS: int64(model.avgMS + 0.5),
		Source:         job.WorkloadNone,
		Observations:   model.avgCount,
	}
	e.mu.Unlock()
	return avg, true
}

// WorkObservationsReader is the narrow read port for bootstrapping the
// estimator from persisted attempt history. Implemented by the preparation
// store.
type WorkObservationsReader interface {
	ListPreparationWorkObservations(context.Context, int) ([]job.WorkObservation, error)
}

// Bootstrap loads recent completed attempts from the reader and folds them into
// the estimator. It is best-effort: a reader error leaves the estimator at its
// current state (fail-open) so speculative planning is never blocked.
func (e *PreparationWorkEstimator) Bootstrap(ctx context.Context, reader WorkObservationsReader, limit int) error {
	if e == nil || reader == nil {
		return nil
	}
	obs, err := reader.ListPreparationWorkObservations(ctx, limit)
	if err != nil {
		return err
	}
	for i := range obs {
		e.Observe(obs[i])
	}
	return nil
}

// String implements fmt.Stringer for diagnostics.
func (e *PreparationWorkEstimator) String() string {
	if e == nil {
		return "PreparationWorkEstimator<nil>"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return fmt.Sprintf("PreparationWorkEstimator{alpha=%.2f, kinds=%d}", e.alpha, len(e.byKind))
}

var _ fmt.Stringer = (*PreparationWorkEstimator)(nil)
