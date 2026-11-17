package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	geography "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/geography"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func ensureRegionGeocodingLexicon(t *testing.T) {
	t.Helper()
	lexicon, err := linguistics.NewProfiles()
	if err != nil {
		t.Fatalf("load lexicon: %v", err)
	}
	if err := linguistics.SetDefaultProfiles(lexicon); err != nil {
		t.Fatalf("set lexicon: %v", err)
	}
}

// TestMapOnlyGeocodingKeepsRegionAndCityAsSeparateCandidates pins the generic
// fix for "La Toscana": the fallback discovers it, but the old "most specific
// scope wins" pass removed every region as soon as the same scene contained a
// city. The region and the cities must stay distinct grounded candidates; the
// planner, not this extraction step, decides which maps fit the budget.
func TestMapOnlyGeocodingKeepsRegionAndCityAsSeparateCandidates(t *testing.T) {
	ensureRegionGeocodingLexicon(t)
	geocoder := &contextRecordingGeocoder{answer: func(req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
		switch req.Query {
		case "Toscana":
			return capabilitygeocoding.Result{
				Latitude: 43.4586541, Longitude: 11.1389204, Scope: "region",
				DisplayName: "Toscana, Italia", Country: "Italia", CountryCode: "it",
			}, nil
		case "Firenze":
			return capabilitygeocoding.Result{
				Latitude: 43.7697955, Longitude: 11.2556404, Scope: "city",
				DisplayName: "Firenze, Toscana, Italia", Country: "Italia", CountryCode: "it",
			}, nil
		case "Venezia":
			return capabilitygeocoding.Result{
				Latitude: 45.4371908, Longitude: 12.3345898, Scope: "city",
				DisplayName: "Venezia, Veneto, Italia", Country: "Italia", CountryCode: "it",
			}, nil
		default:
			return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
		}
	}}
	annotation := &scriptpkg.SceneAnnotations{Version: 1, Language: "it", Status: "completed"}
	var geoContext capabilitygeocoding.Context
	err := geography.GeocodeGroundedPlaceNames(context.Background(), geocoder, map[string]capabilitygeocoding.Result{},
		annotation, "La Toscana ci regala Firenze e Venezia.", "it", 0, &geoContext)
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[string]int{}
	region := ""
	for _, entity := range annotation.PrimaryEntities {
		if entity.Geo == nil {
			continue
		}
		scopes[entity.Geo.Scope]++
		if entity.Geo.Scope == "region" && strings.Contains(entity.CanonicalName, "Toscana") {
			region = entity.CanonicalName
		}
	}
	if region == "" {
		t.Fatalf("Toscana was dropped when the same scene named cities: %+v", annotation.PrimaryEntities)
	}
	if scopes["region"] != 1 || scopes["city"] != 2 {
		t.Fatalf("grounded scopes = %+v, want one region and two cities: %+v", scopes, annotation.PrimaryEntities)
	}
}

// TestMapOnlyGeocodingKeepsAnnotatedRegionBehindItalianArticle covers the
// extraction path where the NER row is already "Toscana" but the scene text
// says "La Toscana": the leading article must not make the region look like a
// fragment of a multi-token person name.
func TestMapOnlyGeocodingKeepsAnnotatedRegionBehindItalianArticle(t *testing.T) {
	ensureRegionGeocodingLexicon(t)
	geocoder := &recordingGeocoder{result: capabilitygeocoding.Result{
		Latitude: 43.4586541, Longitude: 11.1389204, Scope: "region",
		DisplayName: "Toscana, Italia", Country: "Italia", CountryCode: "it",
	}}
	runner := NewRunner(newInMemRunRepository(), nil, nil, nil)
	runner.SetPhraseAligner(testPhraseAligner{})
	runner.SetOverlayPlanCompiler(passthroughOverlayPlanCompiler{})
	runner.SetGeocoder(geocoder)
	annotation := &scriptpkg.SceneAnnotations{
		Version: 1, Language: "it", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Toscana", Type: "LOCATION"}},
	}
	err := runner.geocodePlaceAnnotations(context.Background(),
		map[int]*scriptpkg.SceneAnnotations{0: annotation}, "it", geocodeOptions{
			SceneTexts: map[int]string{0: "La Toscana ci regala Firenze e Venezia."},
			MapsOnly:   true,
		})
	if err != nil {
		t.Fatal(err)
	}
	lookedUp := false
	for _, request := range geocoder.requests {
		if request.Query == "Toscana" {
			lookedUp = true
			break
		}
	}
	if !lookedUp {
		t.Fatalf("article-prefixed region was not looked up: %+v", geocoder.requests)
	}
	if geo := annotation.PrimaryEntities[0].Geo; geo == nil || geo.Scope != "region" {
		t.Fatalf("Toscana was not grounded as a region: %+v", geo)
	}
}
