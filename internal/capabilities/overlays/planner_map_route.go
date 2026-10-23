package overlays

import "sort"

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
func routeEndpoints(places []MapCandidate, fallback MapCenter) (MapCenter, MapCenter) {
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
	return MapCenter{Latitude: ordered[0].Latitude, Longitude: ordered[0].Longitude},
		MapCenter{Latitude: ordered[len(ordered)-1].Latitude, Longitude: ordered[len(ordered)-1].Longitude}
}

// plateFitsCanvas reports whether the plate's certified raster is exactly the
// canvas the plan renders. A mismatch means the plate's georeference cannot be
// honoured, so the plate is unusable — never scaled.
func plateFitsCanvas(plate MapPlate, canvasWidth, canvasHeight int) bool {
	if len(plate.LODs) == 0 {
		return plate.Width == canvasWidth && plate.Height == canvasHeight
	}
	return plate.Width >= canvasWidth && plate.Height >= canvasHeight &&
		plate.Width <= maxMapRasterWidth && plate.Height <= maxMapRasterHeight
}
