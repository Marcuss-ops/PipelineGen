package overlays

import (
	"math"
	"testing"
)

// TestNewMapCandidateAcceptsGroundedPlaceAndProjectsTiming certifies the
// happy path: a validated WGS84 point with a real grounded audio span becomes
// a candidate whose millisecond projection is the contract's floor-start /
// ceil-end form.
func TestNewMapCandidateAcceptsGroundedPlaceAndProjectsTiming(t *testing.T) {
	candidate, ok := NewMapCandidate("ent_neworleans", "  New   Orleans  ", 29.9511, -90.0715, 1_250_000, 3_000_000, 0.9)
	if !ok {
		t.Fatal("a grounded WGS84 place with an audio span must produce a candidate")
	}
	if candidate.EntityID != "ent_neworleans" {
		t.Errorf("entity id = %q, want the stable id verbatim", candidate.EntityID)
	}
	if candidate.Label != "New Orleans" {
		t.Errorf("label = %q, want the whitespace-normalized canonical name", candidate.Label)
	}
	if candidate.Latitude != 29.9511 || candidate.Longitude != -90.0715 {
		t.Errorf("coordinates = (%v, %v), want (29.9511, -90.0715)", candidate.Latitude, candidate.Longitude)
	}
	if candidate.StartUS != 1_250_000 || candidate.DurationUS != 3_000_000 {
		t.Errorf("us timing = (%d, %d), want (1250000, 3000000)", candidate.StartUS, candidate.DurationUS)
	}
	if candidate.StartMs != 1_250 || candidate.EndMs != 4_250 {
		t.Errorf("ms projection = (%d, %d), want (1250, 4250) — floor start, ceil end", candidate.StartMs, candidate.EndMs)
	}
}

// TestNewMapCandidateFailsClosedForUngroundedOrNonWGS84Data is the map
// contract's admission gate: nothing here may become a map, because a map
// pinned at a guessed coordinate lies about geography.
func TestNewMapCandidateFailsClosedForUngroundedOrNonWGS84Data(t *testing.T) {
	cases := []struct {
		name                string
		entityID, label     string
		lat, lon            float64
		startUS, durationUS int64
	}{
		{"blank entity id", "", "Rome", 41.9, 12.5, 0, 1_000_000},
		{"blank label", "ent_1", "   ", 41.9, 12.5, 0, 1_000_000},
		{"latitude above range", "ent_1", "Rome", 90.0001, 12.5, 0, 1_000_000},
		{"latitude below range", "ent_1", "Rome", -90.0001, 12.5, 0, 1_000_000},
		{"longitude above range", "ent_1", "Rome", 41.9, 180.0001, 0, 1_000_000},
		{"longitude below range", "ent_1", "Rome", 41.9, -180.0001, 0, 1_000_000},
		{"nan latitude", "ent_1", "Rome", math.NaN(), 12.5, 0, 1_000_000},
		{"infinite longitude", "ent_1", "Rome", 41.9, math.Inf(1), 0, 1_000_000},
		{"negative start", "ent_1", "Rome", 41.9, 12.5, -1, 1_000_000},
		{"zero duration", "ent_1", "Rome", 41.9, 12.5, 0, 0},
		{"negative duration", "ent_1", "Rome", 41.9, 12.5, 0, -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := NewMapCandidate(tc.entityID, tc.label, tc.lat, tc.lon, tc.startUS, tc.durationUS, 0.5); ok {
				t.Fatal("ungrounded or non-WGS84 data must not produce a map candidate")
			}
		})
	}
}
