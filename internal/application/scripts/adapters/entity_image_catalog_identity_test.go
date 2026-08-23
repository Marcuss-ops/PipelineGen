package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type rankedIdentitySearcher struct {
	calls atomic.Int32
}

func (s *rankedIdentitySearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	results := make([]scriptpkg.SegmentAssetCandidate, 0, 10)
	for rank := 1; rank <= 10; rank++ {
		results = append(results, scriptpkg.SegmentAssetCandidate{
			AssetID:   fmt.Sprintf("%s-%02d", strings.ToLower(strings.ReplaceAll(req.Query, " ", "-")), rank),
			Provider:  scriptpkg.VidRushProviderInternetImages,
			Query:     req.Query,
			Entity:    req.Query,
			Score:     1.0 / float64(rank),
			SourceURL: fmt.Sprintf("https://images.example/catalog/%s/%02d.jpg", strings.ToLower(strings.ReplaceAll(req.Query, " ", "-")), rank),
		})
	}
	return results, nil
}

func TestEntityImageCatalogSamplePersonIdentitiesHaveUniquePoolsAndRanks(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &rankedIdentitySearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

	samples := []struct {
		name string
		id   string
	}{
		{name: "Michael Jordan", id: "person:michael-jordan"},
		{name: "Michael B. Jordan", id: "person:michael-b--jordan"},
		{name: "Elon Musk", id: "person:elon-musk"},
		{name: "Cristiano Ronaldo", id: "person:cristiano-ronaldo"},
		{name: "Mike Tyson", id: "person:mike-tyson"},
		{name: "Taylor Swift", id: "person:taylor-swift"},
		{name: "LeBron James", id: "person:lebron-james"},
		{name: "Tom Cruise", id: "person:tom-cruise"},
	}

	seenIDs := make(map[string]string, len(samples))
	for i, sample := range samples {
		identity, err := entitycatalog.CanonicalizePersonName(sample.name)
		if err != nil {
			t.Fatalf("canonicalize %q: %v", sample.name, err)
		}
		if identity.CanonicalEntityID != sample.id {
			t.Fatalf("%q canonical ID = %q, want %q", sample.name, identity.CanonicalEntityID, sample.id)
		}
		if previous, exists := seenIDs[identity.CanonicalEntityID]; exists {
			t.Fatalf("identity collision: %q and %q both map to %q", previous, sample.name, identity.CanonicalEntityID)
		}
		seenIDs[identity.CanonicalEntityID] = sample.name

		result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput(
			fmt.Sprintf("sample-%02d", i), sample.name,
		))
		if err != nil {
			t.Fatalf("process %q: %v", sample.name, err)
		}
		images := result.VidRushSegments[0].Assets.SecondaryImages
		if len(images) != 10 {
			t.Fatalf("%q returned %d images, want 10", sample.name, len(images))
		}

		rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 20)
		if err != nil {
			t.Fatalf("list %q candidates: %v", sample.name, err)
		}
		if len(rows) != 10 {
			t.Fatalf("%q catalog rows = %d, want 10", sample.name, len(rows))
		}
		urls := make(map[string]struct{}, len(rows))
		for rank, row := range rows {
			if row.Rank != rank+1 {
				t.Fatalf("%q row %d has rank %d, want %d", sample.name, rank, row.Rank, rank+1)
			}
			if _, exists := urls[row.SourceURL]; exists {
				t.Fatalf("%q contains duplicate URL %q", sample.name, row.SourceURL)
			}
			urls[row.SourceURL] = struct{}{}
			if !strings.Contains(row.SourceURL, fmt.Sprintf("/%02d.jpg", rank+1)) {
				t.Fatalf("%q rank %d URL = %q, does not match ranked result", sample.name, row.Rank, row.SourceURL)
			}
		}

		for rank := 0; rank < len(images)-1; rank++ {
			if images[rank].Score < images[rank+1].Score {
				t.Fatalf("%q output ranking is not descending at %d/%d: %.3f < %.3f", sample.name, rank+1, rank+2, images[rank].Score, images[rank+1].Score)
			}
		}
	}

	if got := searcher.calls.Load(); got != int32(len(samples)) {
		t.Fatalf("sample provider calls = %d, want one initial search per identity (%d)", got, len(samples))
	}
}

func TestEntityImageCatalogNormalizesPersonVariantsWithoutCreatingAnotherPool(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &rankedIdentitySearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)
	plan := catalogPersonPlan()

	first, err := processor.Process(context.Background(), plan, catalogPersonInput("jordan-original", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := processor.Process(context.Background(), plan, catalogPersonInput("jordan-variant", " Michael   Jordan "))
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("normalized variant provider calls = %d, want 1", got)
	}
	if len(first.VidRushSegments[0].Assets.SecondaryImages) != 10 || len(second.VidRushSegments[0].Assets.SecondaryImages) != 10 {
		t.Fatalf("normalized variant image counts = %d/%d, want 10/10", len(first.VidRushSegments[0].Assets.SecondaryImages), len(second.VidRushSegments[0].Assets.SecondaryImages))
	}
	identity, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Fatalf("normalized variant pool size = %d, want 10", len(rows))
	}
}

func TestEntityImageCatalogMiddleNameCreatesDistinctURLPool(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &rankedIdentitySearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)
	plan := catalogPersonPlan()

	if _, err := processor.Process(context.Background(), plan, catalogPersonInput("jordan", "Michael Jordan")); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), plan, catalogPersonInput("b-jordan", "Michael B. Jordan")); err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 2 {
		t.Fatalf("distinct identity provider calls = %d, want 2", got)
	}
	jordan, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	bJordan, _ := entitycatalog.CanonicalizePersonName("Michael B. Jordan")
	jordanRows, err := repo.ListCandidates(context.Background(), jordan.CanonicalEntityID, 20)
	if err != nil {
		t.Fatal(err)
	}
	bJordanRows, err := repo.ListCandidates(context.Background(), bJordan.CanonicalEntityID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(jordanRows) != 10 || len(bJordanRows) != 10 {
		t.Fatalf("distinct pools = %d/%d, want 10/10", len(jordanRows), len(bJordanRows))
	}
	for _, jordanRow := range jordanRows {
		for _, bJordanRow := range bJordanRows {
			if jordanRow.SourceURL == bJordanRow.SourceURL {
				t.Fatalf("distinct identities share URL %q", jordanRow.SourceURL)
			}
		}
	}
}
