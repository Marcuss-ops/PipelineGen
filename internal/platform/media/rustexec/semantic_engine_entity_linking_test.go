package rustexec

import (
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

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
				Features: []scriptgen.SemanticLinkFeature{{
					Name: "alias_exactness", Value: 1, Terms: []string{"Nike"},
					Spans: []scriptgen.SemanticLinkEvidenceSpan{{SceneID: "s1", StartByte: 0, EndByte: 4}},
				}},
			}},
			LinkMargin: &margin, LinkRule: "entity_linker.v1",
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
		identity.SearchIntents[1].Query != "Nike statue Athens" || identity.LinkRule != "entity_linker.v1" ||
		identity.LinkMargin == nil || *identity.LinkMargin != margin || len(identity.LinkCandidates) != 1 ||
		identity.LinkCandidates[0].Features[0].Spans[0].EndByte != 4 {
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
