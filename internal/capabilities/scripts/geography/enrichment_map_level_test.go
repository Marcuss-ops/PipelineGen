package geography

import (
	"context"
	"testing"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// installMapLevelLexicon installs the canonical language snapshot the maps-only
// gates read. Production bootstrap does the same once at startup.
func installMapLevelLexicon(t *testing.T) {
	t.Helper()
	if linguistics.DefaultProfilesOrNil() != nil {
		return
	}
	registry, err := linguistics.NewProfiles()
	if err != nil {
		t.Fatalf("load canonical lexicon: %v", err)
	}
	if err := linguistics.SetDefaultProfiles(registry); err != nil {
		t.Fatalf("install canonical lexicon: %v", err)
	}
}

// levelRecordingGeocoder answers by name and records the level each lookup
// asked for, so the test can prove the run's cartographic level reaches the
// provider instead of being applied only after geocoding.
type levelRecordingGeocoder struct {
	calls []capabilitygeocoding.Request
}

func (g *levelRecordingGeocoder) Geocode(_ context.Context, req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
	g.calls = append(g.calls, req)
	var result capabilitygeocoding.Result
	switch req.Query {
	case "Italia":
		result = capabilitygeocoding.Result{Latitude: 42.6384, Longitude: 12.6743, Scope: "country", Country: "Italia", CountryCode: "it"}
	case "Roma":
		result = capabilitygeocoding.Result{Latitude: 41.8933, Longitude: 12.4829, Scope: "city", Country: "Italia", CountryCode: "it"}
	default:
		return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
	}
	// A provider that honours the level hint answers nothing for a place that
	// only exists at another scale, which is what stops a country-level map
	// from paying for a city coordinate.
	if !capabilitygeocoding.ScopeFitsLevel(result.Scope, req.Level) {
		return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
	}
	return result, nil
}

// requestedLevels returns the distinct level each request carried.
func requestedLevels(calls []capabilitygeocoding.Request) map[string]bool {
	out := make(map[string]bool, len(calls))
	for _, call := range calls {
		out[call.Level] = true
	}
	return out
}

// A country-level map forwards its level to the provider: that is what stops
// the city lookup instead of paying for coordinates the map could never use.
func TestEnrichForwardsTheRequestedMapLevelToTheGeocoder(t *testing.T) {
	installMapLevelLexicon(t)
	geocoder := &levelRecordingGeocoder{}
	annotation := &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{place("Italia")}}
	if err := Enrich(context.Background(), geocoder, map[int]*scriptpkg.SceneAnnotations{0: annotation}, "it",
		Options{MapsOnly: true, SceneTexts: map[int]string{0: "Si parla di Italia e di Roma."}, MapLevel: "country"}); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.calls) == 0 {
		t.Fatal("no lookup reached the provider")
	}
	levels := requestedLevels(geocoder.calls)
	if len(levels) != 1 || !levels[capabilitygeocoding.LevelCountry] {
		t.Fatalf("lookup levels = %v, want only %q", levels, capabilitygeocoding.LevelCountry)
	}
	if annotation.PrimaryEntities[0].Geo == nil {
		t.Fatal("the country itself must still resolve")
	}
	// The city named in the same text is out of level: every lookup that
	// mentions it carries the filter, and no coordinate is attached.
	for _, call := range geocoder.calls {
		if call.Query == "Roma" && call.Level != capabilitygeocoding.LevelCountry {
			t.Fatalf("city lookup dropped the level filter: %+v", call)
		}
	}
	searched := 0
	for _, call := range geocoder.calls {
		if call.Query == "Roma" {
			searched++
		}
	}
	if searched > 1 {
		t.Fatalf("city was re-queried %d times on a country-level map", searched)
	}
}

// A fine map level (municipality) must not narrow the provider: the country in
// the scene is the frame that disambiguates its cities, and a municipality map
// keeps it as context while the planner declines to pin it.
func TestEnrichKeepsFineLevelLookupsUnconstrained(t *testing.T) {
	installMapLevelLexicon(t)
	geocoder := &levelRecordingGeocoder{}
	annotation := &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{place("Italia")}}
	if err := Enrich(context.Background(), geocoder, map[int]*scriptpkg.SceneAnnotations{0: annotation}, "it",
		Options{MapsOnly: true, SceneTexts: map[int]string{0: "Si parla di Italia e di Roma."}, MapLevel: "municipality"}); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.calls) == 0 {
		t.Fatal("no lookup reached the provider")
	}
	for _, call := range geocoder.calls {
		if call.Level != "" {
			t.Fatalf("municipality lookup was narrowed to %q, which erases the country frame: %+v", call.Level, call)
		}
	}
	if annotation.PrimaryEntities[0].Geo == nil {
		t.Fatal("the country frame must still resolve on a municipality map")
	}
}

// A place that arrives already geocoded at another scale is dropped before the
// lookup when the map level is coarse: a city pin on a country frame is not a
// candidate the geocoder can improve, and the map must not pay a request for it.
func TestEnrichDropsPreGroundedPlacesOutsideACoarseMapLevel(t *testing.T) {
	installMapLevelLexicon(t)
	geocoder := &levelRecordingGeocoder{}
	roma := grounded("Roma", 41.8933, 12.4829, "city", "Italia", "it")
	annotation := &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{roma}}
	if err := Enrich(context.Background(), geocoder, map[int]*scriptpkg.SceneAnnotations{0: annotation}, "it",
		Options{MapsOnly: true, SceneTexts: map[int]string{0: "Si parla di Italia e di Roma."}, MapLevel: "country"}); err != nil {
		t.Fatal(err)
	}
	if annotation.PrimaryEntities[0].Geo != nil {
		t.Fatalf("city kept its coordinates on a country map: %+v", annotation.PrimaryEntities[0].Geo)
	}
	for _, call := range geocoder.calls {
		if call.Query == "Roma" {
			t.Fatalf("pre-grounded out-of-level place reached the provider: %+v", call)
		}
		if call.Level != capabilitygeocoding.LevelCountry {
			t.Fatalf("country-level lookup was not narrowed: %+v", call)
		}
	}
}

// An unknown level is a configuration error, not a silent unfiltered lookup.
func TestEnrichRejectsAnUnknownMapLevel(t *testing.T) {
	geocoder := &levelRecordingGeocoder{}
	err := Enrich(context.Background(), geocoder, map[int]*scriptpkg.SceneAnnotations{0: {PrimaryEntities: []scriptpkg.AnnotatedEntity{place("Italia")}}}, "it",
		Options{MapsOnly: true, MapLevel: "galaxy"})
	if err == nil {
		t.Fatal("unknown map level must fail closed")
	}
	if len(geocoder.calls) != 0 {
		t.Fatalf("no lookup may run under an unsupported level: %+v", geocoder.calls)
	}
}
