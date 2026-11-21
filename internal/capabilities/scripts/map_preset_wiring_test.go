// Package scriptgeneration — map_preset_wiring_test.go pins the script-facing
// map preset alias: it expands to the certified composition + basemap pair,
// contradicts an explicit choice fail-closed, and rides the map item into the
// renderer payload.
package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestResolveMapPresentationPresetSpecExpandsAlias(t *testing.T) {
	spec := &scriptpkg.MapPresentationSpec{Preset: "italia_scansione_satellitare_pavia"}
	require.NoError(t, resolveMapPresentationPresetSpec(spec))
	require.Equal(t, "italia_scansione_satellitare_pavia", spec.Preset)
	require.Equal(t, "satellite_scan_reveal", spec.Animation)
	require.Equal(t, "esri_sat", spec.Style)

	// The expanded spec has to survive Normalize unchanged.
	resolved, err := spec.Normalize()
	require.NoError(t, err)
	require.Equal(t, "satellite_scan_reveal", resolved.Animation)
	require.Equal(t, "esri_sat", resolved.Style)
}

func TestResolveMapPresentationPresetSpecFailsClosed(t *testing.T) {
	for name, spec := range map[string]*scriptpkg.MapPresentationSpec{
		"unknown preset":       {Preset: "italia_mappa_inventata"},
		"animation conflict":   {Preset: "italia_editoriale_voxel", Animation: "territory_flood_fill"},
		"style conflict":       {Preset: "italia_editoriale_voxel", Style: "esri_sat"},
		"style alias conflict": {Preset: "italia_editoriale_voxel", Style: "satellite"},
		"pool excludes preset": {Preset: "italia_editoriale_voxel", AnimationPool: []string{"continental_atlas"}},
		"style pool excludes":  {Preset: "italia_editoriale_voxel", StylePool: []string{"esri_topo"}},
	} {
		require.Error(t, resolveMapPresentationPresetSpec(spec), name)
	}
	// Values that AGREE with the preset (also through a style alias) are fine.
	agreeing := &scriptpkg.MapPresentationSpec{
		Preset: "italia_editoriale_voxel", Animation: "editorial_vox", Style: "classic",
	}
	require.NoError(t, resolveMapPresentationPresetSpec(agreeing))
}

func TestExpandMapPresetPresentationWinsOnResolvedDefaults(t *testing.T) {
	// The resolved shape carries Normalize's defaults, not caller intent.
	presentation := &scriptpkg.MapPresentation{
		Preset: "italia_editoriale_voxel", Animation: "random", Style: "esri_topo",
	}
	require.NoError(t, expandMapPresetPresentation(presentation))
	require.Equal(t, "editorial_vox", presentation.Animation)
	require.Equal(t, "chronon_editorial", presentation.Style)
}

func TestApplyRequestedMapPresentationCarriesPreset(t *testing.T) {
	item := semanticMapItem(t, "map-1", "scene-1", "Firenze", 43.7697955, 11.2556404, 0)
	// A materialized plate carries its pin; the composition admission gate
	// (rightly) refuses a map with no place to draw.
	item.Map.Pins = []capmodel.MapOverlayPin{{
		ID: "place:firenze", Label: "Firenze", Latitude: 43.7697955, Longitude: 11.2556404, Scope: "city",
	}}
	plan := capmodel.OverlayPlan{Items: []capmodel.OverlayItem{item}}
	require.NoError(t, applyRequestedMapPresentation(&plan, &scriptpkg.MapPresentation{
		Preset: "italia_atlante_continentale_turchia", CountryFeature: "Italy",
	}))
	params := plan.Items[0].Params
	require.Equal(t, "italia_atlante_continentale_turchia", params["map_preset"])
	require.Equal(t, "continental_atlas", params["map_animation"])
	require.Equal(t, "esri_sat", params["map_type"])
}
