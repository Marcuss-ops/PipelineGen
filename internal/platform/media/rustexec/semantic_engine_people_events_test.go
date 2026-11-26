package rustexec

import (
	"strings"
	"testing"
)

func TestProjectSemanticPersonRolesAndAgesValidatesGrounding(t *testing.T) {
	source := "Marco Rossi, CEO of Acme, is 45 years old."
	roleStart := strings.Index(source, "CEO")
	ageStart := strings.Index(source, "45")
	identities := []semanticEngineIdentity{{ID: "person_01"}, {ID: "org_01"}}
	roles, err := projectSemanticPersonRoles(source, identities, []semanticEnginePersonRole{{
		ID: "pr_01", PersonID: "person_01", RoleCode: "chief_executive", RoleSurface: "CEO",
		Gender: "unknown", Rank: "head", Tense: "current", Status: "asserted", OrganizationID: stringPtr("org_01"),
		StartByte: roleStart, EndByte: roleStart + len("CEO"), SentenceIndex: 0, Evidence: source,
	}})
	if err != nil {
		t.Fatalf("projectSemanticPersonRoles: %v", err)
	}
	ages, err := projectSemanticPersonAges(source, identities, []semanticEnginePersonAgeFact{{
		PersonID: "person_01", Age: 45, StartByte: ageStart, EndByte: ageStart + 2,
		SentenceIndex: 0, Evidence: source,
	}})
	if err != nil {
		t.Fatalf("projectSemanticPersonAges: %v", err)
	}
	if len(roles) != 1 || roles[0].OrganizationID != "org_01" || len(ages) != 1 || ages[0].Age != 45 {
		t.Fatalf("roles=%+v ages=%+v", roles, ages)
	}
}

func TestProjectSemanticPersonAgesAcceptsGroundedSpelledNumbers(t *testing.T) {
	source := "Chiara Poggi aveva ventisei anni."
	start := strings.Index(source, "ventisei")
	identities := []semanticEngineIdentity{{ID: "person_01"}}
	valid := semanticEnginePersonAgeFact{PersonID: "person_01", Age: 26, StartByte: start, EndByte: start + len("ventisei"), Evidence: source}
	if ages, err := projectSemanticPersonAges(source, identities, []semanticEnginePersonAgeFact{valid}); err != nil || len(ages) != 1 || ages[0].Age != 26 {
		t.Fatalf("spelled age was rejected: ages=%+v err=%v", ages, err)
	}
	withUnit := "Maya Chen was thirty years old."
	unitStart := strings.Index(withUnit, "thirty years")
	unitAge := semanticEnginePersonAgeFact{PersonID: "person_01", Age: 30, StartByte: unitStart, EndByte: unitStart + len("thirty years"), Evidence: withUnit}
	if ages, err := projectSemanticPersonAges(withUnit, identities, []semanticEnginePersonAgeFact{unitAge}); err != nil || len(ages) != 1 {
		t.Fatalf("spelled age with embedded unit was rejected: ages=%+v err=%v", ages, err)
	}
}

