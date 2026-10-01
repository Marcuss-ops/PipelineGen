// Package maps owns the operator-supplied basemap manifest: the ONLY source of
// raster basemaps the pipeline accepts.
//
// A plate is a locally licensed raster plus its exact Web Mercator georeference.
// The manifest is hand-authored by the operator and read offline — no tile
// fetcher, no public basemap service and no network access are involved at plan
// or render time. Loading is fail-closed: a plate whose raster is missing, is
// not a PNG, or whose declared window disagrees with the raster that shipped is
// an error, never a silently fabricated map. The georeference uses the same
// projection as map_cartography.py, ChrononMotion3D's MapWindow, RenderingGen's
// internal/geo and kernel/geodesy, so a pin lands on the pixel the plate painted.
package maps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// ManifestVersion is the only manifest schema this package reads.
const ManifestVersion = 1

// Bounds mirrored from the published overlay-plan.v1 map contract, so a plate
// the manifest accepts can always be lowered into a valid map declaration.
const (
	maxPlateWidth  = 7680
	maxPlateHeight = 4320
	maxPlateZoom   = 22
)

// pngSignature is the 8-byte magic every PNG stream begins with (RFC 2083).
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// Point is one WGS84 coordinate in degrees.
type Point struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Plate is one operator-supplied, georeferenced raster basemap. The exported
// fields are the manifest's authored provenance; the resolved path, digest and
// window are filled in only by LoadManifest, after the raster is certified.
type Plate struct {
	// ID is the operator's stable plate identity and the deterministic
	// tie-breaker when two plates cover the same point.
	ID string `json:"id"`
	// File is the raster path relative to the manifest's own directory.
	File string `json:"file"`
	// License is the operator's provenance string for the raster.
	License string `json:"license"`
	// Attribution is the provider-required visible credit. The overlay map
	// contract re-validates it at plan time (single owner of the strict
	// single-line rule); the manifest only requires it to be present.
	Attribution string `json:"attribution"`
	// Center is the WGS84 point the raster is centred on.
	Center Point `json:"center"`
	// Zoom is the slippy-tile zoom the raster was stitched at.
	Zoom   int `json:"zoom"`
	Width  int `json:"width"`
	Height int `json:"height"`

	path   string
	sha256 string
	window geodesy.Window
}

// Path is the resolved absolute path of the certified raster.
func (p Plate) Path() string { return p.path }

// SHA256 is the content address of the certified raster bytes.
func (p Plate) SHA256() string { return p.sha256 }

// Window is the certified georeference of the raster.
func (p Plate) Window() geodesy.Window { return p.window }

// Manifest is the validated, certified set of operator plates.
type Manifest struct {
	plates []Plate
}

// manifestFile / manifestPlate are the strict wire shapes of the on-disk
// manifest. Decoding is strict so a typo in an operator's manifest fails loudly
// instead of silently dropping a plate or a provenance field.
type manifestFile struct {
	Version int             `json:"version"`
	Plates  []manifestPlate `json:"plates"`
}

