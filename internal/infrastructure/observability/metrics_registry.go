// Package observability — Prometheus metric definitions.
//
// Every metric is auto-registered via promauto against the default
// Prometheus registry. Split across domain files (metrics_jobs.go,
// metrics_qdrant.go, metrics_scripts.go, metrics_media.go,
// metrics_workers.go) with metrics_registry.go as the canonical
// package-level documentation hub.
//
// Rule (per policy.yaml::prometheus_boundary): NO leaf pkg
// (pkg/<x>/) may import prometheus/client_golang. Metric definitions
// live here; application-layer callers bridge POJO data into metric
// values field-by-field at the call site.
package observability
