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

	// ScriptGenerationBranchTotal (PR-CS-1 FASE 14, July 2026 — CUTOVER default).
	// Single counter, branch + country labels. Branch "a" = ScriptSegment
	// canonical (PR-CS-1 default). Branch "b" = legacy SegmentTopics path
	// targeted by deprecation DL-SCRIPT-BRANCH-B-001 (WAVE-21 + WAVE-22).
	// Increment site: engine_prompt.go tail per-branch. Country label
	// derived from BCP-47 plan.Language via usecase.ExtractCountryForTelemetry.
	ScriptGenerationBranchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "script_generation_branch_total",
		Help: "Total /api/script/generate dispatches by branch (a = ScriptSegment canonical, b = legacy SegmentTopics).",
	}, []string{"branch", "country"})
)
