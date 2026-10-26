package rustexec

import "testing"

func TestGroundedContextQueryRequiresExactSourceEvidenceAndComponents(t *testing.T) {
	source := "Il reperto conservato viene sottoposto a nuove analisi scientifiche."
	evidence := source
	query := semanticEngineQuery{
		Kind: "context_phrase", Text: "reperto conservato nuove analisi scientifiche",
		Origin: "derived_from_source_context", Illustrative: true,
		Evidence: &evidence, EvidenceStartByte: contextQueryIntPtr(0), EvidenceEndByte: contextQueryIntPtr(len(source)),
		Components: []semanticEngineQueryComponent{
			{Kind: "entity", ComponentType: "OBJECT", Text: "reperto conservato", StartByte: 3, EndByte: 21},
			{Kind: "visual_context", ComponentType: "VISUAL_CONCEPT", Text: "nuove analisi scientifiche", StartByte: 41, EndByte: 67},
		},
	}
	if !groundedContextQuery(source, query) {
		t.Fatal("valid contextual query was rejected")
	}
	query.Components[1].EndByte--
	if groundedContextQuery(source, query) {
		t.Fatal("query with a component span that does not match source was accepted")
	}
}

func TestGroundedSemanticTemplateQueryRequiresDeclaredIllustrativeTerms(t *testing.T) {
	source := "Il reperto conservato viene sottoposto a nuove analisi scientifiche."
	evidence := source
	template := "it.OBJECT"
	query := semanticEngineQuery{
		Kind: "context_phrase", Text: "reperto conservato laboratorio forense nuove analisi scientifiche",
		Origin: "derived_from_semantic_template", Illustrative: true, TemplateID: &template,
		DerivedTerms: []string{"laboratorio", "forense"},
		Evidence:     &evidence, EvidenceStartByte: contextQueryIntPtr(0), EvidenceEndByte: contextQueryIntPtr(len(source)),
		Components: []semanticEngineQueryComponent{
			{Kind: "entity", ComponentType: "OBJECT", Text: "reperto conservato", StartByte: 3, EndByte: 21},
			{Kind: "visual_context", ComponentType: "VISUAL_CONCEPT", Text: "nuove analisi scientifiche", StartByte: 41, EndByte: 67},
		},
	}
	if !groundedContextQuery(source, query) {
		t.Fatal("valid semantic template query was rejected")
	}
	query.DerivedTerms = []string{"prova inesistente"}
	if groundedContextQuery(source, query) {
		t.Fatal("undeclared words in the semantic template query were accepted")
	}
}

func contextQueryIntPtr(value int) *int { return &value }
