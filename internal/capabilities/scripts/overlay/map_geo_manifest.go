package overlay

// Single geographic manifest source for editorial map scenes.
// accepted_pin_bounds filters admissibility (Go resolver); camera_bounds drives
// framing/camera (Python renderer). Both projections MUST be generated from
// this manifest — never hand-edited in two places — or a new scene diverges.
// Python: ChrononTemplate/tools/map/map_geo_manifest.json (generated).
type MapGeoEntry struct {
	AcceptedPinBounds [4]float64 `json:"accepted_pin_bounds"`
	CameraBounds      [4]float64 `json:"camera_bounds"`
}

var MapGeoManifest = map[string]MapGeoEntry{
	"ed_saudi_base":     {AcceptedPinBounds: [4]float64{16.0, 34.0, 33.0, 56.0}, CameraBounds: [4]float64{16.0, 34.0, 33.0, 56.0}},
	"ed_iran_glow":      {AcceptedPinBounds: [4]float64{23.5, 46.0, 32.5, 57.0}, CameraBounds: [4]float64{23.5, 46.0, 32.5, 57.0}},
	"ed_yemen_fill":     {AcceptedPinBounds: [4]float64{11.0, 42.0, 20.0, 53.0}, CameraBounds: [4]float64{11.0, 42.0, 20.0, 53.0}},
	"ed_yemen_markers":  {AcceptedPinBounds: [4]float64{12.2, 40.5, 19.8, 47.0}, CameraBounds: [4]float64{12.2, 40.5, 19.8, 47.0}},
	"ed_hormuz_routes":  {AcceptedPinBounds: [4]float64{5.0, 37.0, 29.0, 75.0}, CameraBounds: [4]float64{5.0, 37.0, 29.0, 75.0}},
	"ed_mocha_tactical": {AcceptedPinBounds: [4]float64{12.4, 42.0, 15.8, 45.2}, CameraBounds: [4]float64{12.4, 42.0, 15.8, 45.2}},
}
