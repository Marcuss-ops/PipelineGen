package rustexec

import (
	"fmt"
	"math"

	semanticscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
)

// projectSemanticCrossSceneRoutes validates the cross-scene route projection
// against the scene-local routes and places it composes. Every hop must match a
// grounded local route, share the linked endpoint with the previous hop, and
// describe the same ordered stop list the wire declares.
func projectSemanticCrossSceneRoutes(
	texts map[string]string,
	scenes []semanticEngineSceneManifest,
	routes []semanticEngineCrossSceneRoute,
) ([]semanticscript.SemanticCrossSceneRoute, error) {
	sceneIndex, placeByID, routeByID := indexCrossSceneRouteScenes(scenes)
	out := make([]semanticscript.SemanticCrossSceneRoute, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.ID == "" || route.SubjectID == "" || route.Status != "asserted" || len(route.Evidence) < 2 || len(route.Stops) < 3 {
			return nil, fmt.Errorf("cross-scene route %q is incomplete", route.ID)
		}
		if _, exists := seen[route.ID]; exists {
			return nil, fmt.Errorf("duplicate cross-scene route id %q", route.ID)
		}
		seen[route.ID] = struct{}{}
		expectedStops, projectedEvidence, expectedMode, err := validateCrossSceneRoute(route, texts, sceneIndex, placeByID, routeByID)
		if err != nil {
			return nil, err
		}
		if route.Mode != expectedMode || len(route.Stops) != len(expectedStops) {
			return nil, fmt.Errorf("cross-scene route %q disagrees with its scene-local hops", route.ID)
		}
		projectedStops, err := projectCrossSceneRouteStops(route, placeByID, expectedStops)
		if err != nil {
			return nil, err
		}
		if route.StraightLineKM == nil || *route.StraightLineKM < 0 || math.IsNaN(*route.StraightLineKM) || math.IsInf(*route.StraightLineKM, 0) {
			return nil, fmt.Errorf("cross-scene route %q has invalid distance", route.ID)
		}
		out = append(out, semanticscript.SemanticCrossSceneRoute{
			ID: route.ID, SubjectID: route.SubjectID, Mode: route.Mode, Status: route.Status,
			Stops: projectedStops, Evidence: projectedEvidence, StraightLineKM: route.StraightLineKM,
		})
	}
	return out, nil
}

func indexCrossSceneRouteScenes(scenes []semanticEngineSceneManifest) (map[string]int, map[string]map[string]semanticEnginePlace, map[string]map[string]semanticEngineRoute) {
	sceneIndex := make(map[string]int, len(scenes))
	placeByID := make(map[string]map[string]semanticEnginePlace, len(scenes))
	routeByID := make(map[string]map[string]semanticEngineRoute, len(scenes))
	for index, scene := range scenes {
		sceneIndex[scene.SceneID] = index
		placeByID[scene.SceneID] = make(map[string]semanticEnginePlace, len(scene.Places))
		for _, place := range scene.Places {
			placeByID[scene.SceneID][place.ID] = place
		}
		routeByID[scene.SceneID] = make(map[string]semanticEngineRoute, len(scene.Routes))
		for _, route := range scene.Routes {
			routeByID[scene.SceneID][route.ID] = route
		}
	}
	return sceneIndex, placeByID, routeByID
}

