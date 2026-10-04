package scriptgeneration

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type recordingGeocoder struct {
	requests []capabilitygeocoding.Request
	result   capabilitygeocoding.Result
	err      error
}

func (g *recordingGeocoder) Geocode(_ context.Context, request capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
	g.requests = append(g.requests, request)
	return g.result, g.err
}

func TestGeocodePlaceAnnotationsOnlyLooksUpPlacesAndDeduplicates(t *testing.T) {
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{
		Latitude: 29.9511, Longitude: -90.0715, DisplayName: "New Orleans, Louisiana, United States",
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotations := map[int]*scriptpkg.SceneAnnotations{
		1: {
			Version: 1, Language: "en", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{
				{CanonicalName: "New Orleans", Type: "GPE"},
				{CanonicalName: "New Orleans", Type: "LOCATION"},
				{CanonicalName: "Jane Doe", Type: "PERSON"},
			},
			SecondaryEntities: []scriptpkg.AnnotatedEntity{
				{CanonicalName: "New Orleans", Type: "GPE"},
				{CanonicalName: "economic growth", Type: "CONCEPT"},
			},
		},
		0: {
			Version: 1, Language: "en", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Houston", Type: "LOCATION"}},
		},
	}

	if err := runner.geocodePlaceAnnotations(context.Background(), annotations, "en"); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 2 {
		t.Fatalf("geocoder calls = %d, want unique place names only (Houston, New Orleans)", len(geocoder.requests))
	}
	if geocoder.requests[0].Query != "Houston" || geocoder.requests[1].Query != "New Orleans" {
		t.Fatalf("requests are not in stable scene/entity order: %+v", geocoder.requests)
	}
	for _, annotation := range annotations {
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
			if isPlaceEntityType(entity.Type) {
				if entity.Geo == nil || entity.Geo.Latitude != 29.9511 || entity.Geo.Longitude != -90.0715 {
					t.Errorf("place %q was not enriched from the geocoder: %+v", entity.CanonicalName, entity.Geo)
				}
			} else if entity.Geo != nil {
				t.Errorf("non-place %q unexpectedly received coordinates: %+v", entity.CanonicalName, entity.Geo)
			}
		}
	}
}

func TestRunVidRushJoinAndPrepareSkipsGeocoderWhenPolicyDisabled(t *testing.T) {
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{Latitude: 0, Longitude: 0}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotation := &scriptpkg.SceneAnnotations{
		Version: 1, Language: "en", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Paris", Type: "GPE"}},
	}
	prepared, err := runner.runVidRushJoinAndPrepare(context.Background(), "run-disabled", GenerateRequest{}, []sceneTextSnapshot{{
		ID: "scene-0", Index: 0, Text: "Paris is mentioned here.", Annotations: annotation,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 0 {
		t.Fatalf("disabled provider policy issued %d geocoding requests", len(geocoder.requests))
	}
	if got := prepared.annotations[0].PrimaryEntities[0].Geo; got != nil {
		t.Fatalf("disabled provider policy enriched coordinates: %+v", got)
	}
	if annotation.PrimaryEntities[0].Geo != nil {
		t.Fatalf("prepare branch mutated the shared source annotation: %+v", annotation.PrimaryEntities[0].Geo)
	}
}

func TestRunVidRushJoinAndPrepareGeocodesOnlyOnExplicitEnable(t *testing.T) {
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{Latitude: 48.8566, Longitude: 2.3522}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotation := &scriptpkg.SceneAnnotations{
		Version: 1, Language: "en", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Paris", Type: "GPE"}},
	}
	request := GenerateRequest{}
	request.MediaPlan.ProviderPolicy.Geocoding = mediadomain.MediaToggleEnabled
	prepared, err := runner.runVidRushJoinAndPrepare(context.Background(), "run-enabled", request, []sceneTextSnapshot{{
		ID: "scene-0", Index: 0, Text: "Paris is mentioned here.", Annotations: annotation,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 1 || geocoder.requests[0].Query != "Paris" {
		t.Fatalf("explicit geocoding policy requests = %+v", geocoder.requests)
	}
	if got := prepared.annotations[0].PrimaryEntities[0].Geo; got == nil || got.Latitude != 48.8566 || got.Longitude != 2.3522 {
		t.Fatalf("explicit geocoding policy did not project coordinates: %+v", got)
	}
	if annotation.PrimaryEntities[0].Geo != nil {
		t.Fatalf("prepare branch mutated the shared source annotation: %+v", annotation.PrimaryEntities[0].Geo)
	}
}

func TestMapPlaceCandidateIgnoredWordsUsesConfiguredLexicon(t *testing.T) {
	registry, err := linguistics.NewLexiconRegistry("../../../config/lexicons")
	if err != nil {
		t.Fatalf("load canonical lexicon: %v", err)
	}
	ignored, err := mapPlaceCandidateIgnoredWords(registry, "en")
	if err != nil {
		t.Fatalf("resolve English map candidate exclusions: %v", err)
	}
	for _, word := range []string{"a", "an", "the", "this", "from", "our", "we", "it", "as", "and", "but", "journey", "tracing", "continuing"} {
		if _, ok := ignored[word]; !ok {
			t.Errorf("canonical English entity blocklist does not exclude %q", word)
		}
	}
	if _, err := mapPlaceCandidateIgnoredWords(registry, "missing-language"); err == nil {
		t.Fatal("unconfigured language must fail closed rather than invent a stopword set")
	}
	if _, err := mapPlaceCandidateIgnoredWords(nil, "en"); err == nil {
		t.Fatal("missing lexicon registry must fail closed")
	}
}

func TestGeocodePlaceAnnotationsFailsClosedForMissingAdapterAndInvalidResult(t *testing.T) {
	annotations := map[int]*scriptpkg.SceneAnnotations{0: {
		PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Paris", Type: "GPE"}},
	}}
	if err := NewRunner(newInMemRunRepository(), nil, nil, nil, nil).geocodePlaceAnnotations(context.Background(), annotations, "en"); err == nil {
		t.Fatal("enabled geocoding without an adapter must fail closed")
	}

	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{Latitude: 91, Longitude: 0}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	if err := runner.geocodePlaceAnnotations(context.Background(), annotations, "en"); err == nil {
		t.Fatal("out-of-range provider coordinates must fail closed")
	}
	if annotations[0].PrimaryEntities[0].Geo != nil {
		t.Fatal("invalid coordinates must not be projected")
	}

	geocoder.err = errors.New("provider unavailable")
	if err := runner.geocodePlaceAnnotations(context.Background(), annotations, "en"); err == nil {
		t.Fatal("provider failure must fail closed when geocoding was explicitly enabled")
	}
}
