package script

import "testing"

func TestGenerationEnvelopeResearchSourceIsKnown(t *testing.T) {
	e := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{ID: "research", Source: SourceSpec{Type: SourceResearch, Topic: "topic"}, ScriptParams: ScriptSpec{TargetWords: 100}}}}
	if err := e.Validate(); err != nil {
		t.Fatalf("research source rejected: %v", err)
	}
}
