package rustexec

import "testing"

func TestProjectSemanticLinkedEntitiesCarriesV3RoleIntentsAndConstraints(t *testing.T) {
	canonicalID := "wd:Q42262"
	gloss := "sportswear company"
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
		identity.SearchIntents[1].Query != "Nike statue Athens" {
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
