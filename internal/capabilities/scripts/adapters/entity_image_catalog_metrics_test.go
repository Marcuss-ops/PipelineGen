package adapters

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/entitycatalog"
	_ "github.com/mattn/go-sqlite3"
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
	mu                   sync.Mutex
	candidateCounts      map[string]int
	lifecycleCounts      map[string]int
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
func (m *entityImageCatalogMetricsProbe) AddEntityImageCatalogCandidates(stage, outcome string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.candidateCounts == nil {
		m.candidateCounts = make(map[string]int)
	}
	m.candidateCounts[stage+"/"+outcome] += count
}
func (m *entityImageCatalogMetricsProbe) IncEntityImageCatalogLifecycle(stage, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lifecycleCounts == nil {
		m.lifecycleCounts = make(map[string]int)
	}
	m.lifecycleCounts[stage+"/"+outcome]++
}
func (m *entityImageCatalogMetricsProbe) candidateCount(stage, outcome string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.candidateCounts[stage+"/"+outcome]
}
func (m *entityImageCatalogMetricsProbe) lifecycleCount(stage, outcome string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lifecycleCounts[stage+"/"+outcome]
}

func TestEntityImageCatalogOperationalMetricsLookupRefreshAndProvider(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &catalogIntegrationSearcher{}
	metrics := &entityImageCatalogMetricsProbe{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo, metrics)

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

func TestEntityImageCatalogMetricsCoverFullSQLiteDriveLifecycle(t *testing.T) {
	resetEntityImageCatalogCaches()
	db := openPersistentEntityImageCatalog(t, filepath.Join(t.TempDir(), "lifecycle-metrics.db"))
	repo := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	metrics := &entityImageCatalogMetricsProbe{}
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertEntity(context.Background(), entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID,
		EntityType:        entitycatalog.EntityTypePerson,
		CanonicalName:     identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	candidateURL := "https://images.example/michael-jordan/lifecycle.jpg"
	searcherResult := &lifecycleCatalogSearcher{candidate: scriptpkg.SegmentAssetCandidate{
		AssetID: "search-hit", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "Michael Jordan", Query: "Michael Jordan", SourceURL: candidateURL, RightsStatus: "unknown_allowed",
		AcquisitionStatus: scriptpkg.VidRushStatusCandidateFound,
	}}
	stage := NewMediaResolverImageStageWithCatalog(searcherResult, nil, repo, metrics)
	plan := catalogPersonPlan()
	plan.ImagesPerScene = 1
	plan.MediaPlan.Extraction.EntityImages.MaxPerEntity = 1
	result, err := stage.Process(context.Background(), plan, catalogPersonInput("metric-lifecycle", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 1 {
		t.Fatalf("discovery produced %d candidates, want 1: %+v", got, result.VidRushSegments[0])
	}
	if got := searcherResult.calls.Load(); got != 1 {
		t.Fatalf("provider searches = %d, want 1", got)
	}

	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("persisted candidate rows = %d, err=%v", len(rows), err)
	}
	provider := &lifecycleCatalogImageProvider{}
	finalizer := &lifecycleCatalogFinalizer{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	materializer := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo, metrics).WithMediaSampler(&firstCandidateMediaSampler{})
	materializePlan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	materializePlan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	materializePlan.MediaPlan.Extraction.EntityImages = plan.MediaPlan.Extraction.EntityImages
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "metric-lifecycle",
		SceneID:   "metric-lifecycle",
		TextHash:  result.VidRushSegments[0].TextHash,
		Assets:    result.VidRushSegments[0].Assets,
	}}}
	input.VidRushSegments[0].Assets.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), result.VidRushSegments[0].Assets.Candidates...)
	input.VidRushSegments[0].Insights.Entities = []scriptpkg.ExtractedEntity{{Value: "Michael Jordan", Type: "PERSON"}}
	materialized, err := materializer.Process(context.Background(), materializePlan, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(materialized.VidRushSegments) != 1 || len(materialized.VidRushSegments[0].Assets.SecondaryImages) != 1 {
		t.Fatalf("materialized segments/images = %d/%d, want 1/1; acquire=%d verify=%d publish=%d warnings=%v candidates=%+v", len(materialized.VidRushSegments), func() int {
			if len(materialized.VidRushSegments) == 0 {
				return 0
			}
			return len(materialized.VidRushSegments[0].Assets.SecondaryImages)
		}(), provider.acquireCalls.Load(), provider.verifyCalls.Load(), finalizer.calls.Load(), materialized.Warnings, materialized.VidRushSegments[0].Assets.Candidates)
	}
	asset := materialized.VidRushSegments[0].Assets.SecondaryImages[0]
	if asset.PersistenceStatus != scriptpkg.VidRushStatusPersisted || asset.DriveLink == "" || asset.ContentSHA256 == "" {
		t.Fatalf("published asset missing durable metadata: %+v", asset)
	}
	if provider.acquireCalls.Load() != 1 || provider.verifyCalls.Load() != 1 || finalizer.calls.Load() != 1 {
		t.Fatalf("lifecycle calls acquire=%d verify=%d publish=%d; want 1 each", provider.acquireCalls.Load(), provider.verifyCalls.Load(), finalizer.calls.Load())
	}
	stored, err := repo.GetMaterialization(context.Background(), rows[0].ID)
	if err != nil || stored == nil || stored.Status != entitycatalog.MaterializationStatusMaterialized || stored.AssetID != asset.AssetID || stored.DriveLink != asset.DriveLink {
		t.Fatalf("durable SQLite materialization = %+v, err=%v", stored, err)
	}
	for _, step := range []string{"acquire", "verify", "publish"} {
		if got := metrics.lifecycleCount(step, "success"); got != 1 {
			t.Errorf("lifecycle metric %s/success = %d, want 1", step, got)
		}
	}
	if got := metrics.lifecycleCount("catalog_persist", "success"); got != 2 {
		t.Errorf("lifecycle metric catalog_persist/success = %d, want 2 (discovery candidate plus materialization)", got)
	}
	for _, measurement := range []struct{ stage, outcome string }{
		{"discovery", "found"}, {"identity", "accepted"},
		{"acquire", "accepted"}, {"verify", "accepted"},
		{"publish", "accepted"}, {"catalog_persist", "accepted"},
	} {
		if got := metrics.candidateCount(measurement.stage, measurement.outcome); got != 1 {
			t.Errorf("candidate metric %s/%s = %d, want 1", measurement.stage, measurement.outcome, got)
		}
	}

	// A second request rehydrates the Drive row: it must emit reuse, not repeat
	// search/acquire/verify/finalize.
	resetEntityImageCatalogCaches()
	warm, err := NewMediaResolverImageStageWithCatalog(searcherResult, nil, repo, metrics).Process(
		context.Background(), plan, catalogPersonInput("metric-lifecycle-warm", "Michael Jordan"),
	)
	if err != nil {
		t.Fatal(err)
	}
	warmInput := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "metric-lifecycle-warm", SceneID: "metric-lifecycle-warm", TextHash: warm.VidRushSegments[0].TextHash,
		Assets: warm.VidRushSegments[0].Assets,
	}}}
	warmInput.VidRushSegments[0].Assets.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), warm.VidRushSegments[0].Assets.Candidates...)
	if _, err := materializer.Process(context.Background(), materializePlan, warmInput); err != nil {
		t.Fatal(err)
	}
	if got := searcherResult.calls.Load(); got != 1 {
		t.Fatalf("warm lifecycle caused provider searches=%d, want still 1", got)
	}
	if got := provider.acquireCalls.Load(); got != 1 || finalizer.calls.Load() != 1 {
		t.Fatalf("warm lifecycle repeated acquire/publish: acquire=%d publish=%d", got, finalizer.calls.Load())
	}
	if got := metrics.lifecycleCount("reuse", "success"); got < 1 {
		t.Fatalf("reuse lifecycle metric = %d, want at least one catalog materialization reuse", got)
	}

}

