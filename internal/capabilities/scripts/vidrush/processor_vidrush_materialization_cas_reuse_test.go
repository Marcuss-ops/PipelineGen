package vidrush

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

// TestWarmCatalogReuseBindsCASBytesWithoutAcquisition is the hot-run proof for
// the catalog/CAS link: on a warm run the durable row is enough to satisfy the
// scene, the bytes come from the local content-addressed store, and the
// acquisition provider is never called — no Drive download, no provider I/O.
func TestWarmCatalogReuseBindsCASBytesWithoutAcquisition(t *testing.T) {
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Corrado Augias", "https://images.example/corrado-augias/portrait.jpg")
	identity, err := entitycatalog.CanonicalizePersonName("Corrado Augias")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("catalog rows = %d, err=%v", len(rows), err)
	}
	casPath, sha := writeCASFixture(t)
	if err := repo.UpsertMaterialization(context.Background(), entitycatalog.Materialization{
		CandidateID:    rows[0].ID,
		AssetID:        "asset-corrado-augias",
		ContentSHA256:  sha,
		DriveFileID:    "drive-corrado-augias",
		DriveLink:      "https://drive.google.com/file/d/drive-corrado-augias/view",
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: time.Now().UTC(), LastVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	provider := &refreshFailingAcquireProvider{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, nil, nil, repo).
		WithCASStore(fakeContentStore{sha: casPath})
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	run := &vidRushMaterializationRun{
		processor: processor,
		plan:      plan,
		segment:   scriptpkg.VidRushSegmentResult{SegmentID: "warm-cas"},
	}

	pool, err := entityImageCatalogCandidates(context.Background(), repo, identity, 5, processor.contentStore)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Candidates) != 1 {
		t.Fatalf("catalog pool = %d, want 1", len(pool.Candidates))
	}
	if got := pool.Candidates[0].LocalPath; got != casPath {
		t.Fatalf("catalog candidate LocalPath = %q, want %q", got, casPath)
	}

	materialized, err := run.materialize(context.Background(), pool.Candidates, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.acquireCalls.Load(); got != 0 {
		t.Fatalf("acquire calls = %d, want 0: a warm catalog hit with local bytes must not be downloaded", got)
	}
	if len(materialized) != 1 {
		t.Fatalf("materialized = %d, want 1", len(materialized))
	}
	got := materialized[0]
	if got.LocalPath != casPath {
		t.Fatalf("bound LocalPath = %q, want the CAS path %q", got.LocalPath, casPath)
	}
	if strings.TrimSpace(got.DriveLink) == "" || got.ContentSHA256 != sha {
		t.Fatalf("bound candidate lost its durable locator: %+v", got)
	}
	if !readyVidRushCandidate(got) {
		t.Fatalf("bound candidate is not ready: %+v", got)
	}
}
