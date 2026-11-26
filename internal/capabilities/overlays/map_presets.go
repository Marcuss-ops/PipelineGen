// Package overlays — map_presets.go is the Go projection of the stable
// script-facing map presets of ChrononPlugin
// (ChrononPlugin/plugins/maps/map-presets-catalog.v1.json).
//
// A preset is an ALIAS, not a second renderer vocabulary: it fixes one
// composition + basemap pair (plus the default place the renderer assumes when
// the payload names none) so a script can ask for one named look without
// knowing the two independent runtime fields. The composition and basemap it
// expands to are still validated by the same admission gates as an explicit
// animation/style request.
//
// The list is duplicated here on purpose (the Go kernel cannot read a JSON
// file of a sibling repository at runtime), and map_presets_test.go parses the
// catalog and fails if the two ever diverge — the same parity rule the
// renderer catalog uses.
package overlays

import "strings"

// MapPreset is one script-facing map treatment: a stable id, a human title and
// the runtime presentation it resolves to.
type MapPreset struct {
	ID           string
	Title        string
	Animation    string
	Basemap      string
	DefaultPlace string
}

// mapPresets mirrors map-presets-catalog.v1.json entry for entry, in catalog
// order. Keep the two in lockstep; the parity test guards the table.
var mapPresets = []MapPreset{
	{ID: "italia_atlante_continentale_turchia", Title: "Atlante continentale — Turchia", Animation: "continental_atlas", Basemap: "esri_sat", DefaultPlace: "Italy"},
	{ID: "italia_mappa_storica_duisburg", Title: "Mappa storica — Duisburg", Animation: "historic_location_timeline", Basemap: "esri_sat", DefaultPlace: "Italy"},
	{ID: "italia_scansione_satellitare_pavia", Title: "Scansione satellitare — Pavia", Animation: "satellite_scan_reveal", Basemap: "esri_sat", DefaultPlace: "Italy"},
	{ID: "italia_riempimento_tattico_gerusalemme", Title: "Riempimento tattico — Gerusalemme", Animation: "territory_tactical_fill", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
	{ID: "italia_editoriale_voxel", Title: "Mappa editoriale a voxel", Animation: "editorial_vox", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
	{ID: "italia_editoriale_paper_cutout", Title: "Mappa editoriale paper cutout", Animation: "editorial_paper_cutout", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
	{ID: "italia_editoriale_radar_scuro", Title: "Mappa editoriale radar scuro", Animation: "editorial_dark_radar", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
	{ID: "italia_editoriale_connessioni_ad_arco", Title: "Mappa editoriale connessioni ad arco", Animation: "editorial_connection_arc", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
	{ID: "italia_editoriale_isola_fluttuante", Title: "Mappa editoriale isola fluttuante", Animation: "editorial_floating_island", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
	{ID: "italia_basemap_vettoriale_regionale", Title: "Basemap vettoriale regionale", Animation: "territory_flood_fill", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
	{ID: "italia_basemap_chiara", Title: "Basemap chiara", Animation: "territory_glow_reveal", Basemap: "esri_topo", DefaultPlace: "Italy"},
	{ID: "italia_basemap_regionale_toscana", Title: "Basemap regionale — Toscana", Animation: "territory_extrusion", Basemap: "chronon_editorial", DefaultPlace: "Italy"},
}

// MapPresets returns a copy of the certified preset list in catalog order.
func MapPresets() []MapPreset {
	return append([]MapPreset(nil), mapPresets...)
}

// LookupMapPreset resolves one preset id. Matching is exact after trimming:
// a preset id is a stable wire value, never a fuzzy alias.
func LookupMapPreset(id string) (MapPreset, bool) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return MapPreset{}, false
	}
	for _, preset := range mapPresets {
		if preset.ID == trimmed {
			return preset, true
		}
	}
	return MapPreset{}, false
}