func validateCrossSceneRoute(
	route semanticEngineCrossSceneRoute,
	texts map[string]string,
	sceneIndex map[string]int,
	placeByID map[string]map[string]semanticEnginePlace,
	routeByID map[string]map[string]semanticEngineRoute,
) ([]semanticEngineCrossSceneRouteStop, []semanticscript.SemanticRouteEvidence, string, error) {
	projectedEvidence := make([]semanticscript.SemanticRouteEvidence, 0, len(route.Evidence))
	expectedStops := make([]semanticEngineCrossSceneRouteStop, 0, len(route.Stops))
	lastIndex := -1
	previousDestinationKBID := ""
	expectedMode := ""
	for hopIndex, evidence := range route.Evidence {
		text, ok := texts[evidence.SceneID]
		localRoute, routeOK := routeByID[evidence.SceneID][evidence.RouteID]
		index, indexOK := sceneIndex[evidence.SceneID]
		if !ok || !routeOK || !indexOK || index <= lastIndex || localRoute.Status != "asserted" ||
			localRoute.SubjectID == nil || *localRoute.SubjectID != route.SubjectID ||
			localRoute.StartByte != evidence.StartByte || localRoute.EndByte != evidence.EndByte ||
			localRoute.Evidence != evidence.Evidence ||
			!groundedSpan(text, evidence.StartByte, evidence.EndByte, evidence.Evidence) {
			return nil, nil, "", fmt.Errorf("cross-scene route %q has ungrounded or mismatched hop evidence", route.ID)
		}
		if lastIndex >= 0 && index != lastIndex+1 {
			return nil, nil, "", fmt.Errorf("cross-scene route %q skips a scene between hops", route.ID)
		}
		if len(localRoute.Stops) < 2 {
			return nil, nil, "", fmt.Errorf("cross-scene route %q references a hop with too few stops", route.ID)
		}
		origin, originOK := placeByID[evidence.SceneID][localRoute.Stops[0].PlaceID]
		destination, destinationOK := placeByID[evidence.SceneID][localRoute.Stops[len(localRoute.Stops)-1].PlaceID]
		if !originOK || !destinationOK || origin.LinkStatus != "linked" || destination.LinkStatus != "linked" ||
			origin.KBID == nil || destination.KBID == nil {
			return nil, nil, "", fmt.Errorf("cross-scene route %q hop endpoints are not linked places", route.ID)
		}
		if hopIndex > 0 && previousDestinationKBID != *origin.KBID {
			return nil, nil, "", fmt.Errorf("cross-scene route %q hops do not share a linked endpoint", route.ID)
		}
		previousDestinationKBID = *destination.KBID
		if expectedMode == "" {
			expectedMode = localRoute.Mode
		} else if expectedMode != localRoute.Mode {
			expectedMode = "unknown"
		}
		for stopIndex, localStop := range localRoute.Stops {
			if hopIndex > 0 && stopIndex == 0 {
				continue
			}
			role := localStop.Role
			if stopIndex == len(localRoute.Stops)-1 && hopIndex+1 < len(route.Evidence) {
				role = "waypoint"
			}
			expectedStops = append(expectedStops, semanticEngineCrossSceneRouteStop{
				SceneID: evidence.SceneID, PlaceID: localStop.PlaceID, Role: role,
			})
		}
		lastIndex = index
		projectedEvidence = append(projectedEvidence, semanticscript.SemanticRouteEvidence{
			SceneID: evidence.SceneID, RouteID: evidence.RouteID, Evidence: evidence.Evidence,
			Start: evidence.StartByte, End: evidence.EndByte,
		})
	}
	return expectedStops, projectedEvidence, expectedMode, nil
}

func projectCrossSceneRouteStops(
	route semanticEngineCrossSceneRoute,
	placeByID map[string]map[string]semanticEnginePlace,
	expectedStops []semanticEngineCrossSceneRouteStop,
) ([]semanticscript.SemanticCrossSceneRouteStop, error) {
	projectedStops := make([]semanticscript.SemanticCrossSceneRouteStop, 0, len(route.Stops))
	for index, stop := range route.Stops {
		if _, ok := placeByID[stop.SceneID][stop.PlaceID]; !ok {
			return nil, fmt.Errorf("cross-scene route %q references missing place %s/%s", route.ID, stop.SceneID, stop.PlaceID)
		}
		wantRole := "waypoint"
		if index == 0 {
			wantRole = "origin"
		} else if index == len(route.Stops)-1 {
			wantRole = "destination"
		}
		if stop.Role != wantRole || stop != expectedStops[index] {
			return nil, fmt.Errorf("cross-scene route %q has invalid stop role %q", route.ID, stop.Role)
		}
		projectedStops = append(projectedStops, semanticscript.SemanticCrossSceneRouteStop{
			SceneID: stop.SceneID, PlaceID: stop.PlaceID, Role: stop.Role,
		})
	}
	return projectedStops, nil
}
