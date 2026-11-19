package geography

import (
	"context"
	"testing"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// countryConstrainedGeocoder reproduces the provider behaviour the r14 job hit:
// a place is unresolvable when the lookup carries a country the place does not
// belong to, and resolves normally once the frame is dropped.
type countryConstrainedGeocoder struct {
	calls []capabilitygeocoding.Request
}

func (g *countryConstrainedGeocoder) Geocode(_ context.Context, req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
	g.calls = append(g.calls, req)
	switch req.Query {
	case "Parigi":
		if req.Context.CountryCode != "" {
			return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
		}
		return capabilitygeocoding.Result{Latitude: 48.8534951, Longitude: 2.3483915, Scope: "city", Country: "Francia", CountryCode: "fr"}, nil
	case "Roma":
		// Rome is only resolvable under the Italian frame, like the r14 job:
		// the unconstrained lookup returns the provider's ambiguous top hit.
		if req.Context.CountryCode != "it" {
			return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
		}
		return capabilitygeocoding.Result{Latitude: 41.8933, Longitude: 12.4829, Scope: "city", Country: "Italia", CountryCode: "it"}, nil
	case "Torino":
		return capabilitygeocoding.Result{Latitude: 45.0678, Longitude: 7.6825, Scope: "city", Country: "Italia", CountryCode: "it"}, nil
	case "Italia":
		return capabilitygeocoding.Result{Latitude: 42.6384, Longitude: 12.6743, Scope: "country", Country: "Italia", CountryCode: "it"}, nil
	case "Francia":
		return capabilitygeocoding.Result{Latitude: 46.6034, Longitude: 1.8883, Scope: "country", Country: "Francia", CountryCode: "fr"}, nil
	default:
		return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
	}
}

func place(name string) scriptpkg.AnnotatedEntity {
	return scriptpkg.AnnotatedEntity{Text: name, CanonicalName: name, Type: "GPE"}
}

func grounded(name string, lat, lon float64, scope, country, code string) scriptpkg.AnnotatedEntity {
	entity := place(name)
	entity.Geo = &scriptpkg.GeoCoordinate{Latitude: lat, Longitude: lon, Scope: scope, Country: country, CountryCode: code}
	return entity
}

// A scene that names two countries must not resolve a later city under the
// country of an earlier one. Regression for the r14 job where the order
// Roma, Torino, Parigi left Parigi without coordinates.
func TestEnrichRetriesAmbiguousCountryCityWithoutTheStaleFrame(t *testing.T) {
	annotation := &scriptpkg.SceneAnnotations{
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			place("Roma"), place("Torino"), place("Parigi"),
			place("Italia"), place("Alpi"), place("Francia"), place("Italia"),
		},
	}
	geocoder := &countryConstrainedGeocoder{}
	annotations := map[int]*scriptpkg.SceneAnnotations{0: annotation}
	if err := Enrich(context.Background(), geocoder, annotations, "it"); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	parigi := annotation.PrimaryEntities[2]
	if parigi.Geo == nil {
		t.Fatalf("Parigi still unresolved after the retry pass; calls=%v", geocoder.calls)
	}
	if parigi.Geo.CountryCode != "fr" {
		t.Fatalf("Parigi resolved to %q, want fr", parigi.Geo.CountryCode)
	}
	// Rome needs the Italian frame: the multi-country retry must try the
	// countries the scene grounded before falling back to no constraint.
	roma := annotation.PrimaryEntities[0]
	if roma.Geo == nil {
		t.Fatalf("Roma still unresolved after the retry pass; calls=%v", geocoder.calls)
	}
	if roma.Geo.CountryCode != "it" {
		t.Fatalf("Roma resolved to %q, want it", roma.Geo.CountryCode)
	}
}

// A single-country scene keeps the frame: that is what disambiguates a
// namesake city.
func TestEnrichKeepsTheCountryFrameForASingleCountryScene(t *testing.T) {
	annotation := &scriptpkg.SceneAnnotations{
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			grounded("Italia", 42.63, 12.67, "country", "Italia", "it"),
			place("Parigi"),
		},
	}
	geocoder := &countryConstrainedGeocoder{}
	if err := Enrich(context.Background(), geocoder, map[int]*scriptpkg.SceneAnnotations{0: annotation}, "it"); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if annotation.PrimaryEntities[1].Geo != nil {
		t.Fatalf("Parigi resolved inside a single-country Italian scene, want the constrained lookup to fail closed")
	}
	for _, call := range geocoder.calls {
		if call.Query == "Parigi" && call.Context.CountryCode == "" {
			t.Fatalf("single-country scene dropped its frame: %+v", call)
		}
	}
}

func TestSceneCountryCodesCountsDistinctGroundedCountries(t *testing.T) {
	annotation := &scriptpkg.SceneAnnotations{
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			grounded("Roma", 41.89, 12.48, "city", "Italia", "it"),
			grounded("Parigi", 48.85, 2.34, "city", "Francia", "fr"),
			grounded("Italia", 42.63, 12.67, "country", "Italia", "it"),
			place("Torino"),
		},
	}
	if codes := sceneCountryCodes(annotation); len(codes) != 2 {
		t.Fatalf("sceneCountryCodes = %v, want it,fr", codes)
	}
}
