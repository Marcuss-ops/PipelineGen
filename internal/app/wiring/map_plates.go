// Package wiring — map_plates.go is the composition-root adapter that bridges
// the operator's certified basemap manifest onto the overlay planner's
// PlateResolver port.
//
// It lives here, not in either capability, because it is the only place that
// knows BOTH sides: internal/capabilities/maps owns raster provenance and
// internal/capabilities/overlays owns the map overlay contract. Neither
// capability imports the other.
//
// The adapter is offline by construction. It reads a hand-authored manifest and
// the rasters that ship beside it; it never fetches a tile. The content address
// it hands the planner is the digest of the certified raster bytes, which the
// existing prefetch bridge stages from the local path with no network access.
package wiring

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/maps"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// mapPlateMediaType is the only raster type the manifest certifies, so the
// content address the planner publishes is always a PNG.
const mapPlateMediaType = "image/png"

// mapPlateResolver adapts the certified manifest onto the planner's port. A
// nil manifest resolves nothing, which is the fail-closed default: no map
// item is emitted rather than a fabricated one.
type mapPlateResolver struct {
	manifest *maps.Manifest
}

// ResolvePlate returns the most detailed certified plate covering the point.
// ok is false when no plate covers it, and the planner then emits no map.
func (r mapPlateResolver) ResolvePlate(latitude, longitude float64) (capabilityoverlay.MapPlate, bool) {
	if r.manifest == nil {
		return capabilityoverlay.MapPlate{}, false
	}
	plate, ok := r.manifest.Resolve(latitude, longitude)
	if !ok {
		return capabilityoverlay.MapPlate{}, false
	}
	return capabilityoverlay.MapPlate{
		ID:          plate.ID,
		License:     plate.License,
		Attribution: plate.Attribution,
		Center: capabilityoverlay.MapCenter{
			Latitude:  plate.Center.Latitude,
			Longitude: plate.Center.Longitude,
		},
		Zoom:   plate.Zoom,
		Width:  plate.Width,
		Height: plate.Height,
		Window: plate.Window(),
		// URL stays empty on purpose: the only way the raster reaches the
		// renderer is through the content-addressed prefetch of the local
		// plate, so no deployment can turn a map into a network fetch by
		// filling in a URL.
		Asset: capabilityoverlay.NewOverlayAssetRef(
			asset.New(plate.ID, plate.SHA256(), mapPlateMediaType, 0),
			"",
			plate.Path(),
		),
	}, true
}

// ResolveFlyover chooses complete offline raster coverage for the route. The
// coarsest certified window is the base plate; subsequent zooms become ordered
// LOD assets only when their provenance matches the base exactly.
func (r mapPlateResolver) ResolveFlyover(from, to capabilityoverlay.MapCenter, canvasWidth, canvasHeight int) (capabilityoverlay.MapPlate, bool) {
	if canvasWidth <= 0 || canvasHeight <= 0 {
		return capabilityoverlay.MapPlate{}, false
	}
	if r.manifest == nil {
		return capabilityoverlay.MapPlate{}, false
	}
	plates, ok := r.manifest.ResolveFlyover(from.Latitude, from.Longitude, to.Latitude, to.Longitude, canvasWidth, canvasHeight)
	if !ok || len(plates) < 2 {
		return capabilityoverlay.MapPlate{}, false
	}
	base := plates[0]
	resolved := mapPlateFromManifest(base)
	for _, plate := range plates[1:] {
		if plate.License != base.License || plate.Attribution != base.Attribution {
			return capabilityoverlay.MapPlate{}, false
		}
		lod := mapPlateFromManifest(plate)
		lod.LODs = nil
		resolved.LODs = append(resolved.LODs, lod)
	}
	return resolved, true
}

func mapPlateFromManifest(plate maps.Plate) capabilityoverlay.MapPlate {
	return capabilityoverlay.MapPlate{
		ID: plate.ID, License: plate.License, Attribution: plate.Attribution,
		Center: capabilityoverlay.MapCenter{Latitude: plate.Center.Latitude, Longitude: plate.Center.Longitude},
		Zoom:   plate.Zoom, Width: plate.Width, Height: plate.Height, Window: plate.Window(),
		Asset: capabilityoverlay.NewOverlayAssetRef(
			asset.New(plate.ID, plate.SHA256(), mapPlateMediaType, 0), "", plate.Path(),
		),
	}
}

// mapPlateWiringTarget is the port wireMapPlates needs: the runner's map-source
// setter and nothing else. Narrowing it keeps the wiring testable without a
// fully populated Runner.
type mapPlateWiringTarget interface {
	SetMapPlateResolver(capabilityoverlay.PlateResolver)
}

// wireMapPlates loads the operator basemap manifest when one is configured and
// wires it as the runner's ONLY map source. An unconfigured path is a valid
// deployment (no maps at all); a configured path that cannot be certified is a
// hard error, because silently running without the plates the operator
// declared would drop every map from the run without saying so.
func wireMapPlates(runner mapPlateWiringTarget, manifestPath string, log *zap.Logger) error {
	path := strings.TrimSpace(manifestPath)
	if path == "" {
		log.Info("map plates not wired: no operator basemap manifest configured (no map overlays will be emitted)")
		return nil
	}
	manifest, err := maps.LoadManifest(path)
	if err != nil {
		return fmt.Errorf("wire map plates: %w", err)
	}
	plates := manifest.Plates()
	runner.SetMapPlateResolver(mapPlateResolver{manifest: manifest})
	log.Info("operator basemap plates wired",
		zap.String("manifest", path),
		zap.Int("plates", len(plates)),
	)
	return nil
}
