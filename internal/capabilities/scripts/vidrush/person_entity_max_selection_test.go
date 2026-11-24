package vidrush

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The Rust `imagecatalog` crate was retired by TICKET-28 (2026-11-18), so the
// person/entity "max" suite described for it cannot run anywhere. The surviving
// owner is split: catalog policy lives in
// internal/capabilities/images/entitycatalog, while selection and binding live
// here. These are the cases from that suite with no reachable Go equivalent:
//
//	two_people_two_queries      -> TestPersonEntityMaxTwoPeopleOneSceneKeepBothPools
//	reuse_prefers_unused        -> TestPersonEntityMaxReusePrefersUnusedVariant
//	replay_identical            -> TestPersonEntityMaxReplayIsIdentical
//	mismatch rejects accepted   -> TestPersonEntityMaxAcceptedRowWithConflictingSourceNameIsNotSelected
//
// Already covered: canonical parity (plain/possessive/accented/CJK) and the
// seven-day boundary in entitycatalog/person_entity_max_test.go; single-person
// selection in TestInternetImagesProcessorCatalogHitSkipsProvider; catalog miss
// in TestInternetImagesProcessorCatalogMissPopulatesAndReusesCanonicalEntity;
// PERSON-only eligibility in
// TestPersonCatalogIdentityExplicitlyExcludesNonPersonEntities. The two
// live-seed cases (Chiara Poggi 49960, triptych 34357) are not portable: the
// seed fixture was deleted with the crate and no media ids remain.

// personEntityMaxInput builds one scene that names every PERSON in the phrase.
// It is the multi-entity counterpart of catalogPersonInput, whose single
// annotated entity is what hid the "two people collapse into one pool" bug.
func personEntityMaxInput(segmentID string, names ...string) ProcessInput {
	entities := make([]scriptpkg.ExtractedEntity, 0, len(names))
	annotated := make([]scriptpkg.AnnotatedEntity, 0, len(names))
	canonicalIDs := make(map[string]string, len(names))
	for _, name := range names {
		identity, _ := entitycatalog.CanonicalizePersonName(name)
		entities = append(entities, scriptpkg.ExtractedEntity{
			Value: name, Type: "PERSON", CanonicalID: identity.CanonicalEntityID,
			SearchIntents: []string{name},
		})
		annotated = append(annotated, scriptpkg.AnnotatedEntity{
			CanonicalName: name, Text: name, Type: "PERSON",
			CanonicalEntityID: identity.CanonicalEntityID,
		})
		canonicalIDs[name] = identity.CanonicalEntityID
	}
	return ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: segmentID, SegmentID: segmentID,
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: annotated},
		}}},
		VidRushSegments: []scriptpkg.VidRushSegmentResult{{
			SegmentID: segmentID, SceneID: segmentID, TextHash: segmentID,
			Insights: scriptpkg.SegmentInsights{
				Entities:                entities,
				ImageEntityCanonicalIDs: canonicalIDs,
				ImageQueries:            append([]string(nil), names...),
			},
		}},
	}
}

// personEntityMaxReadyCandidate is a candidate the binding gate accepts without
// a Drive copy: acquired + verified bytes, a content address and the original
// locator. Rights stay unknown_allowed, the only value the URL fallback admits.
func personEntityMaxReadyCandidate(assetID, name, sourceURL string) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		AssetID: assetID, Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: name, Query: name, SourceURL: sourceURL, Score: 1,
		SemanticStatus: entitycatalog.CandidateSemanticAccepted, SemanticScore: 0.9,
		VerifierVersion:    entitycatalog.CandidateVerifierVersion,
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		ContentSHA256:      strings.Repeat("a", 64),
		RightsStatus:       "unknown_allowed",
	}
}

func personEntityMaxScene(id, name string) scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: id, SegmentID: id,
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			CanonicalName: name, Text: name, Type: "PERSON",
		}}},
	}
}

func personEntityMaxSegment(id, name string, candidates ...scriptpkg.SegmentAssetCandidate) scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{
		SegmentID: id, SceneID: id,
		Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: name, Type: "PERSON"}}},
		Assets:   scriptpkg.SegmentAssetSelection{SecondaryImages: candidates},
	}
}

func seedPersonEntityMaxRow(t *testing.T, repo *integrationEntityImageCatalog, name, sourceURL, qualityReason string) {
	t.Helper()
	identity, err := entitycatalog.CanonicalizePersonName(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertEntity(context.Background(), entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID, EntityType: entitycatalog.EntityTypePerson, CanonicalName: identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 1,
		SourceURL: sourceURL, Width: 1200, Height: 1600,
		Status:         entitycatalog.CandidateStatusActive,
		SemanticStatus: entitycatalog.CandidateSemanticAccepted, SemanticScore: 1, TechnicalScore: 0.9,
		QualityReason: qualityReason, VerifierVersion: entitycatalog.CandidateVerifierVersion,
	}); err != nil {
		t.Fatal(err)
	}
}

type personEntityMaxEmptySearcher struct{ calls atomic.Int32 }

func (s *personEntityMaxEmptySearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	return nil, nil
}

