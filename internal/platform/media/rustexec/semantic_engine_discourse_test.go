package rustexec

import "testing"

func TestProjectSemanticDiscoursePreservesGroundedReferencesAndThreads(t *testing.T) {
	source := "Maya said she returned."
	mayaStart := 0
	sheStart := len("Maya said ")
	refs := []semanticEngineDiscourseReference{
		{ID: "dref_0001", SceneID: "s1", SentenceIndex: 0, Mention: "Maya", Type: "explicit_name", Status: "resolved", TargetEntityIDs: []string{"person_01"}, Evidence: source, StartByte: mayaStart, EndByte: mayaStart + len("Maya"), SentenceStartByte: 0, SentenceEndByte: len(source)},
		{ID: "dref_0002", SceneID: "s1", SentenceIndex: 0, Mention: "she", Type: "pronoun", Status: "resolved", TargetEntityIDs: []string{"person_01"}, Evidence: source, StartByte: sheStart, EndByte: sheStart + len("she"), SentenceStartByte: 0, SentenceEndByte: len(source)},
	}
	discourse := &semanticEngineDiscourseProjection{
		Schema: "discourse.v1", References: refs,
		Threads: []semanticEngineEntityThread{{
			EntityID: "person_01", CanonicalSurface: "Maya", Type: "PERSON", SceneIDs: []string{"s1"},
			FirstSceneID: "s1", LastSceneID: "s1", MentionCount: 2,
			Beats: []semanticEngineThreadBeat{{ReferenceID: "dref_0001", SceneID: "s1", SentenceIndex: 0, State: "starts"}, {ReferenceID: "dref_0002", SceneID: "s1", SentenceIndex: 0, State: "continues"}},
		}},
		SceneContinuity: []semanticEngineSceneContinuity{{SceneID: "s1", Status: "start", ActiveEntityIDs: []string{"person_01"}, EnteredEntityIDs: []string{"person_01"}}},
		Counts:          semanticEngineDiscourseCounts{Total: 2, Resolved: 2, Threads: 1},
	}
	projected, err := projectSemanticDiscourse(map[string]string{"s1": source}, []semanticEngineLinkedEntity{{LocalID: "person_01"}}, discourse)
	if err != nil {
		t.Fatalf("projectSemanticDiscourse: %v", err)
	}
	if len(projected.References) != 2 || projected.References[1].Mention != "she" ||
		len(projected.Threads) != 1 || projected.Counts.Resolved != 2 {
		t.Fatalf("projected discourse = %+v", projected)
	}
}

func TestProjectSemanticDiscourseRejectsUngroundedReference(t *testing.T) {
	source := "Maya said she returned."
	discourse := &semanticEngineDiscourseProjection{
		Schema: "discourse.v1",
		References: []semanticEngineDiscourseReference{{
			ID: "dref_0001", SceneID: "s1", Mention: "Maya", Type: "explicit_name", Status: "resolved",
			TargetEntityIDs: []string{"person_01"}, Evidence: source, StartByte: 0, EndByte: len("Maya"),
			SentenceStartByte: 0, SentenceEndByte: len(source) - 1,
		}},
	}
	_, err := projectSemanticDiscourse(map[string]string{"s1": source}, []semanticEngineLinkedEntity{{LocalID: "person_01"}}, discourse)
	if err == nil {
		t.Fatal("discourse reference with ungrounded sentence evidence passed")
	}
}
