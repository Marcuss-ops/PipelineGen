package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ScriptGenerationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_generation_total",
		Help: "Total number of script generation attempts",
	}, []string{"model", "language", "outcome"})

	ScriptCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_cache_hits_total",
		Help: "Memory gate cache hits, partitioned by level.",
	}, []string{"level", "channel_id"})

	ScriptMemoryEntries = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "script_memory_entries",
		Help: "Current row count of gemmamemory tables, by table",
	}, []string{"table"})

	ScriptNearDuplicates = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_near_duplicates_total",
		Help: "Generations flagged as near-duplicate of a prior run.",
	}, []string{"channel_id"})

	ScriptPhaseTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_phase_total",
		Help: "Total number of script phase executions",
	}, []string{"phase", "topic"})

	// ScriptFallbackUsedTotal counts every deterministic fallback applied to
	// the generated narration body. A non-zero rate signals a translation or
	// segment-validation regression that the plain word-count gate cannot see.
	ScriptFallbackUsedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_fallback_used_total",
		Help: "Total number of generated-body fallbacks applied, by bounded reason.",
	}, []string{"reason"})

	// ── Checkpoint write-amplification instrumentation ──────────────────
	//
	// The script voiceover phase checkpoints the WHOLE GenerateResult once per
	// completed (scene, language) unit — 5 scenes × 9 languages is 45
	// serializations of the same growing document. Before that granularity can
	// be reduced (scene-complete / language-complete / phase boundary), the
	// amplification has to be MEASURABLE: attempts vs. writes, the bytes each
	// write ships to SQLite, the wall time it costs, and the hidden barrier of
	// waiting for the per-unit apply lock that no stage timer covered.
	//
	// script_checkpoint_attempt_total counts every checkpoint() call, including
	// the ones whose save fails — attempt/write divergence is the signal that
	// checkpoints are being dropped rather than deduplicated.
	ScriptCheckpointAttemptTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "script_checkpoint_attempt_total",
		Help: "Total number of script checkpoint attempts (one per per-unit SavePartialResult call).",
	})

	// script_checkpoint_write_total counts the attempts whose partial result
	// was persisted successfully.
	ScriptCheckpointWriteTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "script_checkpoint_write_total",
		Help: "Total number of script checkpoints persisted successfully.",
	})

	// script_checkpoint_bytes_total counts the serialized checkpoint payload
	// bytes written, so write amplification is visible as bytes-per-run and not
	// only as a call count.
	ScriptCheckpointBytesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "script_checkpoint_bytes_total",
		Help: "Total serialized bytes written by script checkpoints.",
	})

	// ScriptCheckpointSeconds observes the duration of ONE checkpoint save
	// (repository write + semantic-bundle sidecar), excluding the apply lock.
	// Buckets are sub-second because the measured range on production runs was
	// tens to low hundreds of milliseconds.
	ScriptCheckpointSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "script_checkpoint_seconds",
		Help:    "Wall time of one script checkpoint save (repository write + sidecar).",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})

	// ScriptCheckpointWaitSeconds observes how long a worker waited for the
	// per-unit apply lock before it could checkpoint. The stage timer never saw
	// this wait, so contention between the scene×language workers was invisible;
	// it is the barrier this metric exists to expose.
	ScriptCheckpointWaitSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "script_checkpoint_wait_seconds",
		Help:    "Wall time a voiceover worker waited for the per-unit apply lock before checkpointing.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
	})
)
