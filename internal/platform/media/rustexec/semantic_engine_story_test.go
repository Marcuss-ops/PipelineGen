package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func scriptViewsFixture(t *testing.T) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal([]byte(semanticFixture), &response); err != nil {
		t.Fatal(err)
	}
	manifest := response["manifest"].(map[string]any)
	manifest["analyzers"].(map[string]any)["lexicon"] = "lexicon-test"
	scenes := manifest["scenes"].([]any)
	first := scenes[0].(map[string]any)
	first["entity_facts"] = []any{map[string]any{"id": "f1", "subject_entity_id": "person_01", "predicate": "founded", "value": "Marco Rossi", "type": "PERSON", "status": "asserted", "importance": 0.7, "sentence_index": 0, "evidence": "Marco Rossi founded the studio.", "start_byte": 0, "end_byte": 11, "evidence_start_byte": 0, "evidence_end_byte": 31}}
	manifest["story_graph"] = map[string]any{"nodes": []any{
		map[string]any{"id": "identity:person_01", "kind": "identity", "label": "Marco Rossi", "scene_id": "scene_01", "scene_ids": []string{"scene_01"}, "entity_id": "person_01", "evidence": "Marco Rossi", "start_byte": 0, "end_byte": 11},
		map[string]any{"id": "fact:scene_01:f1", "kind": "fact", "label": "Marco Rossi", "scene_id": "scene_01", "scene_ids": []string{"scene_01"}, "entity_id": "person_01", "fact_id": "f1", "sentence_index": 0, "evidence": "Marco Rossi founded the studio.", "start_byte": 0, "end_byte": 31},
	}, "edges": []any{map[string]any{"id": "about:f1", "kind": "about", "from_id": "identity:person_01", "to_id": "fact:scene_01:f1", "cross_scene": false, "evidence": "Marco Rossi founded the studio.", "start_byte": 0, "end_byte": 31}}}
	manifest["visual_coverage"] = map[string]any{"total_facts": 1, "covered_facts": 0, "scenes": []any{
		map[string]any{"scene_id": "scene_01", "facts": []any{map[string]any{"fact_id": "f1", "predicate": "founded", "subject_entity_id": "person_01", "importance": 0.7, "status": "uncovered", "proposal_types": []string{}, "sentence_index": 0, "evidence": "Marco Rossi founded the studio.", "start_byte": 0, "end_byte": 11}}},
		map[string]any{"scene_id": "scene_02", "facts": []any{}},
	}}
	return response
}

func TestSemanticScriptViewsPreserveEveryWireField(t *testing.T) {
	response := scriptViewsFixture(t)
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: body}))
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatal(err)
	}
	first, second := bundles["scene_01"], bundles["scene_02"]
	if first.StoryGraph == nil || first.StoryGraph != second.StoryGraph || first.VisualCoverage == nil || first.VisualCoverage != second.VisualCoverage || first.Analyzers.Lexicon != "lexicon-test" {
		t.Fatalf("whole-script views/provenance lost: %+v", first)
	}
	for key, view := range map[string]any{"story_graph": first.StoryGraph, "visual_coverage": first.VisualCoverage} {
		actual, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(actual, &decoded); err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(response["manifest"].(map[string]any)[key])
		got, _ := json.Marshal(decoded)
		if string(expected) != string(got) {
			t.Fatalf("%s lost fields: want %s got %s", key, expected, got)
		}
	}
	bullet := first.Bullets[0]
	if bullet.ID != "bullet_01" || bullet.Kind != "SENTENCE" || bullet.Evidence != bullet.Text || bullet.Provenance != "visualner" {
		t.Fatalf("bullet provenance lost: %+v", bullet)
	}
}

func TestSemanticScriptViewsRejectCorruption(t *testing.T) {
	for _, name := range []string{"node_span", "unknown_identity", "unknown_fact", "edge_target", "edge_span", "cross_scene", "coverage_total", "coverage_fact", "coverage_status", "coverage_missing_scene"} {
		t.Run(name, func(t *testing.T) {
			response := scriptViewsFixture(t)
			manifest := response["manifest"].(map[string]any)
			graph := manifest["story_graph"].(map[string]any)
			nodes := graph["nodes"].([]any)
			edge := graph["edges"].([]any)[0].(map[string]any)
			coverage := manifest["visual_coverage"].(map[string]any)
			scenes := coverage["scenes"].([]any)
			fact := scenes[0].(map[string]any)["facts"].([]any)[0].(map[string]any)
			switch name {
			case "node_span":
				nodes[0].(map[string]any)["end_byte"] = 12
			case "unknown_identity":
				nodes[0].(map[string]any)["entity_id"] = "missing"
			case "unknown_fact":
				nodes[1].(map[string]any)["fact_id"] = "missing"
			case "edge_target":
				edge["to_id"] = "missing"
			case "edge_span":
				edge["end_byte"] = 30
			case "cross_scene":
				edge["cross_scene"] = true
			case "coverage_total":
				coverage["total_facts"] = 2
			case "coverage_fact":
				fact["fact_id"] = "missing"
			case "coverage_status":
				fact["status"] = "covered"
			case "coverage_missing_scene":
				coverage["scenes"] = scenes[:1]
			}
			body, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: body}))
			if _, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes()); err == nil || (!strings.Contains(err.Error(), "story") && !strings.Contains(err.Error(), "coverage")) {
				t.Fatalf("corrupt %s accepted or wrong failure: %v", name, err)
			}
		})
	}
}

func TestSemanticGroupsPreserveNormalizedDate(t *testing.T) {
	date := "2008-03-15"
	source := "First event. Second event."
	groups, err := projectSemanticGroups("s", map[string]string{"s": source}, []semanticEngineGroup{{ID: "g", GroupType: "chronological", Title: "Events", Members: []semanticEngineGroupMember{
		{CandidateID: "a", Text: "First event.", Evidence: "First event.", StartByte: 0, EndByte: 12, NormalizedDate: &date},
		{CandidateID: "b", Text: "Second event.", Evidence: "Second event.", StartByte: 13, EndByte: 26},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if groups[0].Members[0].NormalizedDate != date || groups[0].Members[1].NormalizedDate != "" {
		t.Fatalf("date presence lost: %+v", groups)
	}
}
