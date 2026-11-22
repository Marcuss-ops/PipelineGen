package rustexec

import (
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestProjectSemanticLinkedEntitiesRepairsIntentOrder pins the boundary rule:
// a named entity whose worker response leads with a contextual query must not
// abort the run. The identity anchor is moved first, and a named entity with
// no anchor at all keeps no intents instead of a subject-less contextual one.
func TestProjectSemanticLinkedEntitiesRepairsIntentOrder(t *testing.T) {
	projected, err := projectSemanticLinkedEntities(
		map[string]string{"s1": "Corrado Augias, giornalista e scrittore ha scritto un libro."},
		[]semanticEngineLinkedEntity{
			{
				LocalID: "person_01", SurfaceKey: "corrado-augias", CanonicalSurface: "Corrado Augias",
				LinkStatus: "unlinked", Role: "named_entity", EntityType: "PERSON",
				Mentions: []semanticEngineMention{{SceneID: "s1", Surface: "Corrado Augias", StartByte: 0, EndByte: 14}},
				SearchIntents: []semanticEngineSearchIntent{
					{Kind: "contextual", Query: "Corrado Augias Roma", Lang: "it"},
					{Kind: "identity", Query: "Corrado Augias", Lang: "it"},
				},
			},
			{
				LocalID: "person_02", SurfaceKey: "long-surface", CanonicalSurface: "Corrado Augias, giornalista e scrittore",
				LinkStatus: "unlinked", Role: "named_entity", EntityType: "PERSON",
				Mentions: []semanticEngineMention{{SceneID: "s1", Surface: "Corrado Augias, giornalista e scrittore", StartByte: 0, EndByte: 39}},
				SearchIntents: []semanticEngineSearchIntent{
					{Kind: "contextual", Query: "Corrado Augias Roma", Lang: "it"},
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("intent order must be repaired, not fatal: %v", err)
	}
	if got := projected[0].SearchIntents; len(got) != 2 || got[0].Kind != "identity" || got[1].Kind != "contextual" {
		t.Fatalf("intents = %+v, want identity first then contextual", got)
	}
	if got := projected[1].SearchIntents; len(got) != 0 {
		t.Fatalf("an anchorless named entity must keep no intents, got %+v", got)
	}
}

func TestProjectSemanticLinkedEntitiesCarriesV3RoleIntentsAndConstraints(t *testing.T) {
	canonicalID := "wd:Q42262"
	gloss := "sportswear company"
	margin := 3
	projected, err := projectSemanticLinkedEntities(
		map[string]string{"s1": "Nike ha presentato la collezione."},
		[]semanticEngineLinkedEntity{{
			LocalID: "org_01", SurfaceKey: "nike", CanonicalSurface: "Nike",
			CanonicalID: &canonicalID, LinkStatus: "linked", CanonicalName: "Nike",
			EntityType: "ORG", Role: "named_entity", DisambiguationGloss: &gloss,
			NegativeConstraints: []string{"statue", "ancient Greek goddess"},
			Mentions:            []semanticEngineMention{{SceneID: "s1", Surface: "Nike", StartByte: 0, EndByte: 4}},
			SearchIntents: []semanticEngineSearchIntent{
				{Kind: "identity", Query: "Nike sportswear", Lang: "en"},
				{Kind: "contextual", Query: "Nike statue Athens", Lang: "en"},
			},
			LinkCandidates: []scriptgen.SemanticLinkCandidate{{
				KBID: canonicalID, Name: "Nike sportswear", Score: 4,
				Features: []scriptgen.SemanticLinkFeature{
					{
						Name: "alias_exactness", Value: 1, Terms: []string{"Nike"},
						Spans: []scriptgen.SemanticLinkEvidenceSpan{{SceneID: "s1", StartByte: 0, EndByte: 4}},
					},
					{Name: "type_prior", Value: 1, Terms: []string{"ORG"}},
					{Name: "context_overlap", Value: 3},
					{Name: "counter_penalty", Value: 0},
					{Name: "type_cue", Value: 1},
					{
						Name: "discourse_prior", Value: 2, Terms: []string{"Nike"},
						Spans: []scriptgen.SemanticLinkEvidenceSpan{{SceneID: "s1", StartByte: 0, EndByte: 4}},
					},
				},
			}},
			LinkMargin: &margin, LinkRule: "entity_linker.v2",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 {
		t.Fatalf("projected = %+v", projected)
	}
	identity := projected[0]
	if identity.CanonicalID != canonicalID || identity.LinkStatus != "linked" || identity.Role != "named_entity" ||
		identity.DisambiguationGloss != gloss || len(identity.NegativeConstraints) != 2 ||
		len(identity.SearchIntents) != 2 || identity.SearchIntents[0].Kind != "identity" ||
		identity.SearchIntents[1].Query != "Nike statue Athens" || identity.LinkRule != "entity_linker.v2" ||
		identity.LinkMargin == nil || *identity.LinkMargin != margin || len(identity.LinkCandidates) != 1 ||
		len(identity.LinkCandidates[0].Features) != 6 || identity.LinkCandidates[0].Features[0].Spans[0].EndByte != 4 {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestProjectSemanticLinkedEntitiesRejectsUnresolvedLinkMetadata(t *testing.T) {
	canonicalID := "wd:Q42262"
	_, err := projectSemanticLinkedEntities(map[string]string{"s1": "Nike"}, []semanticEngineLinkedEntity{{
		LocalID: "org_01", SurfaceKey: "nike", CanonicalSurface: "Nike",
		CanonicalID: &canonicalID, LinkStatus: "unlinked", CanonicalName: "Nike",
		EntityType: "ORG", Role: "named_entity",
		Mentions: []semanticEngineMention{{SceneID: "s1", Surface: "Nike", StartByte: 0, EndByte: 4}},
	}})
	if err == nil {
		t.Fatal("an unlinked entity with a canonical id must fail closed")
	}
}

func TestProjectSemanticLinkedEntitiesRejectsSevenLinkFeatures(t *testing.T) {
	canonicalID := "wd:Q42262"
	features := make([]scriptgen.SemanticLinkFeature, 7)
	for index := range features {
		features[index] = scriptgen.SemanticLinkFeature{Name: "feature"}
	}
	_, err := projectSemanticLinkedEntities(map[string]string{"s1": "Nike"}, []semanticEngineLinkedEntity{{
		LocalID: "org_01", SurfaceKey: "nike", CanonicalSurface: "Nike",
		CanonicalID: &canonicalID, LinkStatus: "linked", CanonicalName: "Nike",
		EntityType: "ORG", Role: "named_entity",
		Mentions: []semanticEngineMention{{SceneID: "s1", Surface: "Nike", StartByte: 0, EndByte: 4}},
		LinkCandidates: []scriptgen.SemanticLinkCandidate{{
			KBID: canonicalID, Name: "Nike sportswear", Features: features,
		}},
	}})
	if err == nil {
		t.Fatal("seven link features must exceed the contract limit")
	}
}
