package vidrush

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func acceptedPersonVerdict(entity, entityID, sourceURL string) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		Provider: scriptpkg.VidRushProviderInternetImages, Entity: entity, EntityID: entityID,
		Query: entity, SourceURL: sourceURL, SemanticStatus: entitycatalog.CandidateSemanticAccepted,
		SemanticScore: 0.93, VerifierVersion: entitycatalog.CandidateVerifierVersion,
		QualityReason: "Ollama visual identity verification confirmed the requested person and one dominant subject",
	}
}

// TestPersistPersonImageVisualVerdictsRefusesNonPersonEntity pins the guard that
// kept contextual discovery results out of the PERSON catalog: an organization
// or place candidate must never be canonicalized into a person:* pool.
func TestPersistPersonImageVisualVerdictsRefusesNonPersonEntity(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	identity, err := entitycatalog.CanonicalizePersonName("Corrado Augias")
	if err != nil {
		t.Fatal(err)
	}
	results := []scriptpkg.SegmentAssetCandidate{
		acceptedPersonVerdict("Tripoli 1930s colonial streets", "place:tripoli", "https://images.example/place/tripoli.jpg"),
		acceptedPersonVerdict("RAI newsroom", "org:rai", "https://images.example/org/rai.jpg"),
	}
	if err := persistPersonImageVisualVerdicts(context.Background(), repo, identity, nil, results); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if len(repo.candidates) != 0 {
		t.Fatalf("catalog rows = %d, want 0 — a non-PERSON candidate must not enter the PERSON catalog", len(repo.candidates))
	}
}

// TestPersistPersonImageVisualVerdictsDoesNotMintSecondPerson pins the
// established-identity contract: a query variant that resolves to a different
// person is skipped instead of fragmenting the pool into
// person:<query-with-suffix>.
func TestPersistPersonImageVisualVerdictsDoesNotMintSecondPerson(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	identity, err := entitycatalog.CanonicalizePersonName("Corrado Augias")
	if err != nil {
		t.Fatal(err)
	}
	results := []scriptpkg.SegmentAssetCandidate{
		acceptedPersonVerdict("Somebody Else Entirely", "", "https://images.example/somebody.jpg"),
	}
	if err := persistPersonImageVisualVerdicts(context.Background(), repo, identity, nil, results); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if len(repo.candidates) != 0 {
		t.Fatalf("catalog rows = %d, want 0 — a different person must not be written under the established identity", len(repo.candidates))
	}
}

// TestPersistPersonImageVisualVerdictsResolvesAliasOntoEstablishedIdentity
// proves the positive half: the segment alias graph collapses an alias surface
// onto the canonical row the caller selected.
func TestPersistPersonImageVisualVerdictsResolvesAliasOntoEstablishedIdentity(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	graph := entitycatalog.NewPersonAliasGraph()
	if err := graph.Register("person:corrado-augias", "Augias"); err != nil {
		t.Fatal(err)
	}
	identity, err := entitycatalog.CanonicalizePersonIdentityWithAliases("Corrado Augias", "person:corrado-augias", graph)
	if err != nil {
		t.Fatal(err)
	}
	results := []scriptpkg.SegmentAssetCandidate{
		acceptedPersonVerdict("Augias", "", "https://images.example/augias.jpg"),
	}
	if err := persistPersonImageVisualVerdicts(context.Background(), repo, identity, graph, results); err != nil {
		t.Fatalf("persist: %v", err)
	}
	rows, err := repo.ListCandidates(context.Background(), "person:corrado-augias", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SourceURL != "https://images.example/augias.jpg" {
		t.Fatalf("rows = %+v, want the alias verdict stored on person:corrado-augias", rows)
	}
	for id := range repo.entities {
		if id != "person:corrado-augias" {
			t.Fatalf("unexpected catalog entity %q — the alias must not mint a second pool", id)
		}
	}
}

// TestPersonCandidateIdentityForSegmentRejectsContextualQuery pins the
// classification that decides whether a fan-out candidate is a PERSON at all.
func TestPersonCandidateIdentityForSegmentRejectsContextualQuery(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "ctx", Text: "Tripoli negli anni Trenta.",
		Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Corrado Augias", Type: "PERSON"},
			{Value: "Tripoli", Type: "LOCATION"},
		}},
	}
	graph := segmentPersonAliasGraph(segment)
	if _, ok, err := personCandidateIdentityForSegment(segment, graph, acceptedPersonVerdict("Corrado Augias", "", "https://images.example/a.jpg")); err != nil || !ok {
		t.Fatalf("person candidate accepted=%v err=%v, want true", ok, err)
	}
	contextual := acceptedPersonVerdict("Tripoli Libya 1930s colonial streets archival photograph", "", "https://images.example/t.jpg")
	contextual.Query = "Tripoli Libya 1930s colonial streets archival photograph"
	if identity, ok, err := personCandidateIdentityForSegment(segment, graph, contextual); err != nil || ok {
		t.Fatalf("contextual identity = %+v, ok=%v, err=%v — must not resolve as a PERSON", identity, ok, err)
	}
}