func TestProjectSemanticPersonRelationsAndEventsRequiresGroundedEvidence(t *testing.T) {
	source := "Marco is the father of Luca."
	identities := []semanticEngineIdentity{{ID: "person_01"}, {ID: "person_02"}}
	relations, err := projectSemanticPersonRelations(source, identities, []semanticEnginePersonRelation{{
		FromID: "person_01", ToID: "person_02", Kind: "parent_of", Status: "asserted",
		StartByte: 0, EndByte: len(source) - 1, SentenceIndex: 0, Evidence: source,
	}})
	if err != nil {
		t.Fatalf("projectSemanticPersonRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].Kind != "parent_of" {
		t.Fatalf("relations = %+v", relations)
	}

	eventText := "Marco was arrested in Rome."
	triggerStart := strings.Index(eventText, "was arrested")
	events, err := projectSemanticEvents(map[string]string{"s1": eventText}, []semanticEngineSceneManifest{{SceneID: "s1"}}, []semanticEngineLinkedEntity{{LocalID: "person_01"}}, []semanticEngineEventFrame{{
		ID: "ev_01", EventType: "arrest", Trigger: "was arrested", TriggerStartByte: triggerStart,
		TriggerEndByte: triggerStart + len("was arrested"), Participants: []semanticEngineEventParticipant{{Role: "patient", EntityID: "person_01"}},
		Status: "asserted", SceneIDs: []string{"s1"}, Evidence: []semanticEngineEventEvidence{{SceneID: "s1", StartByte: 0, EndByte: len(eventText), Text: eventText}},
	}})
	if err != nil {
		t.Fatalf("projectSemanticEvents: %v", err)
	}
	if len(events) != 1 || events[0].Trigger != "was arrested" || events[0].Participants[0].EntityID != "person_01" {
		t.Fatalf("events = %+v", events)
	}
}

func TestProjectSemanticEventsCollapsesIdenticalDuplicateFrames(t *testing.T) {
	eventText := "Marco was arrested in Rome."
	triggerStart := strings.Index(eventText, "was arrested")
	event := semanticEngineEventFrame{
		ID: "ev_01", EventType: "arrest", Trigger: "was arrested", TriggerStartByte: triggerStart,
		TriggerEndByte: triggerStart + len("was arrested"), Participants: []semanticEngineEventParticipant{{Role: "patient", EntityID: "person_01"}},
		Status: "asserted", SceneIDs: []string{"s1"}, Evidence: []semanticEngineEventEvidence{{SceneID: "s1", StartByte: 0, EndByte: len(eventText), Text: eventText}},
	}
	events, err := projectSemanticEvents(map[string]string{"s1": eventText}, []semanticEngineSceneManifest{{SceneID: "s1"}}, []semanticEngineLinkedEntity{{LocalID: "person_01"}}, []semanticEngineEventFrame{event, event})
	if err != nil {
		t.Fatalf("identical duplicate event was rejected: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want the identical duplicate collapsed to 1", len(events))
	}
}

func TestProjectSemanticEventsCollapsesLabelOnlyDuplicateFrames(t *testing.T) {
	eventText := "Marco was arrested in Rome."
	triggerStart := strings.Index(eventText, "was arrested")
	event := semanticEngineEventFrame{
		ID: "ev_01", EventType: "arrest", Trigger: "was arrested", TriggerStartByte: triggerStart,
		TriggerEndByte: triggerStart + len("was arrested"), Participants: []semanticEngineEventParticipant{{Role: "patient", EntityID: "person_01"}},
		Status: "asserted", SceneIDs: []string{"s1"}, Evidence: []semanticEngineEventEvidence{{SceneID: "s1", StartByte: 0, EndByte: len(eventText), Text: eventText}},
	}
	relabelled := event
	relabelled.EventType = "match_competition_result"
	events, err := projectSemanticEvents(map[string]string{"s1": eventText}, []semanticEngineSceneManifest{{SceneID: "s1"}}, []semanticEngineLinkedEntity{{LocalID: "person_01"}}, []semanticEngineEventFrame{event, relabelled})
	if err != nil {
		t.Fatalf("same grounded event with a second label was rejected: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "arrest" {
		t.Fatalf("events = %+v, want one event keeping the first label", events)
	}
}

func TestProjectSemanticEventsRejectsConflictingDuplicateFrames(t *testing.T) {
	eventText := "Marco was arrested in Rome."
	triggerStart := strings.Index(eventText, "was arrested")
	event := semanticEngineEventFrame{
		ID: "ev_01", EventType: "arrest", Trigger: "was arrested", TriggerStartByte: triggerStart,
		TriggerEndByte: triggerStart + len("was arrested"), Participants: []semanticEngineEventParticipant{{Role: "patient", EntityID: "person_01"}},
		Status: "asserted", SceneIDs: []string{"s1"}, Evidence: []semanticEngineEventEvidence{{SceneID: "s1", StartByte: 0, EndByte: len(eventText), Text: eventText}},
	}
	conflicting := event
	conflicting.Trigger = "convicted"
	if _, err := projectSemanticEvents(map[string]string{"s1": eventText}, []semanticEngineSceneManifest{{SceneID: "s1"}}, []semanticEngineLinkedEntity{{LocalID: "person_01"}}, []semanticEngineEventFrame{event, conflicting}); err == nil {
		t.Fatal("conflicting duplicate event id passed")
	}
}

func TestProjectSemanticPersonRelationsDropsSelfReference(t *testing.T) {
	source := "Marco is the father of Luca."
	identities := []semanticEngineIdentity{{ID: "person_01"}, {ID: "person_02"}}
	relations, err := projectSemanticPersonRelations(source, identities, []semanticEnginePersonRelation{
		{
			FromID: "person_02", ToID: "person_02", Kind: "parent_of", Status: "asserted",
			StartByte: 0, EndByte: len(source), SentenceIndex: 0, Evidence: source,
		},
		{
			FromID: "person_01", ToID: "person_02", Kind: "parent_of", Status: "asserted",
			StartByte: 0, EndByte: len(source), SentenceIndex: 0, Evidence: source,
		},
	})
	if err != nil {
		t.Fatalf("self-referential relation must be dropped, not fatal: %v", err)
	}
	if len(relations) != 1 || relations[0].FromID != "person_01" || relations[0].ToID != "person_02" {
		t.Fatalf("relations = %+v, want only the valid person_01 -> person_02", relations)
	}
}

func TestProjectSemanticPersonRelationRejectsMissingIdentity(t *testing.T) {
	source := "Marco is father of Luca."
	_, err := projectSemanticPersonRelations(source, []semanticEngineIdentity{{ID: "person_01"}}, []semanticEnginePersonRelation{{
		FromID: "person_01", ToID: "person_02", Kind: "parent_of", Status: "asserted",
		StartByte: 0, EndByte: len(source), Evidence: source,
	}})
	if err == nil {
		t.Fatal("person relation with a missing identity passed")
	}
}