type lifecycleCatalogSearcher struct {
	calls     atomic.Int32
	candidate scriptpkg.SegmentAssetCandidate
}

func (s *lifecycleCatalogSearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	return []scriptpkg.SegmentAssetCandidate{s.candidate}, nil
}

type lifecycleCatalogImageProvider struct {
	acquireCalls atomic.Int32
	verifyCalls  atomic.Int32
}

func (*lifecycleCatalogImageProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (*lifecycleCatalogImageProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (p *lifecycleCatalogImageProvider) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.acquireCalls.Add(1)
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/entity-image.jpg", MIMEType: "image/jpeg", SizeBytes: 123, ContentSHA256: "entity-image-hash"}, nil
}
func (p *lifecycleCatalogImageProvider) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	p.verifyCalls.Add(1)
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, ContentSHA256: artifact.ContentSHA256, Width: 1200, Height: 800, RightsStatus: "verified"}, nil
}

type lifecycleCatalogFinalizer struct{ calls atomic.Int32 }

func (f *lifecycleCatalogFinalizer) Finalize(_ context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	f.calls.Add(1)
	candidate := artifact.Candidate
	candidate.AssetID = "published-michael-jordan"
	candidate.ContentSHA256 = artifact.ContentSHA256
	candidate.DriveLink = "https://drive.google.com/file/d/published-michael-jordan/view"
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	candidate.IndexStatus = scriptpkg.VidRushStatusIndexed
	return candidate, nil
}

var _ scriptports.VidRushAssetProvider = (*lifecycleCatalogImageProvider)(nil)
var _ scriptports.VidRushArtifactFinalizer = (*lifecycleCatalogFinalizer)(nil)

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
		ContentSHA256:  "metrics-drive-hash",
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
