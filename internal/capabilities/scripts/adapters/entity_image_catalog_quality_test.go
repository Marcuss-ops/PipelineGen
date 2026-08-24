package adapters

import (
	"context"
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
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

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
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

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
}

func TestEntityImageCatalogAcceptsExactPersonAndPersistsQualityMetadata(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &qualityCatalogSearcher{result: []scriptpkg.SegmentAssetCandidate{{
		AssetID: "exact-person", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "MICHAEL   JORDAN", Query: "Michael Jordan",
		SourceURL: "https://images.example/michael-jordan.jpg", Width: 1920, Height: 1080,
	}}}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

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
	if len(rows) != 1 || rows[0].SemanticStatus != "accepted" || rows[0].SemanticScore < 0.99 || rows[0].TechnicalScore < 0.99 {
		t.Fatalf("persisted quality metadata = %+v, want accepted semantic/technical quality", rows)
	}
}

func entitycatalogIdentityForTest(name string) (string, error) {
	identity, err := entitycatalog.CanonicalizePersonName(name)
	if err != nil {
		return "", err
	}
	return identity.CanonicalEntityID, nil
}
