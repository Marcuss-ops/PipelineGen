package adapters

import (
	"time"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports/metrics"
)

func entityImageCatalogMetricsFor(metrics any) scriptmetrics.EntityImageCatalogMetrics {
	if metrics == nil {
		return nil
	}
	catalogMetrics, _ := metrics.(scriptmetrics.EntityImageCatalogMetrics)
	return catalogMetrics
}

func observeEntityImageCatalogLookup(metrics any, started time.Time) {
	if catalogMetrics := entityImageCatalogMetricsFor(metrics); catalogMetrics != nil {
		catalogMetrics.ObserveEntityImageCatalogLookup(time.Since(started).Seconds())
	}
}

func observeEntityImageCatalogMaterialization(metrics any, started time.Time) {
	if started.IsZero() || metrics == nil {
		return
	}
	if catalogMetrics, ok := metrics.(scriptmetrics.EntityImageCatalogMetrics); ok {
		catalogMetrics.ObserveEntityImageCatalogMaterialization(time.Since(started).Seconds())
	}
}
