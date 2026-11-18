package jobs

import (
	"context"
	"sync/atomic"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	PreparationUnitsTotal          = promauto.NewCounterVec(prometheus.CounterOpts{Name: "preparation_units_total", Help: "Preparation units observed by outcome."}, []string{"outcome", "kind"})
	PreparationCacheHitsTotal      = promauto.NewCounterVec(prometheus.CounterOpts{Name: "preparation_cache_hits_total", Help: "Preparation units adopted from READY cache."}, []string{"kind"})
	PreparationCacheMissesTotal    = promauto.NewCounterVec(prometheus.CounterOpts{Name: "preparation_cache_misses_total", Help: "Preparation units that were not READY at adoption time."}, []string{"kind"})
	PreparationWastedTotal         = promauto.NewCounterVec(prometheus.CounterOpts{Name: "preparation_wasted_total", Help: "Speculative preparation work that was not adopted."}, []string{"kind", "reason"})
	PreparationCriticalPathSavedMS = promauto.NewCounter(prometheus.CounterOpts{Name: "preparation_critical_path_saved_ms_total", Help: "Estimated critical-path milliseconds saved by adopted preparation."})
	PreparationUnitsAtClaim        = promauto.NewHistogram(prometheus.HistogramOpts{Name: "preparation_units_at_claim", Help: "Number of preparation units ready at claim time.", Buckets: []float64{0, 1, 2, 4, 8, 16, 32, 64, 128}})
	PreparationAtClaimRatio        = promauto.NewGauge(prometheus.GaugeOpts{Name: "prepared_at_claim_ratio", Help: "Ratio of required preparation units READY when the job was claimed."})
	PreparationClaimBands          = promauto.NewCounterVec(prometheus.CounterOpts{Name: "preparation_claim_bands_total", Help: "Count of job claims by prepared_at_claim_ratio target band."}, []string{"band"})
	PreparationEstimatedWorkMS     = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "preparation_estimated_work_ms", Help: "Current EMA estimate of preparation work in milliseconds."}, []string{"kind", "source"})
	PreparationWorkloadAmount      = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "preparation_workload_amount", Help: "Workload amount observed for preparation attempts."}, []string{"kind", "dimension"})
	PreparationWorkloadRateMS      = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "preparation_workload_rate_ms", Help: "Observed preparation milliseconds per workload unit."}, []string{"kind", "dimension"})
)

// PreparationAdoptionEvent is the durable observability payload emitted when
// execution adopts or misses a prepared unit.
type PreparationAdoptionEvent struct {
	JobID                 string `json:"job_id"`
	UnitID                string `json:"unit_id"`
	Fingerprint           string `json:"fingerprint"`
	Kind                  string `json:"kind"`
	PreparedBeforeClaim   bool   `json:"prepared_before_claim"`
	Outcome               string `json:"outcome"`
	EstimatedSavedMS      int64  `json:"estimated_saved_ms"`
	ActualWorkMS          int64  `json:"actual_work_ms"`
	SpeculativeWorkWasted bool   `json:"speculative_work_wasted"`
}

// PreparationMetrics tracks process-wide counters and optionally emits the
// canonical job event through the existing job Store event port.
type PreparationMetrics struct {
	store interface {
		AddEvent(context.Context, string, string, string, map[string]any) error
	}

	adoptions atomic.Int64
	hits      atomic.Int64
}

func (m *PreparationMetrics) RecordWorkEstimate(estimate job.WorkEstimate) {
	if estimate.Kind == "" || estimate.ExpectedWorkMS <= 0 {
		return
	}
	PreparationEstimatedWorkMS.WithLabelValues(string(estimate.Kind), string(estimate.Source)).Set(float64(estimate.ExpectedWorkMS))
}

func (m *PreparationMetrics) RecordAdoption(ctx context.Context, event PreparationAdoptionEvent) error {
	kind := event.Kind
	if kind == "" {
		kind = "unknown"
	}
	PreparationUnitsTotal.WithLabelValues(event.Outcome, kind).Inc()
	if event.PreparedBeforeClaim {
		PreparationCacheHitsTotal.WithLabelValues(kind).Inc()
		m.hits.Add(1)
		if event.EstimatedSavedMS > 0 {
			PreparationCriticalPathSavedMS.Add(float64(event.EstimatedSavedMS))
		}
	} else {
		PreparationCacheMissesTotal.WithLabelValues(kind).Inc()
	}
	m.adoptions.Add(1)
	if event.SpeculativeWorkWasted {
		PreparationWastedTotal.WithLabelValues(kind, event.Outcome).Inc()
	}
	if m.store == nil || event.JobID == "" {
		return nil
	}
	data := map[string]any{}
	data["unit_id"] = event.UnitID
	data["fingerprint"] = event.Fingerprint
	data["kind"] = event.Kind
	data["prepared_before_claim"] = event.PreparedBeforeClaim
	data["estimated_saved_ms"] = event.EstimatedSavedMS
	data["actual_work_ms"] = event.ActualWorkMS
	data["speculative_work_wasted"] = event.SpeculativeWorkWasted
	return m.store.AddEvent(ctx, event.JobID, "preparation_adopted", event.Outcome, data)
}
