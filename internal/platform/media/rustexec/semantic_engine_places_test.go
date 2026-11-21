package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectSemanticPlacesAndRoutesKeepsGroundedStops(t *testing.T) {
	source := "Maya drove from Paris to Rome."
	latParis, lonParis := 48.8566, 2.3522
	latRome, lonRome := 41.9028, 12.4964
	parisStart := strings.Index(source, "Paris")
	romeStart := strings.Index(source, "Rome")
	places, err := projectSemanticPlaces(source, []semanticEnginePlace{
		{ID: "pl_01", EntityLocalID: "loc_01", KBID: stringPtr("wd:Q90"), LinkStatus: "linked", Kind: "city", Name: "Paris", Lat: &latParis, Lon: &lonParis, Role: "origin", StartByte: parisStart, EndByte: parisStart + len("Paris"), Evidence: "Paris"},
		{ID: "pl_02", EntityLocalID: "loc_02", KBID: stringPtr("wd:Q220"), LinkStatus: "linked", Kind: "city", Name: "Rome", Lat: &latRome, Lon: &lonRome, Role: "destination", StartByte: romeStart, EndByte: romeStart + len("Rome"), Evidence: "Rome"},
	})
	if err != nil {
		t.Fatalf("projectSemanticPlaces: %v", err)
	}
	start := strings.Index(source, "drove")
	end := len(source) - 1
	routes, err := projectSemanticRoutes(source, places, []semanticEngineRoute{{
		ID: "rt_0001", Mode: "road", SubjectID: stringPtr("person_01"),
		Stops:  []semanticEngineRouteStop{{PlaceID: "pl_01", Role: "origin"}, {PlaceID: "pl_02", Role: "destination"}},
		Status: "asserted", StartByte: start, EndByte: end, Evidence: source[start:end],
	}})
	if err != nil {
		t.Fatalf("projectSemanticRoutes: %v", err)
	}
	if len(routes) != 1 || len(routes[0].Stops) != 2 || routes[0].Stops[1].PlaceID != "pl_02" {
		t.Fatalf("projected routes = %+v, want one ordered Paris-to-Rome route", routes)
	}
}

func TestProjectSemanticPlacesRejectsCoordinatesOnAmbiguousLink(t *testing.T) {
	source := "Paris"
	lat, lon := 48.8566, 2.3522
	_, err := projectSemanticPlaces(source, []semanticEnginePlace{{
		ID: "pl_01", EntityLocalID: "loc_01", LinkStatus: "ambiguous", Kind: "city", Name: source,
		Lat: &lat, Lon: &lon, Role: "mention", StartByte: 0, EndByte: len(source), Evidence: source,
	}})
	if err == nil {
		t.Fatal("ambiguous place with coordinates passed")
	}
}

func TestSemanticEngineAdapterCarriesGroundedPlacesAndEventsIntoGoBundle(t *testing.T) {
	var response map[string]any
	if err := json.Unmarshal(semanticFixtureWithScenes(t, 2), &response); err != nil {
		t.Fatal(err)
	}
	manifest := response["manifest"].(map[string]any)
	analyzers := manifest["analyzers"].(map[string]any)
	analyzers["places"] = "place_index.v1"
	analyzers["events"] = "event_frames.v2"
	analyzers["discourse"] = "discourse.v4"
	scenes := manifest["scenes"].([]any)
	scene := scenes[1].(map[string]any)
	start := strings.Index(semanticSceneTwo, "hearing")
	scene["places"] = []any{map[string]any{
		"id": "pl_01", "entity_local_id": "facility_01", "link_status": "unlinked",
		"kind": "unknown", "name": "hearing", "role": "setting", "start_byte": start,
		"end_byte": start + len("hearing"), "sentence_index": 0, "evidence": "hearing",
	}}
	triggerStart := strings.Index(semanticSceneOne, "founded")
	manifest["events"] = []any{map[string]any{
		"id": "ev_01", "event_type": "founding", "trigger": "founded",
		"trigger_start_byte": triggerStart, "trigger_end_byte": triggerStart + len("founded"),
		"participants": []any{map[string]any{"role": "agent", "entity_id": "person_01"}},
		"status":       "asserted", "scene_ids": []string{"scene_01"},
		"evidence":         []any{map[string]any{"scene_id": "scene_01", "start_byte": 0, "end_byte": len("Marco Rossi founded the studio."), "text": "Marco Rossi founded the studio."}},
		"confidence_basis": []string{"trigger", "participant"},
	}}
	fixture, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: fixture}))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatalf("SemanticBundles: %v", err)
	}
	bundle := bundles["scene_02"]
	if bundle.Analyzers.Discourse != "discourse.v4" {
		t.Fatalf("discourse analyzer version = %q, want discourse.v4", bundle.Analyzers.Discourse)
	}
	if len(bundle.Places) != 1 || bundle.Places[0].Name != "hearing" || bundle.Places[0].LinkStatus != "unlinked" {
		t.Fatalf("places were not carried into the Go bundle: %+v", bundle.Places)
	}
	if len(bundle.Events) != 1 || bundle.Events[0].EventType != "founding" || bundle.Events[0].Participants[0].EntityID != "person_01" {
		t.Fatalf("events were not carried into the Go bundle: %+v", bundle.Events)
	}
}

