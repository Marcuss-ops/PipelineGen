package adapters

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type entityImageCatalogMetricsProbe struct {
	catalogHits          atomic.Int32
	catalogMisses        atomic.Int32
	refreshes            atomic.Int32
	brokenURLs           atomic.Int32
	providerCalls        atomic.Int32
	lookupObservations   atomic.Int32
	materializationTimes atomic.Int32
	driveReuses          atomic.Int32
	newDownloads         atomic.Int32
}

func (*entityImageCatalogMetricsProbe) IncSegments()                             {}
func (*entityImageCatalogMetricsProbe) IncExtractionCache(bool)                  {}
func (*entityImageCatalogMetricsProbe) IncAssetCache(string, bool)               {}
func (*entityImageCatalogMetricsProbe) IncProviderRequest(string)                {}
func (*entityImageCatalogMetricsProbe) IncProviderFailure(string)                {}
func (*entityImageCatalogMetricsProbe) IncBinding()                              {}
func (*entityImageCatalogMetricsProbe) IncUnresolvedSegment()                    {}
func (*entityImageCatalogMetricsProbe) ObserveProcessorDuration(string, float64) {}
func (*entityImageCatalogMetricsProbe) ObserveProviderDuration(string, float64)  {}

func (m *entityImageCatalogMetricsProbe) IncEntityImageCatalogLookup(hit bool) {
	if hit {
		m.catalogHits.Add(1)
		return
	}
	m.catalogMisses.Add(1)
}
func (m *entityImageCatalogMetricsProbe) IncEntityImageCatalogRefresh() {
	m.refreshes.Add(1)
}
func (m *entityImageCatalogMetricsProbe) IncEntityImageCatalogURLBroken() {
	m.brokenURLs.Add(1)
}
func (m *entityImageCatalogMetricsProbe) IncEntityImageCatalogProviderCall() {
	m.providerCalls.Add(1)
}
func (m *entityImageCatalogMetricsProbe) ObserveEntityImageCatalogLookup(float64) {
	m.lookupObservations.Add(1)
}
func (m *entityImageCatalogMetricsProbe) ObserveEntityImageCatalogMaterialization(float64) {
	m.materializationTimes.Add(1)
}
func (m *entityImageCatalogMetricsProbe) IncEntityImageCatalogDriveReuse() {
	m.driveReuses.Add(1)
}
func (m *entityImageCatalogMetricsProbe) IncEntityImageCatalogNewDownload() {
	m.newDownloads.Add(1)
}

func TestEntityImageCatalogOperationalMetricsLookupRefreshAndProvider(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &catalogIntegrationSearcher{}
	metrics := &entityImageCatalogMetricsProbe{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo, metrics)

	if _, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("metrics-cold", "Michael Jordan")); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("metrics-warm", "Michael Jordan")); err != nil {
		t.Fatal(err)
	}

	if got := metrics.catalogMisses.Load(); got != 1 {
		t.Fatalf("catalog misses = %d, want 1", got)
	}
	if got := metrics.catalogHits.Load(); got != 1 {
		t.Fatalf("catalog hits = %d, want 1", got)
	}
	if got := metrics.refreshes.Load(); got != 1 {
		t.Fatalf("catalog refreshes = %d, want 1", got)
	}
	if got := metrics.providerCalls.Load(); got != 1 {
		t.Fatalf("catalog provider calls = %d, want 1", got)
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("searcher calls = %d, want 1", got)
	}
	if got := metrics.lookupObservations.Load(); got != 2 {
		t.Fatalf("catalog lookup observations = %d, want 2", got)
	}
}

func TestEntityImageCatalogOperationalMetricsDriveReuseAndBrokenDownload(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/michael-jordan-metrics.jpg")
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("catalog rows = %d, err=%v", len(rows), err)
	}
	if err := repo.UpsertMaterialization(context.Background(), entitycatalog.Materialization{
		CandidateID:    rows[0].ID,
		AssetID:        "metrics-drive-asset",
		LegacyFileMD5:  "metrics-drive-hash",
		DriveLink:      "https://drive.google.com/file/d/metrics-drive-asset/view",
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: time.Now().UTC(),
		LastVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	reuseMetrics := &entityImageCatalogMetricsProbe{}
	reuseProvider := &catalogReuseImageProvider{}
	reuseFinalizer := &catalogReuseFinalizer{}
	reuseRegistry := NewVidRushAssetProviderRegistry()
	if err := reuseRegistry.Register(reuseProvider); err != nil {
		t.Fatal(err)
	}
	reuseRegistry.Freeze()
	reuseProcessor := NewVidRushMaterializationProcessorWithCatalog(reuseRegistry, reuseFinalizer, nil, repo, reuseMetrics)
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	_, err = reuseProcessor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "metrics-drive-reuse",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "discovery-id", Provider: scriptpkg.VidRushProviderInternetImages,
			Entity: "Michael Jordan", SourceURL: "https://images.example/michael-jordan-metrics.jpg",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := reuseMetrics.driveReuses.Load(); got != 1 {
		t.Fatalf("Drive reuses = %d, want 1", got)
	}
	if got := reuseMetrics.newDownloads.Load(); got != 0 {
		t.Fatalf("new downloads during Drive reuse = %d, want 0", got)
	}
	if got := reuseProvider.acquireCalls.Load(); got != 0 {
		t.Fatalf("Drive reuse acquire calls = %d, want 0", got)
	}

	brokenRepo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, brokenRepo, "Michael Jordan", "https://images.example/michael-jordan-broken-metrics.jpg")
	brokenMetrics := &entityImageCatalogMetricsProbe{}
	brokenProvider := &catalogReuseImageProvider{}
	brokenRegistry := NewVidRushAssetProviderRegistry()
	if err := brokenRegistry.Register(brokenProvider); err != nil {
		t.Fatal(err)
	}
	brokenRegistry.Freeze()
	brokenProcessor := NewVidRushMaterializationProcessorWithCatalog(brokenRegistry, &catalogReuseFinalizer{}, nil, brokenRepo, brokenMetrics)
	_, err = brokenProcessor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "metrics-broken-url",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "discovery-id", Provider: scriptpkg.VidRushProviderInternetImages,
			Entity: "Michael Jordan", SourceURL: "https://images.example/michael-jordan-broken-metrics.jpg",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := brokenMetrics.newDownloads.Load(); got != 1 {
		t.Fatalf("new downloads = %d, want 1", got)
	}
	if got := brokenMetrics.brokenURLs.Load(); got != 1 {
		t.Fatalf("broken URLs = %d, want 1", got)
	}
	if got := brokenMetrics.materializationTimes.Load(); got != 1 {
		t.Fatalf("materialization observations = %d, want 1", got)
	}
}
