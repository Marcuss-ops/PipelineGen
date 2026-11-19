package rustexec

import (
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func bundleWithGroup(sceneID, groupID string) *scriptgen.SemanticCandidateBundle {
	return &scriptgen.SemanticCandidateBundle{
		Groups: []scriptgen.SemanticGroup{{ID: groupID, Type: "chronological"}},
	}
}

// A group id declared by two scenes cannot be attributed to either scene from
// the link alone, so consolidation refuses it instead of binding the wrong
// composition.
func TestIndexSemanticGroupScenesRejectsAGroupIDSharedByTwoScenes(t *testing.T) {
	bundles := map[string]*scriptgen.SemanticCandidateBundle{
		"s01": bundleWithGroup("s01", "chronology_01"),
		"s02": bundleWithGroup("s02", "chronology_01"),
	}

	if _, err := indexSemanticGroupScenes(bundles); err == nil {
		t.Fatal("a group id declared by two scenes must fail closed")
	} else if !strings.Contains(err.Error(), "more than one scene") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIndexSemanticGroupScenesScopesTheGroupToItsScene(t *testing.T) {
	bundles := map[string]*scriptgen.SemanticCandidateBundle{
		"s01": bundleWithGroup("s01", "chronology_s01_01"),
		"s02": bundleWithGroup("s02", "chronology_s02_01"),
	}

	index, err := indexSemanticGroupScenes(bundles)
	if err != nil {
		t.Fatalf("distinct scene-scoped ids must be accepted: %v", err)
	}
	if _, ok := index["chronology_s01_01"]["s01"]; !ok {
		t.Fatalf("s01 group must be attributed to s01: %v", index)
	}
	if _, ok := index["chronology_s01_01"]["s02"]; ok {
		t.Fatalf("s01 group must not be attributed to s02: %v", index)
	}
}

// An event link names a group and the scene of its target member. If the group
// belongs to another scene, the link would silently bind the wrong timeline.
func TestProjectSemanticConsolidationRejectsAnEventLinkAcrossScenes(t *testing.T) {
	texts := map[string]string{
		"s01": "Nel 2009 fu assolto. Nel 2011 fu confermata la condanna.",
		"s02": "Nel 2016 fu riaperto il caso. Nel 2020 arrivò la sentenza.",
	}
	bundles := map[string]*scriptgen.SemanticCandidateBundle{
		"s01": bundleWithGroup("s01", "chronology_s01_01"),
		"s02": bundleWithGroup("s02", "chronology_s02_01"),
	}
	wire := &semanticEngineConsolidation{
		Schema: "visual_consolidation.v1",
		EventLinks: []semanticEngineEventLink{{
			ID:        "el_0001",
			GroupID:   "chronology_s01_01",
			SceneID:   "s02",
			Kind:      "precedes",
			Evidence:  "Nel 2016 fu riaperto il caso.",
			StartByte: 0,
			EndByte:   len("Nel 2016 fu riaperto il caso."),
		}},
	}

	if _, err := projectSemanticConsolidation(texts, wire, bundles); err == nil {
		t.Fatal("a link pointing at another scene's group must fail closed")
	} else if !strings.Contains(err.Error(), "of another scene") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectSemanticConsolidationKeepsAScopedEventLink(t *testing.T) {
	const evidence = "Nel 2020 arrivò la sentenza."
	texts := map[string]string{
		"s01": "Nel 2009 fu assolto. Nel 2011 fu confermata la condanna.",
		"s02": "Nel 2016 fu riaperto il caso. " + evidence,
	}
	bundles := map[string]*scriptgen.SemanticCandidateBundle{
		"s01": bundleWithGroup("s01", "chronology_s01_01"),
		"s02": bundleWithGroup("s02", "chronology_s02_01"),
	}
	start := strings.Index(texts["s02"], evidence)
	wire := &semanticEngineConsolidation{
		Schema: "visual_consolidation.v1",
		EventLinks: []semanticEngineEventLink{{
			ID:              "el_0001",
			GroupID:         "chronology_s02_01",
			SceneID:         "s02",
			FromCandidateID: "c_0001",
			ToCandidateID:   "c_0002",
			Kind:            "precedes",
			Evidence:        evidence,
			StartByte:       start,
			EndByte:         start + len(evidence),
		}},
	}

	projected, err := projectSemanticConsolidation(texts, wire, bundles)
	if err != nil {
		t.Fatalf("a scene-scoped link must be accepted: %v", err)
	}
	if len(projected.EventLinks) != 1 {
		t.Fatalf("event links = %d, want 1", len(projected.EventLinks))
	}
	if got := projected.EventLinks[0].GroupID; got != "chronology_s02_01" {
		t.Fatalf("group id = %q, want chronology_s02_01", got)
	}
}
