package rustexec

import (
	"fmt"
	"math"

	semanticscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
)

func projectSemanticPlaces(source string, places []semanticEnginePlace) ([]semanticscript.SemanticPlace, error) {
	out := make([]semanticscript.SemanticPlace, 0, len(places))
	seen := make(map[string]struct{}, len(places))
	for _, place := range places {
		if place.ID == "" || place.EntityLocalID == "" || place.Name == "" || place.Kind == "" ||
			place.Evidence == "" || !groundedSpan(source, place.StartByte, place.EndByte, place.Evidence) {
			return nil, fmt.Errorf("place %q is missing identity, kind, name, or grounded evidence", place.ID)
		}
		if _, exists := seen[place.ID]; exists {
			return nil, fmt.Errorf("duplicate place id %q", place.ID)
		}
		seen[place.ID] = struct{}{}
		if place.LinkStatus != "linked" && place.LinkStatus != "ambiguous" && place.LinkStatus != "unlinked" {
			return nil, fmt.Errorf("place %q has invalid link status %q", place.ID, place.LinkStatus)
		}
		if place.Role != "setting" && place.Role != "origin" && place.Role != "destination" &&
			place.Role != "waypoint" && place.Role != "mention" {
			return nil, fmt.Errorf("place %q has invalid role %q", place.ID, place.Role)
		}
		if (place.Lat == nil) != (place.Lon == nil) {
			return nil, fmt.Errorf("place %q has only one coordinate", place.ID)
		}
		if place.Lat != nil && (math.IsNaN(*place.Lat) || math.IsInf(*place.Lat, 0) ||
			math.IsNaN(*place.Lon) || math.IsInf(*place.Lon, 0) ||
			*place.Lat < -90 || *place.Lat > 90 || *place.Lon < -180 || *place.Lon > 180) {
			return nil, fmt.Errorf("place %q has out-of-range coordinates", place.ID)
		}
		if place.LinkStatus == "linked" {
			if place.KBID == nil || *place.KBID == "" || place.Lat == nil {
				return nil, fmt.Errorf("linked place %q lacks a KB id or coordinates", place.ID)
			}
		} else if place.KBID != nil || place.Lat != nil {
			return nil, fmt.Errorf("unresolved place %q carries a KB id or coordinates", place.ID)
		}
		kbID := ""
		if place.KBID != nil {
			kbID = *place.KBID
		}
		out = append(out, semanticscript.SemanticPlace{
			ID: place.ID, EntityLocalID: place.EntityLocalID, KBID: kbID,
			LinkStatus: place.LinkStatus, Kind: place.Kind, Name: place.Name,
			Lat: place.Lat, Lon: place.Lon, ParentIDs: append([]string(nil), place.ParentIDs...),
			Role: place.Role, Evidence: place.Evidence, SentenceIndex: place.SentenceIndex,
			Start: place.StartByte, End: place.EndByte,
		})
	}
	return out, nil
}

