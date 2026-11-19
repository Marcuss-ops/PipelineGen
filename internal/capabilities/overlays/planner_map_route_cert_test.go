package overlays

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// routeStub is a certified-plate catalog that can answer both the static and
// the flyover question. ResolvePlate always returns a camera plate centred on
// the requested place, which is exactly the shape that used to leak into a
// chunked item: three places share the first place's ladder, but the camera
// travels from the first to the last of them.
type routeStub struct {
	flyoverOK   bool
	flyovers    [][4]float64
	plateCalls  int
	flyoverCall int
}

func stubCameraPlate(center capmodel.MapCenter) MapPlate {
	plate := MapPlate{
		ID: "stub-plate", License: "stub-license", Attribution: "Stub Imagery",
		Center: center, Zoom: 6, Width: 5760, Height: 3240,
		Window: geodesy.CenteredOn(center.Latitude, center.Longitude, 6, 5760, 3240),
	}
	for _, zoom := range []int{7, 8} {
		plate.LODs = append(plate.LODs, MapPlate{
			ID: "stub-plate", License: "stub-license", Attribution: "Stub Imagery",
			Center: center, Zoom: zoom, Width: 5760, Height: 3240,
			Window: geodesy.CenteredOn(center.Latitude, center.Longitude, zoom, 5760, 3240),
		})
	}
	return plate
}

func (s *routeStub) ResolvePlate(latitude, longitude float64) (MapPlate, bool) {
	s.plateCalls++
	return stubCameraPlate(capmodel.MapCenter{Latitude: latitude, Longitude: longitude}), true
}

func (s *routeStub) ResolveFlyover(from, to capmodel.MapCenter, _, _ int) (MapPlate, bool) {
	s.flyoverCall++
	s.flyovers = append(s.flyovers, [4]float64{from.Latitude, from.Longitude, to.Latitude, to.Longitude})
	if !s.flyoverOK {
		return MapPlate{}, false
	}
	return stubCameraPlate(capmodel.MapCenter{
		Latitude:  (from.Latitude + to.Latitude) / 2,
		Longitude: (from.Longitude + to.Longitude) / 2,
	}), true
}

// A narrative group hands every place the first place's plate. The emitted
// camera move then runs Roma -> Parigi over a ladder certified for Roma only,
// which the worker's LOD coverage seal rejects and voids the whole plan. The
// item must be re-certified for the route it actually renders.
func TestMapItemsForSceneRecertifiesTheEmittedRoute(t *testing.T) {
	roma := testMapCandidate(t, "roma", "Roma", 41.8933, 12.4829, 50_000, 3_000_000)
	torino := testMapCandidate(t, "torino", "Torino", 45.0678, 7.6825, 3_100_000, 3_000_000)
	parigi := testMapCandidate(t, "parigi", "Parigi", 48.8535, 2.3484, 6_200_000, 3_000_000)
	shared := stubCameraPlate(capmodel.MapCenter{Latitude: roma.Latitude, Longitude: roma.Longitude})
	plans := []MapPlan{
		{Plate: shared, Place: roma},
		{Plate: shared, Place: torino},
		{Plate: shared, Place: parigi},
	}
	stub := &routeStub{flyoverOK: true}
	items := mapItemsForScene("static__roma", plans, stub, 1920, 1080, 0, 0, "seed", MapGroupConfig{})
	if len(items) != 1 {
		t.Fatalf("items = %d, want one grouped map", len(items))
	}
	item := items[0]
	if item.Map == nil || item.Map.CameraMove == nil {
		t.Fatalf("grouped camera map expected, got %+v", item.Map)
	}
	if stub.flyoverCall == 0 {
		t.Fatal("the emitted route was never re-certified")
	}
	move := item.Map.CameraMove
	if move.To.Latitude != parigi.Latitude || move.To.Longitude != parigi.Longitude {
		t.Fatalf("camera destination = %+v, want Parigi", move.To)
	}
	wantCenter := (roma.Latitude + parigi.Latitude) / 2
	if diff := item.Map.Center.Latitude - wantCenter; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("map centre = %.4f, want the certified route midpoint %.4f", item.Map.Center.Latitude, wantCenter)
	}
	if len(item.Map.LODs) != 3 {
		t.Fatalf("LODs = %d, want the certified three-step ladder", len(item.Map.LODs))
	}
	for _, pin := range item.Map.Pins {
		_ = pin
	}
	if len(item.Map.Pins) != 3 {
		t.Fatalf("pins = %d, want the three grouped places", len(item.Map.Pins))
	}
}

// When no certified journey exists for the route, the plate is left untouched:
// the ordinary coverage contract then decides instead of a fabricated ladder.
func TestMapItemsForSceneKeepsThePlateWhenRecertificationFails(t *testing.T) {
	roma := testMapCandidate(t, "roma", "Roma", 41.8933, 12.4829, 50_000, 3_000_000)
	parigi := testMapCandidate(t, "parigi", "Parigi", 48.8535, 2.3484, 3_100_000, 3_000_000)
	shared := stubCameraPlate(capmodel.MapCenter{Latitude: roma.Latitude, Longitude: roma.Longitude})
	plans := []MapPlan{{Plate: shared, Place: roma}, {Plate: shared, Place: parigi}}
	stub := &routeStub{flyoverOK: false}
	items := mapItemsForScene("static__roma", plans, stub, 1920, 1080, 0, 0, "seed", MapGroupConfig{})
	if len(items) != 1 {
		t.Fatalf("items = %d, want one map", len(items))
	}
	if items[0].Map.Center.Latitude != roma.Latitude {
		t.Fatalf("map centre = %.4f, want the resolved plate centre %.4f", items[0].Map.Center.Latitude, roma.Latitude)
	}
}
