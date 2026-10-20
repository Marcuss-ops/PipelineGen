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

// contextRecordingGeocoder answers each lookup from its (query, context) and
// records every request, so tests can prove which grounded context reached the
// provider.
type contextRecordingGeocoder struct {
	requests []capabilitygeocoding.Request
	answer   func(capabilitygeocoding.Request) (capabilitygeocoding.Result, error)
}

func (g *contextRecordingGeocoder) Geocode(_ context.Context, request capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
	g.requests = append(g.requests, request)
	return g.answer(request)
}

// TestGeocodePlaceAnnotationsUsesGroundedSceneContext proves the scene's own
// already-grounded places become the lookup context (never invented): the
// homonymous city is asked under the country the scene already certified.
func TestGeocodePlaceAnnotationsUsesGroundedSceneContext(t *testing.T) {
	geocoder := &contextRecordingGeocoder{answer: func(req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
		if req.Query == "Paris" {
			return capabilitygeocoding.Result{Latitude: 48.8566, Longitude: 2.3522, Scope: "city", DisplayName: "Paris, France"}, nil
		}
		return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotations := map[int]*scriptpkg.SceneAnnotations{
		0: {
			Version: 1, Language: "fr", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{
				{CanonicalName: "Paris", Type: "GPE"},
				{CanonicalName: "France", Type: "GPE", Geo: &scriptpkg.GeoCoordinate{Latitude: 46.2276, Longitude: 2.2137, Scope: "country"}},
			},
		},
	}
	if err := runner.geocodePlaceAnnotations(context.Background(), annotations, "fr"); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 1 {
		t.Fatalf("geocoder calls = %d, want 1 (Paris only): %+v", len(geocoder.requests), geocoder.requests)
	}
	if got := geocoder.requests[0].Context.Country; got != "France" {
		t.Fatalf("lookup context country = %q, want the scene's grounded France", got)
	}
}

func TestGeocodePlaceAnnotationsRetriesCityAfterCountryContextAppears(t *testing.T) {
	geocoder := &contextRecordingGeocoder{answer: func(req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
		switch req.Query {
		case "Lisbona":
			if req.Context.Country == "Portogallo" {
				return capabilitygeocoding.Result{Latitude: 38.7223, Longitude: -9.1393, Scope: "city", DisplayName: "Lisboa, Portugal"}, nil
			}
			return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
		case "Portogallo":
			return capabilitygeocoding.Result{Latitude: 39.6622, Longitude: -8.1354, Scope: "country", DisplayName: "Portugal"}, nil
		default:
			return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
		}
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotations := map[int]*scriptpkg.SceneAnnotations{0: {
		Version: 1, Language: "it", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{CanonicalName: "Lisbona", Type: "GPE"},
			{CanonicalName: "Portogallo", Type: "GPE"},
		},
	}}
	if err := runner.geocodePlaceAnnotations(context.Background(), annotations, "it"); err != nil {
		t.Fatal(err)
	}
	city := annotations[0].PrimaryEntities[0].Geo
	if city == nil || city.Latitude != 38.7223 || city.Longitude != -9.1393 {
		t.Fatalf("city was not retried under the grounded country context: %+v", city)
	}
	if len(geocoder.requests) != 3 || geocoder.requests[0].Context.Country != "" || geocoder.requests[2].Context.Country != "Portogallo" {
		t.Fatalf("lookup sequence did not retry the city after grounding country context: %+v", geocoder.requests)
	}
}

func TestGeocodePlaceAnnotationsSkipsItalianGenericPaese(t *testing.T) {
	geocoder := &contextRecordingGeocoder{answer: func(req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
		if req.Query == "Coimbra" {
			return capabilitygeocoding.Result{Latitude: 40.2033, Longitude: -8.4103, DisplayName: "Coimbra, Portugal", Scope: "city"}, nil
		}
		return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotations := map[int]*scriptpkg.SceneAnnotations{0: {
		Version: 1, Language: "it", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Paese", Type: "LOCATION", Geo: &scriptpkg.GeoCoordinate{
			Latitude: 45.6999302, Longitude: 12.1491918, DisplayName: "Paese, Treviso, Italia", Scope: "city",
		}}},
	}}
	if err := runner.geocodePlaceAnnotations(context.Background(), annotations, "it", geocodeOptions{
		sceneTexts: map[int]string{0: "Ci spostiamo verso Coimbra, nel centro del Paese."}, mapsOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 1 || geocoder.requests[0].Query != "Coimbra" || annotations[0].PrimaryEntities[0].Geo != nil {
		t.Fatalf("generic noun produced a homonymous map instead of recovering Coimbra: requests=%+v geo=%+v", geocoder.requests, annotations[0].PrimaryEntities[0].Geo)
	}
	if len(annotations[0].PrimaryEntities) < 2 || annotations[0].PrimaryEntities[1].CanonicalName != "Coimbra" || annotations[0].PrimaryEntities[1].Geo == nil || annotations[0].PrimaryEntities[1].Geo.Latitude != 40.2033 {
		t.Fatalf("grounded city candidate was not recovered: %+v", annotations[0].PrimaryEntities)
	}
}

func TestMapOnlyGeocodingUsesGroundedCountryForCity(t *testing.T) {
	geocoder := &contextRecordingGeocoder{answer: func(req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
		if req.Query == "Lisbona" && req.Context.Country == "Portogallo" {
			return capabilitygeocoding.Result{Latitude: 38.7223, Longitude: -9.1393, DisplayName: "Lisboa, Portugal", Scope: "city"}, nil
		}
		return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotations := map[int]*scriptpkg.SceneAnnotations{0: {
		Version: 1, Language: "it", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{CanonicalName: "Lisbona", Type: "GPE"},
			{CanonicalName: "Portogallo", Type: "GPE", Geo: &scriptpkg.GeoCoordinate{Latitude: 39.66, Longitude: -8.13, Scope: "country"}},
		},
	}}
	err := runner.geocodePlaceAnnotations(context.Background(), annotations, "it", geocodeOptions{
		sceneTexts: map[int]string{0: "Il viaggio comincia a Lisbona, in Portogallo."}, mapsOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	city := annotations[0].PrimaryEntities[0].Geo
	if city == nil || city.Latitude != 38.7223 || city.Longitude != -9.1393 {
		t.Fatalf("map-only request did not resolve the grounded city under its country: %+v", city)
	}
}

func TestMapOnlyGeocodingRecoversCityOmittedByEntityExtractor(t *testing.T) {
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{
		Latitude: 40.2033, Longitude: -8.4103, DisplayName: "Coimbra, Portugal", Scope: "city",
	}}
	annotation := &scriptpkg.SceneAnnotations{Version: 1, Language: "it", Status: "completed"}
	var geoContext capabilitygeocoding.Context
	if err := geocodeGroundedPlaceNames(context.Background(), geocoder, map[string]capabilitygeocoding.Result{},
		annotation, "Ci spostiamo verso Coimbra, nel centro del Paese.", "it", 0, &geoContext); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 1 || geocoder.requests[0].Query != "Coimbra" {
		t.Fatalf("map-only candidate recovery requests=%+v, want only Coimbra", geocoder.requests)
	}
	if len(annotation.PrimaryEntities) != 1 || annotation.PrimaryEntities[0].CanonicalName != "Coimbra" || annotation.PrimaryEntities[0].Geo == nil || annotation.PrimaryEntities[0].Geo.Latitude != 40.2033 {
		t.Fatalf("grounded Coimbra was not recovered without the generic Paese false positive: %+v", annotation.PrimaryEntities)
	}
}

// TestGeocodePlaceAnnotationsSeparatesContextsAcrossScenes proves one name
// under two different grounded contexts is two lookups, not one shared cached
// answer: each scene resolves its own locality.
func TestGeocodePlaceAnnotationsSeparatesContextsAcrossScenes(t *testing.T) {
	geocoder := &contextRecordingGeocoder{answer: func(req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
		switch req.Context.Country {
		case "France":
			return capabilitygeocoding.Result{Latitude: 48.8566, Longitude: 2.3522, Scope: "city", DisplayName: "Paris, France"}, nil
		case "United States":
			return capabilitygeocoding.Result{Latitude: 33.6617, Longitude: -95.5555, Scope: "city", DisplayName: "Paris, Texas"}, nil
		default:
			return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
		}
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotations := map[int]*scriptpkg.SceneAnnotations{
		0: {
			Version: 1, Language: "en", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{
				{CanonicalName: "Paris", Type: "GPE"},
				{CanonicalName: "France", Type: "GPE", Geo: &scriptpkg.GeoCoordinate{Latitude: 46.2276, Longitude: 2.2137, Scope: "country"}},
			},
		},
		1: {
			Version: 1, Language: "en", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{
				{CanonicalName: "Paris", Type: "GPE"},
				{CanonicalName: "United States", Type: "GPE", Geo: &scriptpkg.GeoCoordinate{Latitude: 37.0902, Longitude: -95.7129, Scope: "country"}},
			},
		},
	}
	if err := runner.geocodePlaceAnnotations(context.Background(), annotations, "en"); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 2 {
		t.Fatalf("geocoder calls = %d, want one per context: %+v", len(geocoder.requests), geocoder.requests)
	}
	fr := annotations[0].PrimaryEntities[0].Geo
	us := annotations[1].PrimaryEntities[0].Geo
	if fr == nil || us == nil || fr.Latitude == us.Latitude {
		t.Fatalf("one shared answer served two contexts: fr=%+v us=%+v", fr, us)
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

func TestRequestedMapCountGeocodesGroundedPlaceNames(t *testing.T) {
	lexicon, err := linguistics.NewLexiconRegistry("../../../config/lexicons")
	if err != nil {
		t.Fatal(err)
	}
	if err := linguistics.SetDefaultLexicon(lexicon); err != nil {
		t.Fatal(err)
	}
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{
		Latitude: 45.184, Longitude: 8.997, DisplayName: "Pavia, Lombardy, Italy", Scope: "city",
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	request := GenerateRequest{SourceLanguage: "it", AnimationCounts: map[string]int{"maps": 5}}
	request.MediaPlan.ProviderPolicy.Geocoding = mediadomain.MediaToggleEnabled
	annotation := &scriptpkg.SceneAnnotations{Version: 1, Language: "it", Status: "completed"}
	prepared, err := runner.runVidRushJoinAndPrepare(context.Background(), "run-map-count", request, []sceneTextSnapshot{{
		ID: "scene-pavia", Index: 0, Text: "Pavia apre il racconto giudiziario.", Annotations: annotation,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 1 || geocoder.requests[0].Query != "Pavia" {
		t.Fatalf("requested maps did not geocode the exact spoken place: %+v", geocoder.requests)
	}
	places := 0
	for _, entity := range prepared.annotations[0].PrimaryEntities {
		if isPlaceEntityType(entity.Type) && entity.Geo != nil {
			places++
		}
	}
	if places != 1 {
		t.Fatalf("grounded map places = %d, want one validated city candidate: %+v", places, prepared.annotations[0].PrimaryEntities)
	}
}

func TestGroundedMapFallbackIgnoresSentenceAdverbsAndUppercaseOrganizations(t *testing.T) {
	lexicon, err := linguistics.NewLexiconRegistry("../../../config/lexicons")
	if err != nil {
		t.Fatal(err)
	}
	if err := linguistics.SetDefaultLexicon(lexicon); err != nil {
		t.Fatal(err)
	}
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{
		Latitude: 45.184, Longitude: 8.997, DisplayName: "homonymous locality", Scope: "city",
	}}
	annotation := &scriptpkg.SceneAnnotations{Version: 1, Language: "it", Status: "completed"}
	var noContext capabilitygeocoding.Context
	if err := geocodeGroundedPlaceNames(context.Background(), geocoder, map[string]capabilitygeocoding.Result{},
		annotation, "Infine, ANSA pubblica un aggiornamento.", "it", 0, &noContext); err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 0 || len(annotation.PrimaryEntities) != 0 {
		t.Fatalf("prose and uppercase organization became map places: requests=%+v entities=%+v", geocoder.requests, annotation.PrimaryEntities)
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

func TestMapGeocodingSkipsItalianDiscourseWordsMisclassifiedAsPlaces(t *testing.T) {
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{
		Latitude: 45.184, Longitude: 9.158, DisplayName: "Pavia, Italy", Scope: "city",
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil, nil)
	runner.SetGeocoder(geocoder)
	annotation := &scriptpkg.SceneAnnotations{
		Version: 1, Language: "it", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{CanonicalName: "Tale", Type: "LOCATION"},
			{CanonicalName: "Ciò", Type: "LOCATION"},
			{CanonicalName: "Egli", Type: "LOCATION"},
			{CanonicalName: "Essa", Type: "LOCATION"},
			{CanonicalName: "Secondo", Type: "LOCATION"},
			{CanonicalName: "Solo", Type: "LOCATION"},
			{CanonicalName: "Usare", Type: "LOCATION"},
			{CanonicalName: "Durante", Type: "LOCATION"},
			{CanonicalName: "Stasi", Type: "LOCATION"},
			{CanonicalName: "Sempio", Type: "LOCATION"},
			{CanonicalName: "Pavia", Type: "GPE"},
		},
	}
	err := runner.geocodePlaceAnnotations(context.Background(), map[int]*scriptpkg.SceneAnnotations{0: annotation}, "it", geocodeOptions{
		sceneTexts: map[int]string{0: "Tale è la procedura; ciò avviene; egli è indagato; essa parla secondo gli atti, usare con cautela durante il processo, solo Alberto Stasi e Andrea Sempio a Pavia."},
		mapsOnly:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(geocoder.requests) != 1 || geocoder.requests[0].Query != "Pavia" {
		t.Fatalf("map geocoder requests = %+v, want only the grounded city Pavia", geocoder.requests)
	}
	for _, entity := range annotation.PrimaryEntities[:10] {
		if entity.Geo != nil {
			t.Errorf("Italian discourse word %q unexpectedly received map coordinates: %+v", entity.CanonicalName, entity.Geo)
		}
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