func projectSemanticRoutes(source string, places []semanticscript.SemanticPlace, routes []semanticEngineRoute) ([]semanticscript.SemanticRoute, error) {
	placeByID := make(map[string]semanticscript.SemanticPlace, len(places))
	for _, place := range places {
		placeByID[place.ID] = place
	}
	out := make([]semanticscript.SemanticRoute, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.ID == "" || route.Evidence == "" || !groundedSpan(source, route.StartByte, route.EndByte, route.Evidence) {
			return nil, fmt.Errorf("route %q has ungrounded evidence", route.ID)
		}
		if _, exists := seen[route.ID]; exists {
			return nil, fmt.Errorf("duplicate route id %q", route.ID)
		}
		seen[route.ID] = struct{}{}
		if route.Status != "asserted" && route.Status != "negated" && route.Status != "uncertain" {
			return nil, fmt.Errorf("route %q has invalid status %q", route.ID, route.Status)
		}
		if route.Mode != "road" && route.Mode != "rail" && route.Mode != "air" &&
			route.Mode != "sea" && route.Mode != "walk" && route.Mode != "unknown" {
			return nil, fmt.Errorf("route %q has invalid mode %q", route.ID, route.Mode)
		}
		if len(route.Stops) < 2 || route.Stops[0].Role != "origin" || route.Stops[len(route.Stops)-1].Role != "destination" {
			return nil, fmt.Errorf("route %q must have grounded origin and destination stops", route.ID)
		}
		stops := make([]semanticscript.SemanticRouteStop, 0, len(route.Stops))
		lastStart := -1
		for index, stop := range route.Stops {
			place, ok := placeByID[stop.PlaceID]
			if !ok {
				return nil, fmt.Errorf("route %q references missing place %q", route.ID, stop.PlaceID)
			}
			wantRole := "waypoint"
			if index == 0 {
				wantRole = "origin"
			} else if index == len(route.Stops)-1 {
				wantRole = "destination"
			}
			if stop.Role != wantRole || place.Start < lastStart {
				return nil, fmt.Errorf("route %q has out-of-order or invalid stop role", route.ID)
			}
			lastStart = place.Start
			stops = append(stops, semanticscript.SemanticRouteStop{PlaceID: stop.PlaceID, Role: stop.Role})
		}
		if route.StraightLineKM != nil && (*route.StraightLineKM < 0 || math.IsNaN(*route.StraightLineKM) || math.IsInf(*route.StraightLineKM, 0)) {
			return nil, fmt.Errorf("route %q has invalid straight-line distance", route.ID)
		}
		if route.StraightLineKM != nil && route.Status != "asserted" {
			return nil, fmt.Errorf("non-asserted route %q carries a distance", route.ID)
		}
		firstPlace := placeByID[route.Stops[0].PlaceID]
		lastPlace := placeByID[route.Stops[len(route.Stops)-1].PlaceID]
		if route.StraightLineKM != nil && (firstPlace.LinkStatus != "linked" || lastPlace.LinkStatus != "linked") {
			return nil, fmt.Errorf("route %q carries a distance without linked endpoints", route.ID)
		}
		subject := ""
		if route.SubjectID != nil {
			subject = *route.SubjectID
		}
		out = append(out, semanticscript.SemanticRoute{
			ID: route.ID, Mode: route.Mode, SubjectID: subject, Status: route.Status,
			Stops: stops, StraightLineKM: route.StraightLineKM, Evidence: route.Evidence,
			Start: route.StartByte, End: route.EndByte,
		})
	}
	return out, nil
}

func projectSemanticCrossSceneRoutes(
	texts map[string]string,
	scenes []semanticEngineSceneManifest,
	routes []semanticEngineCrossSceneRoute,
) ([]semanticscript.SemanticCrossSceneRoute, error) {
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
				return nil, fmt.Errorf("cross-scene route %q has ungrounded or mismatched hop evidence", route.ID)
			}
			if lastIndex >= 0 && index != lastIndex+1 {
				return nil, fmt.Errorf("cross-scene route %q skips a scene between hops", route.ID)
			}
			if len(localRoute.Stops) < 2 {
				return nil, fmt.Errorf("cross-scene route %q references a hop with too few stops", route.ID)
			}
			origin, originOK := placeByID[evidence.SceneID][localRoute.Stops[0].PlaceID]
			destination, destinationOK := placeByID[evidence.SceneID][localRoute.Stops[len(localRoute.Stops)-1].PlaceID]
			if !originOK || !destinationOK || origin.LinkStatus != "linked" || destination.LinkStatus != "linked" ||
				origin.KBID == nil || destination.KBID == nil {
				return nil, fmt.Errorf("cross-scene route %q hop endpoints are not linked places", route.ID)
			}
			if hopIndex > 0 && previousDestinationKBID != *origin.KBID {
				return nil, fmt.Errorf("cross-scene route %q hops do not share a linked endpoint", route.ID)
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
		if route.Mode != expectedMode || len(route.Stops) != len(expectedStops) {
			return nil, fmt.Errorf("cross-scene route %q disagrees with its scene-local hops", route.ID)
		}
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
