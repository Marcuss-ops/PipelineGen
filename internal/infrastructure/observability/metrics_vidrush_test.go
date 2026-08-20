package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestVidRushMetricsAdapterEntityImageCatalogMetrics(t *testing.T) {
	adapter := NewVidRushMetricsAdapter()

	hitsBefore := testutil.ToFloat64(entityImageCatalogHits)
	missesBefore := testutil.ToFloat64(entityImageCatalogMisses)
	refreshBefore := testutil.ToFloat64(entityImageCatalogRefreshes)
	brokenBefore := testutil.ToFloat64(entityImageCatalogBrokenURLs)
	providerBefore := testutil.ToFloat64(entityImageCatalogProviderCalls)
	driveBefore := testutil.ToFloat64(entityImageCatalogDriveReuses)
	downloadBefore := testutil.ToFloat64(entityImageCatalogNewDownloads)

	adapter.IncEntityImageCatalogLookup(true)
	adapter.IncEntityImageCatalogLookup(false)
	adapter.IncEntityImageCatalogRefresh()
	adapter.IncEntityImageCatalogURLBroken()
	adapter.IncEntityImageCatalogProviderCall()
	adapter.IncEntityImageCatalogDriveReuse()
	adapter.IncEntityImageCatalogNewDownload()

	if got := testutil.ToFloat64(entityImageCatalogHits) - hitsBefore; got != 1 {
		t.Fatalf("catalog hit delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(entityImageCatalogMisses) - missesBefore; got != 1 {
		t.Fatalf("catalog miss delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(entityImageCatalogRefreshes) - refreshBefore; got != 1 {
		t.Fatalf("catalog refresh delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(entityImageCatalogBrokenURLs) - brokenBefore; got != 1 {
		t.Fatalf("broken URL delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(entityImageCatalogProviderCalls) - providerBefore; got != 1 {
		t.Fatalf("catalog provider-call delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(entityImageCatalogDriveReuses) - driveBefore; got != 1 {
		t.Fatalf("Drive reuse delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(entityImageCatalogNewDownloads) - downloadBefore; got != 1 {
		t.Fatalf("new download delta = %v, want 1", got)
	}
}

func TestVidRushMetricsAdapterEntityImageCatalogDurations(t *testing.T) {
	adapter := NewVidRushMetricsAdapter()
	lookupBefore := histogramSampleCount(t, entityImageCatalogLookupDuration)
	materializationBefore := histogramSampleCount(t, entityImageCatalogMaterializationDuration)

	adapter.ObserveEntityImageCatalogLookup(0.012)
	adapter.ObserveEntityImageCatalogMaterialization(0.034)

	if got := histogramSampleCount(t, entityImageCatalogLookupDuration) - lookupBefore; got != 1 {
		t.Fatalf("catalog lookup histogram delta = %d, want 1", got)
	}
	if got := histogramSampleCount(t, entityImageCatalogMaterializationDuration) - materializationBefore; got != 1 {
		t.Fatalf("catalog materialization histogram delta = %d, want 1", got)
	}
}

func histogramSampleCount(t *testing.T, collector interface{ Write(*dto.Metric) error }) uint64 {
	t.Helper()
	metric := &dto.Metric{}
	if err := collector.Write(metric); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	if metric.Histogram == nil {
		t.Fatal("expected histogram payload")
	}
	return metric.Histogram.GetSampleCount()
}
