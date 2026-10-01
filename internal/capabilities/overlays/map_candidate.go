package overlays

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// MapCandidate is one GROUNDED place annotation projected onto the planner's
// per-scene input. It carries the stable entity identity the canonical entity
// timeline resolved (StableEntityID), the validated WGS84 point, and the
// grounded occurrence's audio bounds — never guessed timing, never an
// ungrounded place and never an out-of-range coordinate.
//
// The planner turns candidates into at most one map item per scene; the
// basemap itself is an operator-supplied raster supplied separately, so a
// place that cannot be grounded here produces no map at all rather than a
// fabricated one.
type MapCandidate struct {
	// EntityID is the content-addressed StableEntityID the occurrence was
	// joined on — the same identity the entity card and the media index use.
	EntityID string
	// Label is the place's canonical name, rendered as the pin label.
	Label string
	// Latitude/Longitude are the geocoder-validated WGS84 degrees.
	Latitude  float64
	Longitude float64
	// StartMs/EndMs are the grounded occurrence's audio window (floor start,
	// ceil end); StartUS/DurationUS are the canonical microsecond form.
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
}

// NewMapCandidate validates and builds a grounded map candidate. It fails
// closed — ok is false — for a blank stable id, a blank label, a non-finite or
// out-of-WGS84 coordinate, or an empty audio span, so ungrounded or non-WGS84
// data can never become a map.
func NewMapCandidate(entityID, label string, latitude, longitude float64, startUS, durationUS int64, score float64) (MapCandidate, bool) {
	if strings.TrimSpace(entityID) == "" || strings.TrimSpace(label) == "" {
		return MapCandidate{}, false
	}
	if err := (geodesy.Point{Latitude: latitude, Longitude: longitude}).Validate(); err != nil {
		return MapCandidate{}, false
	}
	if startUS < 0 || durationUS <= 0 {
		return MapCandidate{}, false
	}
	return MapCandidate{
		EntityID: entityID,
		Label:    strings.Join(strings.Fields(label), " "),
		// The millisecond projection mirrors the plan contract's floor
		// start / ceil end so the two representations never drift.
		Latitude:   latitude,
		Longitude:  longitude,
		StartMs:    startUS / 1000,
		EndMs:      (startUS + durationUS + 999) / 1000,
		StartUS:    startUS,
		DurationUS: durationUS,
		Score:      score,
	}, true
}