type manifestPlate struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	License     string `json:"license"`
	Attribution string `json:"attribution"`
	Center      Point  `json:"center"`
	Zoom        int    `json:"zoom"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

// LoadManifest reads, validates and certifies the manifest at path. Plate files
// are resolved relative to the manifest's own directory, so a deployment can
// move the whole maps directory without rewriting the manifest.
func LoadManifest(path string) (*Manifest, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("map manifest: resolve %q: %w", path, err)
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("map manifest: read %q: %w", absPath, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file manifestFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("map manifest %q: decode: %w", absPath, err)
	}
	if file.Version != ManifestVersion {
		return nil, fmt.Errorf("map manifest %q: unsupported version %d (want %d)", absPath, file.Version, ManifestVersion)
	}
	root := filepath.Dir(absPath)
	manifest := &Manifest{plates: make([]Plate, 0, len(file.Plates))}
	seen := make(map[string]struct{}, len(file.Plates))
	for index, entry := range file.Plates {
		plate, err := certifyPlate(root, entry)
		if err != nil {
			return nil, fmt.Errorf("map manifest %q plate[%d]: %w", absPath, index, err)
		}
		if _, exists := seen[plate.ID]; exists {
			return nil, fmt.Errorf("map manifest %q: duplicate plate id %q", absPath, plate.ID)
		}
		seen[plate.ID] = struct{}{}
		manifest.plates = append(manifest.plates, plate)
	}
	// Deterministic order: ID ascending. With the Resolve tie-break this makes
	// plate selection reproducible across runs and machines.
	sort.Slice(manifest.plates, func(i, j int) bool { return manifest.plates[i].ID < manifest.plates[j].ID })
	return manifest, nil
}

// Plates returns a copy of the certified plates in deterministic (id) order.
func (m *Manifest) Plates() []Plate {
	if m == nil {
		return nil
	}
	return append([]Plate(nil), m.plates...)
}

// Resolve returns the certified plate whose window contains the point. The
// highest zoom wins (the most detailed raster of the same geography); ties break
// on the smaller raster area, then on the lexicographically smaller plate id, so
// selection is deterministic. ok is false when no certified plate covers the
// point — the caller then produces NO map rather than a fabricated one.
func (m *Manifest) Resolve(latitude, longitude float64) (Plate, bool) {
	if m == nil {
		return Plate{}, false
	}
	best := -1
	for i := range m.plates {
		if !m.plates[i].window.Contains(latitude, longitude) {
			continue
		}
		if best < 0 || moreDetailed(m.plates[i], m.plates[best]) {
			best = i
		}
	}
	if best < 0 {
		return Plate{}, false
	}
	return m.plates[best], true
}

// ResolveFlyover selects a deterministic ordered set of locally certified
// plates whose matching zoom coverage contains the complete straight projected
// route plus camera viewport margin. It returns false unless >=2 increasing
// zoom levels cover the entire route, so callers may safely fall back to the
// legacy static single-plate map.
func (m *Manifest) ResolveFlyover(fromLat, fromLon, toLat, toLon float64, canvasWidth, canvasHeight int) ([]Plate, bool) {
	if m == nil || canvasWidth <= 0 || canvasHeight <= 0 || len(m.plates) < 2 {
		return nil, false
	}
	from := geodesy.Point{Latitude: fromLat, Longitude: fromLon}
	to := geodesy.Point{Latitude: toLat, Longitude: toLon}
	if from.Validate() != nil || to.Validate() != nil {
		return nil, false
	}
	byZoom := make(map[int][]Plate)
	zooms := make([]int, 0)
	for _, plate := range m.plates {
		if _, exists := byZoom[plate.Zoom]; !exists {
			zooms = append(zooms, plate.Zoom)
		}
		byZoom[plate.Zoom] = append(byZoom[plate.Zoom], plate)
	}
	sort.Ints(zooms)
	selected := make([]Plate, 0, len(zooms))
	for _, zoom := range zooms {
		candidates := append([]Plate(nil), byZoom[zoom]...)
		sort.Slice(candidates, func(i, j int) bool { return moreDetailed(candidates[i], candidates[j]) })
		for _, candidate := range candidates {
			if !windowCoversRouteAtZoom(candidate.window, from, to, zoom, canvasWidth, canvasHeight) {
				continue
			}
			selected = append(selected, candidate)
			break
		}
	}
	if len(selected) < 2 {
		return nil, false
	}
	// Keep two or more distinct zoom levels, but avoid unbounded asset fanout.
	// Deterministically retain the coarsest and the most detailed certified
	// levels and evenly sample any intermediate levels.
	if len(selected) > 8 {
		bounded := make([]Plate, 0, 8)
		for index := 0; index < 8; index++ {
			at := index * (len(selected) - 1) / 7
			bounded = append(bounded, selected[at])
		}
		selected = bounded
	}
	return selected, true
}

func windowCoversRouteAtZoom(window geodesy.Window, from, to geodesy.Point, targetZoom, canvasWidth, canvasHeight int) bool {
	if canvasWidth <= 0 || canvasHeight <= 0 {
		return false
	}
	fx, fy := geodesy.LatLonToGlobalPixel(from.Latitude, from.Longitude, window.Zoom)
	tx, ty := geodesy.LatLonToGlobalPixel(to.Latitude, to.Longitude, window.Zoom)
	deltaX := tx - fx
	worldSize := geodesy.MercatorTileSize * math.Pow(2, float64(window.Zoom))
	if deltaX > worldSize/2 { deltaX -= worldSize }
	if deltaX < -worldSize/2 { deltaX += worldSize }
	zoomFactor := math.Pow(2, float64(targetZoom-window.Zoom))
	viewportRadius := math.Hypot(float64(canvasWidth)/2, float64(canvasHeight)/2)
	for step := 0; step <= 64; step++ {
		t := float64(step)/64
		zoom := float64(window.Zoom) + math.Log2(1+(zoomFactor-1)*t)
		x := fx + deltaX*t - window.TopLeftX
		y := fy + (ty-fy)*t - window.TopLeftY
		margin := viewportRadius * math.Pow(2, float64(window.Zoom)-zoom)
		if x < margin || y < margin || x > window.Width-margin || y > window.Height-margin {
			return false
		}
	}
	return true
}

func moreDetailed(candidate, incumbent Plate) bool {
	if candidate.Zoom != incumbent.Zoom {
		return candidate.Zoom > incumbent.Zoom
	}
	if area := candidate.Width * candidate.Height; area != incumbent.Width*incumbent.Height {
		return area < incumbent.Width*incumbent.Height
	}
	return candidate.ID < incumbent.ID
}

func certifyPlate(root string, entry manifestPlate) (Plate, error) {
	if strings.TrimSpace(entry.ID) != entry.ID || entry.ID == "" {
		return Plate{}, fmt.Errorf("id must be non-blank and not blank-padded")
	}
	rel := strings.TrimSpace(entry.File)
	if rel == "" {
		return Plate{}, fmt.Errorf("plate %q requires a file", entry.ID)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(filepath.ToSlash(rel), "/") {
		return Plate{}, fmt.Errorf("plate %q file %q must be relative to the manifest directory", entry.ID, entry.File)
	}
	if strings.TrimSpace(entry.License) == "" || strings.TrimSpace(entry.Attribution) == "" {
		return Plate{}, fmt.Errorf("plate %q requires license and attribution", entry.ID)
	}
	if err := (geodesy.Point{Latitude: entry.Center.Latitude, Longitude: entry.Center.Longitude}).Validate(); err != nil {
		return Plate{}, fmt.Errorf("plate %q center: %w", entry.ID, err)
	}
	if entry.Zoom < 0 || entry.Zoom > maxPlateZoom {
		return Plate{}, fmt.Errorf("plate %q zoom %d outside [0,%d]", entry.ID, entry.Zoom, maxPlateZoom)
	}
	if entry.Width < 1 || entry.Width > maxPlateWidth || entry.Height < 1 || entry.Height > maxPlateHeight {
		return Plate{}, fmt.Errorf("plate %q raster %dx%d outside 1..%dx1..%d", entry.ID, entry.Width, entry.Height, maxPlateWidth, maxPlateHeight)
	}

	absolute := filepath.Join(root, filepath.FromSlash(rel))
	contained, err := filepath.Rel(root, absolute)
	if err != nil || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return Plate{}, fmt.Errorf("plate %q file %q escapes the manifest directory", entry.ID, entry.File)
	}

	file, err := os.Open(absolute)
	if err != nil {
		return Plate{}, fmt.Errorf("plate %q raster: %w", entry.ID, err)
	}
	defer file.Close()
	header := make([]byte, len(pngSignature))
	if _, err := io.ReadFull(file, header); err != nil {
		return Plate{}, fmt.Errorf("plate %q raster is not a PNG: %w", entry.ID, err)
	}
	if !bytes.Equal(header, pngSignature) {
		return Plate{}, fmt.Errorf("plate %q raster is not a PNG: unexpected magic", entry.ID)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Plate{}, fmt.Errorf("plate %q raster: %w", entry.ID, err)
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return Plate{}, fmt.Errorf("plate %q raster: %w", entry.ID, err)
	}
	if format != "png" {
		return Plate{}, fmt.Errorf("plate %q raster decoded as %q, want png", entry.ID, format)
	}
	// The declared window IS the georeference: a raster whose real size
	// disagrees with it would put every pin on the wrong pixel, so it is
	// rejected instead of trusted.
	if config.Width != entry.Width || config.Height != entry.Height {
		return Plate{}, fmt.Errorf("plate %q declared %dx%d but the raster is %dx%d", entry.ID, entry.Width, entry.Height, config.Width, config.Height)
	}

	sha256, _, err := digest.SHA256File(absolute)
	if err != nil {
		return Plate{}, fmt.Errorf("plate %q raster: %w", entry.ID, err)
	}

	return Plate{
		ID:          entry.ID,
		File:        filepath.ToSlash(rel),
		License:     entry.License,
		Attribution: entry.Attribution,
		Center:      entry.Center,
		Zoom:        entry.Zoom,
		Width:       entry.Width,
		Height:      entry.Height,
		path:        absolute,
		sha256:      sha256,
		window:      geodesy.CenteredOn(entry.Center.Latitude, entry.Center.Longitude, entry.Zoom, entry.Width, entry.Height),
	}, nil
}