func TestProjectSemanticCrossSceneRouteRequiresGroundedConsecutiveHops(t *testing.T) {
	firstText := "Maya drove from Paris to Rome."
	secondText := "Maya drove from Rome to Berlin."
	firstStart := strings.Index(firstText, "drove")
	secondStart := strings.Index(secondText, "drove")
	firstEvidence := firstText[firstStart : len(firstText)-1]
	secondEvidence := secondText[secondStart : len(secondText)-1]
	subject := "person_01"
	firstRoute := semanticEngineRoute{ID: "rt_01", Mode: "road", SubjectID: &subject, Status: "asserted", StartByte: firstStart, EndByte: len(firstText) - 1, Evidence: firstEvidence, Stops: []semanticEngineRouteStop{{PlaceID: "p-paris", Role: "origin"}, {PlaceID: "p-rome", Role: "destination"}}}
	secondRoute := semanticEngineRoute{ID: "rt_01", Mode: "road", SubjectID: &subject, Status: "asserted", StartByte: secondStart, EndByte: len(secondText) - 1, Evidence: secondEvidence, Stops: []semanticEngineRouteStop{{PlaceID: "p-rome-2", Role: "origin"}, {PlaceID: "p-berlin", Role: "destination"}}}
	scenes := []semanticEngineSceneManifest{
		{SceneID: "leg-1", Routes: []semanticEngineRoute{firstRoute}, Places: []semanticEnginePlace{wirePlace("p-paris", "wd:Q90"), wirePlace("p-rome", "wd:Q220")}},
		{SceneID: "leg-2", Routes: []semanticEngineRoute{secondRoute}, Places: []semanticEnginePlace{wirePlace("p-rome-2", "wd:Q220"), wirePlace("p-berlin", "wd:Q64")}},
	}
	distance := 1183.0
	projected, err := projectSemanticCrossSceneRoutes(map[string]string{"leg-1": firstText, "leg-2": secondText}, scenes, []semanticEngineCrossSceneRoute{{
		ID: "cr_0001", SubjectID: subject, Mode: "road", Status: "asserted", StraightLineKM: &distance,
		Stops: []semanticEngineCrossSceneRouteStop{
			{SceneID: "leg-1", PlaceID: "p-paris", Role: "origin"},
			{SceneID: "leg-1", PlaceID: "p-rome", Role: "waypoint"},
			{SceneID: "leg-2", PlaceID: "p-berlin", Role: "destination"},
		},
		Evidence: []semanticEngineRouteEvidence{
			{SceneID: "leg-1", RouteID: "rt_01", StartByte: firstStart, EndByte: len(firstText) - 1, Evidence: firstEvidence},
			{SceneID: "leg-2", RouteID: "rt_01", StartByte: secondStart, EndByte: len(secondText) - 1, Evidence: secondEvidence},
		},
	}})
	if err != nil {
		t.Fatalf("projectSemanticCrossSceneRoutes: %v", err)
	}
	if len(projected) != 1 || len(projected[0].Stops) != 3 || len(projected[0].Evidence) != 2 {
		t.Fatalf("projected cross-scene route = %+v", projected)
	}
}

func TestProjectSemanticCrossSceneRouteRejectsSkippedScene(t *testing.T) {
	firstText := "Maya drove from Paris to Rome."
	secondText := "Maya drove from Rome to Berlin."
	firstStart := strings.Index(firstText, "drove")
	secondStart := strings.Index(secondText, "drove")
	subject := "person_01"
	firstEvidence := firstText[firstStart : len(firstText)-1]
	secondEvidence := secondText[secondStart : len(secondText)-1]
	route := semanticEngineCrossSceneRoute{
		ID: "cr_0001", SubjectID: subject, Mode: "road", Status: "asserted", StraightLineKM: floatPtr(1183),
		Stops: []semanticEngineCrossSceneRouteStop{{SceneID: "leg-1", PlaceID: "p1", Role: "origin"}, {SceneID: "leg-3", PlaceID: "p2", Role: "waypoint"}, {SceneID: "leg-3", PlaceID: "p3", Role: "destination"}},
		Evidence: []semanticEngineRouteEvidence{
			{SceneID: "leg-1", RouteID: "rt_01", StartByte: firstStart, EndByte: len(firstText) - 1, Evidence: firstEvidence},
			{SceneID: "leg-3", RouteID: "rt_01", StartByte: secondStart, EndByte: len(secondText) - 1, Evidence: secondEvidence},
		},
	}
	scenes := []semanticEngineSceneManifest{
		{SceneID: "leg-1", Routes: []semanticEngineRoute{{ID: "rt_01", SubjectID: &subject, Status: "asserted", StartByte: firstStart, EndByte: len(firstText) - 1, Evidence: firstEvidence, Mode: "road", Stops: []semanticEngineRouteStop{{PlaceID: "p1", Role: "origin"}, {PlaceID: "p2", Role: "destination"}}}}, Places: []semanticEnginePlace{wirePlace("p1", "wd:Q90"), wirePlace("p2", "wd:Q220")}},
		{SceneID: "leg-2"},
		{SceneID: "leg-3", Routes: []semanticEngineRoute{{ID: "rt_01", SubjectID: &subject, Status: "asserted", StartByte: secondStart, EndByte: len(secondText) - 1, Evidence: secondEvidence, Mode: "road", Stops: []semanticEngineRouteStop{{PlaceID: "p2", Role: "origin"}, {PlaceID: "p3", Role: "destination"}}}}, Places: []semanticEnginePlace{wirePlace("p2", "wd:Q220"), wirePlace("p3", "wd:Q64")}},
	}
	_, err := projectSemanticCrossSceneRoutes(map[string]string{"leg-1": firstText, "leg-3": secondText}, scenes, []semanticEngineCrossSceneRoute{route})
	if err == nil {
		t.Fatal("cross-scene route with a skipped scene passed")
	}
}

func stringPtr(value string) *string { return &value }

func floatPtr(value float64) *float64 { return &value }

func wirePlace(id, kbID string) semanticEnginePlace {
	return semanticEnginePlace{ID: id, LinkStatus: "linked", KBID: stringPtr(kbID)}
}
