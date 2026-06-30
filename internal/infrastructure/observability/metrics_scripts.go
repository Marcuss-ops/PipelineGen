package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Script Generation Metrics
	ScriptGenerationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "script_generation_duration_seconds",
		Help:    "Duration of script generation calls (Ollama round-trip)",
		Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"model", "language", "outcome"})

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

	// Phase-level Timing Metrics
	ScriptPhaseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "script_phase_duration_seconds",
		Help:    "Duration of each phase in the script generation pipeline",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"phase", "topic"})

	ScriptPhaseTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_phase_total",
		Help: "Total number of script phase executions",
	}, []string{"phase", "topic"})
)
