package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Qdrant Vector Store Metrics
	QdrantSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_search_duration_seconds",
		Help:    "Duration of Qdrant vector search operations",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"vector_name", "status"})

	QdrantSearchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_search_total",
		Help: "Total number of Qdrant vector search operations",
	}, []string{"vector_name", "status"})

	// QDRANT-006 PR 9 (June 2026): unified qdrant_request_* metrics
	QdrantRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_request_duration_seconds",
		Help:    "Duration of every Qdrant REST request, by canonical operation and HTTP status label.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"operation", "status"})

	QdrantRequestTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_request_total",
		Help: "Total number of Qdrant REST requests, by canonical operation and HTTP status label.",
	}, []string{"operation", "status"})

	QdrantRequestCircuitOpen = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qdrant_request_circuit_open_gauge",
		Help: "Qdrant client circuit-breaker state by breaker scope (0=closed, 1=half-open, 2=open).",
	}, []string{"scope"})

	QdrantUpsertTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_upsert_total",
		Help: "Total number of Qdrant upsert operations",
	}, []string{"status"})

	QdrantReindexDocumentsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_reindex_documents_total",
		Help: "Total number of documents processed by the qdrant reindex pipeline, labelled by outcome.",
	}, []string{"status"})

	QdrantCollectionSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qdrant_collection_size",
		Help: "Number of points in the Qdrant collection",
	}, []string{"collection"})

	QdrantHealthStatus = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_health_status",
		Help: "Qdrant health status: 1 = healthy, 0 = unreachable",
	})

	QdrantErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_errors_total",
		Help: "Total number of Qdrant operation errors",
	}, []string{"operation"})

	// Qdrant Stale Cleaner Metrics
	QdrantStaleTombstoned = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_stale_tombstoned",
		Help: "Number of Qdrant points tombstoned (grace period started) in last cleanup run",
	})

	QdrantStaleDeleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_stale_deleted",
		Help: "Number of Qdrant points hard-deleted (grace period expired) in last cleanup run",
	})

	// SearchNoResultsTotal counts search queries that returned zero hits.
	SearchNoResultsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "search_no_results_total",
		Help: "Total number of vector searches that returned zero results, by vector name",
	}, []string{"vector_name"})

	// QDRANT-005C Reconciler Metrics
	ReconcilerLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_reconciler_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful reconciler run.",
	})

	ReconcilerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_reconciler_duration_seconds",
		Help:    "Duration of reconciler runs by mode (dry_run, apply).",
		Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"mode"})

	ReconcilerFindingsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_reconciler_findings_total",
		Help: "Total number of classification findings emitted by the reconciler, by kind.",
	}, []string{"kind"})

	ReconcilerErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "qdrant_reconciler_errors_total",
		Help: "Total number of non-fatal errors encountered during reconciler runs.",
	})

	ReconcilerVersionMismatchPerChannel = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_reconciler_version_mismatch_per_channel_total",
		Help: "Total number of version-stale classifications, by embedding channel.",
	}, []string{"channel"})

	ReconcilerDispatchesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_reconciler_dispatches_total",
		Help: "Total number of repair actions dispatched by the reconciler, by action.",
	}, []string{"action"})

	PayloadLegacyCleanedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_payload_legacy_cleaned_total",
		Help: "Total number of legacy payload keys stripped from Qdrant points by the reconciler, by key.",
	}, []string{"legacy_key"})

	// QDRANT-005C DR/snapshot alias-switch telemetry
	QdrantAliasSwitchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_alias_switch_total",
		Help: "Total number of alias-switch operations, by action (switch, rollback, rehydrate).",
	}, []string{"action"})

	QdrantAliasSwitchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_alias_switch_duration_seconds",
		Help:    "Duration of alias-switch operations by action.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"action"})

	QdrantAliasCurrentCollection = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qdrant_alias_current_collection",
		Help: "Current physical collection bound to each runtime alias. Set to 1 for the current target, 0 otherwise.",
	}, []string{"alias", "collection"})

	// P6 METRICS-ALIAS (July 2026): alias cache hit/miss counters for
	// monitoring the Searcher.resolveCollection 30s TTL cache effectiveness.
	// Operators compute hit rate as hit/(hit+miss); a low rate signals
	// excessive alias churn or TTL misconfiguration.
	QdrantAliasCacheHitTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "qdrant_alias_cache_hit_total",
		Help: "Total number of alias cache hits from Searcher.resolveCollection.",
	})

	QdrantAliasCacheMissTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "qdrant_alias_cache_miss_total",
		Help: "Total number of alias cache misses from Searcher.resolveCollection.",
	})

	// P6 METRICS-ALIAS (July 2026): end-to-end search latency histogram
	// covering alias resolution + Qdrant query. Measured at the Searcher
	// boundary (Search / HybridSearch) so operators see the wall-clock
	// latency the caller experiences.
	QdrantSearchLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_search_latency_seconds",
		Help:    "End-to-end search latency including alias resolution and Qdrant query, by vector_name.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"vector_name"})
)
