package vidrush

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type qualityCatalogSearcher struct {
	calls  atomic.Int32
	result []scriptpkg.SegmentAssetCandidate
}

func (s *qualityCatalogSearcher) SearchImages(_ context.Context, _ InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	return append([]scriptpkg.SegmentAssetCandidate(nil), s.result...), nil
}

func TestEntityImageCatalogRejectsWrongPersonWithGoodTechnicalQuality(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &qualityCatalogSearcher{result: []scriptpkg.SegmentAssetCandidate{{
		AssetID: "wrong-person", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "Michael B. Jordan", Query: "Michael Jordan",
		SourceURL: "https://images.example/michael-b-jordan.jpg", Width: 1920, Height: 1080,
	}}}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("semantic-wrong", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 0 {
		t.Fatalf("wrong-person candidates returned = %d, want 0", got)
	}
	identity, _ := entitycatalogIdentityForTest("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("wrong-person candidates persisted = %d, want 0", len(rows))
	}
}

func TestEntityImageCatalogRejectsTechnicallyInsufficientCandidate(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &qualityCatalogSearcher{result: []scriptpkg.SegmentAssetCandidate{{
		AssetID: "tiny-person", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "Michael Jordan", Query: "Michael Jordan",
		SourceURL: "https://images.example/michael-jordan-small.jpg", Width: 320, Height: 240,
	}}}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("technical-small", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 0 {
		t.Fatalf("low-quality candidates returned = %d, want 0", got)
	}
	identity, _ := entitycatalogIdentityForTest("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("low-quality candidates persisted = %d, want 0", len(rows))
	}
	entity, err := repo.GetEntity(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if entity.RefreshStatus != entitycatalog.RefreshStatusFailed {
		t.Fatalf("refresh status = %q, want failed", entity.RefreshStatus)
	}
	if !strings.Contains(entity.LastError, "dimensions below PERSON catalog minimum") {
		t.Fatalf("refresh error = %q, want the dominant rejection reason", entity.LastError)
	}
	if !strings.Contains(entity.LastError, "provider returned 1 candidate(s)") {
		t.Fatalf("refresh error = %q, want the provider candidate count", entity.LastError)
	}
}

func TestEntityImageCatalogKeepsExactPersonPendingUntilVisualVerdict(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &qualityCatalogSearcher{result: []scriptpkg.SegmentAssetCandidate{{
		AssetID: "exact-person", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "MICHAEL   JORDAN", Query: "Michael Jordan",
		SourceURL: "https://images.example/michael-jordan.jpg", Width: 1920, Height: 1080,
	}}}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("semantic-exact", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 1 {
		t.Fatalf("exact-person candidates returned = %d, want 1", got)
	}
	identity, _ := entitycatalogIdentityForTest("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	// A provider entity/query echo is necessary to keep another person out,
	// but it is not proof that the pixels depict this person. Without a
	// visual scorer the row must stay pending and non-reusable.
	if len(rows) != 1 || rows[0].SemanticStatus != entitycatalog.CandidateSemanticUnknown || rows[0].SemanticScore != 0 || rows[0].TechnicalScore < 0.99 {
		t.Fatalf("persisted quality metadata = %+v, want unknown semantic identity with technical quality", rows)
	}
	if entitycatalog.IsUsableCandidateQuality(rows[0]) {
		t.Fatalf("pending row %+v is catalog-reusable without a visual verdict", rows[0])
	}
}

func TestEntityImageCatalogRejectsNonRasterImageSource(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &qualityCatalogSearcher{result: []scriptpkg.SegmentAssetCandidate{{
		AssetID: "document-source", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "Michael Jordan", Query: "Michael Jordan",
		SourceURL: "https://images.example/michael-jordan-profile.pdf", Width: 1920, Height: 1080,
	}}}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("semantic-document", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 0 {
		t.Fatalf("non-raster candidates returned = %d, want 0", got)
	}
	identity, _ := entitycatalogIdentityForTest("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("non-raster candidates persisted = %d, want 0", len(rows))
	}
	entity, err := repo.GetEntity(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if entity.RefreshStatus != entitycatalog.RefreshStatusFailed || !strings.Contains(entity.LastError, "not a raster image") {
		t.Fatalf("refresh state = %q / %q, want failed with the non-raster rejection reason", entity.RefreshStatus, entity.LastError)
	}
}

func entitycatalogIdentityForTest(name string) (string, error) {
	identity, err := entitycatalog.CanonicalizePersonName(name)
	if err != nil {
		return "", err
	}
	return identity.CanonicalEntityID, nil
}

func TestFormatPersonImageRejectionSummaryIsDeterministic(t *testing.T) {
	if got := formatPersonImageRejectionSummary(0, nil); got != "provider returned no candidates" {
		t.Fatalf("empty summary = %q", got)
	}
	got := formatPersonImageRejectionSummary(4, map[string]int{
		"technical quality score below minimum":                      1,
		"image dimensions below PERSON catalog minimum":              2,
		"candidate query does not contain requested PERSON identity": 1,
	})
	want := "provider returned 4 candidate(s), rejected 4: image dimensions below PERSON catalog minimum (2); candidate query does not contain requested PERSON identity (1); technical quality score below minimum (1)"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

// TestEntityImageCatalogPoolDoesNotCountRecertifyingUnknownAsUsable pins the
// silent-empty mechanism: a layout-recertification row is returned to the
// caller as unknown (so it can be retried), but it is not supply. Counting it
// as usable made the pool look sufficient, skipped the provider refresh, and
// then the selector dropped it because PERSON selection requires accepted.
func TestEntityImageCatalogPoolDoesNotCountRecertifyingUnknownAsUsable(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
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
	if _, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID,
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/michael-jordan/recheck.jpg",
		Width:             1920,
		Height:            1080,
		Status:            entitycatalog.CandidateStatusFresh,
		SemanticStatus:    entitycatalog.CandidateSemanticUnknown,
		TechnicalScore:    1,
		QualityReason:     "Ollama visual verification did not find one clear person",
	}); err != nil {
		t.Fatal(err)
	}

	pool, err := entityImageCatalogCandidates(context.Background(), repo, identity, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pool.UsableCount != 0 {
		t.Fatalf("usable count = %d, want 0 for an unknown recertification row", pool.UsableCount)
	}
	if pool.Sufficient {
		t.Fatal("an unknown-only pool must not satisfy demand; the caller must refresh")
	}
	if len(pool.Candidates) != 1 {
		t.Fatalf("candidates = %d, want the unknown row kept for retry", len(pool.Candidates))
	}
}