// TestPersonEntityMaxTwoPeopleOneSceneKeepBothPools pins the multi-entity rule:
// two PEOPLE in one scene are two catalog identities, and neither may be
// labelled with the other's name and silently discarded by the semantic gate.
func TestPersonEntityMaxTwoPeopleOneSceneKeepBothPools(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Chiara Poggi", "https://images.example/chiara/1.jpg", "https://images.example/chiara/2.jpg")
	seedCatalogPerson(t, repo, "Alberto Stasi", "https://images.example/alberto/1.jpg", "https://images.example/alberto/2.jpg")
	searcher := &catalogIntegrationSearcher{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), personEntityMaxInput("two-people", "Chiara Poggi", "Alberto Stasi"))
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 0 {
		t.Fatalf("provider calls = %d, want 0 when both identities are in the catalog", got)
	}
	perEntity := map[string]int{}
	for _, candidate := range result.VidRushSegments[0].Assets.SecondaryImages {
		perEntity[candidate.Entity]++
	}
	for _, name := range []string{"Chiara Poggi", "Alberto Stasi"} {
		if perEntity[name] == 0 {
			t.Fatalf("no catalog candidate for %q; got pools %v", name, perEntity)
		}
	}
	for _, name := range []string{"Chiara Poggi", "Alberto Stasi"} {
		identity, err := entitycatalog.CanonicalizePersonName(name)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
		if err != nil || len(rows) != 2 {
			t.Fatalf("%s catalog rows = %d, err=%v; want 2", name, len(rows), err)
		}
	}
}

// TestPersonEntityMaxReusePrefersUnusedVariant pins the run-scoped ledger: two
// scenes of the same person with two verified variants bind two different
// images instead of repeating the first one.
func TestPersonEntityMaxReusePrefersUnusedVariant(t *testing.T) {
	const name = "Chiara Poggi"
	first := personEntityMaxReadyCandidate("chiara-a", name, "https://images.example/chiara-a.jpg")
	second := personEntityMaxReadyCandidate("chiara-b", name, "https://images.example/chiara-b.jpg")
	spec := scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
		personEntityMaxScene("scene-a", name),
		personEntityMaxScene("scene-b", name),
	}}
	segments := []scriptpkg.VidRushSegmentResult{
		personEntityMaxSegment("scene-a", name, first, second),
		personEntityMaxSegment("scene-b", name, first, second),
	}

	out := projectEntityImageBindings(spec, segments, mediadomain.EntityImagePolicy{Enabled: true, EntityTypes: []string{"PERSON"}})
	bound := make([]string, 0, len(out.Scenes))
	for i := range out.Scenes {
		image := out.Scenes[i].Annotations.PrimaryEntities[0].Image
		if image == nil || image.AssetID == "" {
			t.Fatalf("scene %s bound no image: %+v", out.Scenes[i].ID, image)
		}
		bound = append(bound, image.AssetID)
	}
	if len(bound) != 2 {
		t.Fatalf("bound %d images, want 2", len(bound))
	}
	if bound[0] == bound[1] {
		t.Fatalf("both scenes bound %q, want the unused variant preferred", bound[0])
	}
}

// TestPersonEntityMaxReplayIsIdentical pins deterministic selection: the same
// warm catalog and the same input must produce the same asset sequence on a
// second run. Binding happens later, after materialization, and is covered by
// the binding tests above.
func TestPersonEntityMaxReplayIsIdentical(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Chiara Poggi",
		"https://images.example/chiara/1.jpg",
		"https://images.example/chiara/2.jpg",
		"https://images.example/chiara/3.jpg")
	searcher := &catalogIntegrationSearcher{}
	plan := catalogPersonPlan()
	input := catalogPersonInput("replay", "Chiara Poggi")

	run := func() []string {
		result, err := NewMediaResolverImageStageWithCatalog(searcher, nil, repo).Process(context.Background(), plan, input)
		if err != nil {
			t.Fatal(err)
		}
		assets := make([]string, 0, len(result.VidRushSegments[0].Assets.SecondaryImages))
		for _, candidate := range result.VidRushSegments[0].Assets.SecondaryImages {
			assets = append(assets, candidate.AssetID)
		}
		return assets
	}

	firstAssets := run()
	secondAssets := run()
	if len(firstAssets) == 0 {
		t.Fatalf("warm catalog replay returned no candidates")
	}
	if strings.Join(firstAssets, ",") != strings.Join(secondAssets, ",") {
		t.Fatalf("replay asset sequence changed: %v vs %v", firstAssets, secondAssets)
	}
}

// TestPersonEntityMaxAcceptedRowWithConflictingSourceNameIsNotSelected pins the
// mismatch rule at catalog level: an accepted verdict is not proof of identity
// when the source filename names a different person, so the row cannot be
// selected even though it stays accepted for audit.
func TestPersonEntityMaxAcceptedRowWithConflictingSourceNameIsNotSelected(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedPersonEntityMaxRow(t, repo, "Chiara Poggi", "https://images.example/chiara-stasi-portrait.jpg", "manual visual identity review confirmed")
	searcher := &personEntityMaxEmptySearcher{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("mismatch", "Chiara Poggi"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 0 {
		t.Fatalf("mismatched accepted row selected %d image(s), want 0", got)
	}
	identity, err := entitycatalog.CanonicalizePersonName("Chiara Poggi")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SemanticStatus != entitycatalog.CandidateSemanticAccepted {
		t.Fatalf("accepted row was mutated by a catalog read: %+v", rows)
	}
}
