package overlays

import (
	"sort"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// mapFadeHalfBandZoom is the planner's half-width (in zoom stops) of every LOD
// cross-fade — the exact mirror of the worker's mapLODFadeHalfBandZoom. The
// viewport coverage below certifies each LOD over precisely the interval where
// the worker will render it visible.
const mapFadeHalfBandZoom = 0.5

// flyoverActiveZoomRange mirrors the worker's mapLODActiveZoomRange: the zoom
// interval where LOD[index] is visible over the move.
func flyoverActiveZoomRange(index int, zooms []int, startZoom, endZoom float64) (float64, float64) {
	var low, high float64
	if index == 0 {
		low = startZoom
	} else {
		low = (float64(zooms[index-1])+float64(zooms[index]))/2 - mapFadeHalfBandZoom
		if low < startZoom {
			low = startZoom
		}
	}
	if index == len(zooms)-1 {
		high = endZoom
	} else {
		high = (float64(zooms[index])+float64(zooms[index+1]))/2 + mapFadeHalfBandZoom
		if high > endZoom {
			high = endZoom
		}
	}
	return low, high
}

// routeEndpoints chooses the chronological first and last grounded locations;
// one grounded place intentionally becomes a stationary zoom-to, while a
// multi-place route follows spoken order. It never invents geography from
// spatial sorting or arithmetic averages.
func routeEndpoints(places []MapCandidate, fallback capmodel.MapCenter) (capmodel.MapCenter, capmodel.MapCenter) {
	if len(places) == 0 {
		return fallback, fallback
	}
	ordered := append([]MapCandidate(nil), places...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartUS != ordered[j].StartUS {
			return ordered[i].StartUS < ordered[j].StartUS
		}
		return ordered[i].EntityID < ordered[j].EntityID
	})
	return capmodel.MapCenter{Latitude: ordered[0].Latitude, Longitude: ordered[0].Longitude},
		capmodel.MapCenter{Latitude: ordered[len(ordered)-1].Latitude, Longitude: ordered[len(ordered)-1].Longitude}
}

// plateFitsCanvas reports whether the plate's certified raster is exactly the
// canvas the plan renders. A mismatch means the plate's georeference cannot be
// honoured, so the plate is unusable — never scaled.
func plateFitsCanvas(plate MapPlate, canvasWidth, canvasHeight int) bool {
	if len(plate.LODs) == 0 {
		return plate.Width == canvasWidth && plate.Height == canvasHeight
	}
	return plate.Width >= canvasWidth && plate.Height >= canvasHeight &&
		plate.Width <= capmodel.MaxMapRasterWidth && plate.Height <= capmodel.MaxMapRasterHeight
}

// certifyUnitRoute re-certifies the plate a chunked map will actually render.
//
// Narrative grouping may hand several places the plate of the first one, and a
// chunk's camera journey then starts and ends somewhere the resolved ladder was
// never certified for: the worker validates every LOD over the exact zoom
// interval where it is visible and voids the plan when the emitted route leaves
// the raster. The item's own endpoints are the only route the renderer will
// travel, so the flyover is re-resolved for them and the chunk keeps its pins
// only when the new ladder covers every one of them. When no certified journey
// exists the plate is returned unchanged and the ordinary coverage check decides.
func certifyUnitRoute(plate MapPlate, places []MapCandidate, resolver PlateResolver, canvasWidth, canvasHeight int) MapPlate {
	if len(plate.LODs) < 2 || resolver == nil || canvasWidth <= 0 || canvasHeight <= 0 || len(places) == 0 {
		return plate
	}
	flyovers, ok := resolver.(FlyoverPlateResolver)
	if !ok {
		return plate
	}
	from, to := routeEndpoints(places, plate.Center)
	certified, ok := flyovers.ResolveFlyover(from, to, canvasWidth, canvasHeight)
	if !ok || len(certified.LODs) == 0 || !plateTextMatches(plate, certified) || !plateCoversPlaces(certified, places) {
		return plate
	}
	return certified
}

// plateTextMatches keeps the provenance the plan already carries: a re-resolved
// ladder is only substitutable when it describes the same imagery licensing.
func plateTextMatches(current, candidate MapPlate) bool {
	return current.License == candidate.License && current.Attribution == candidate.Attribution
}

// plateCoversPlaces reports whether every pin of the chunk falls inside the
// certified base raster and each of its LODs.
func plateCoversPlaces(plate MapPlate, places []MapCandidate) bool {
	windows := make([]geodesy.Window, 0, len(plate.LODs)+1)
	windows = append(windows, plate.Window)
	for _, lod := range plate.LODs {
		windows = append(windows, geodesy.CenteredOn(lod.Center.Latitude, lod.Center.Longitude, lod.Zoom, lod.Width, lod.Height))
	}
	for _, place := range places {
		for _, window := range windows {
			if !window.Contains(place.Latitude, place.Longitude) {
				return false
			}
		}
	}
	return true
}
