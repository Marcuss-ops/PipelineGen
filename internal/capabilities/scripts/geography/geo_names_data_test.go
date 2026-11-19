package geography

import (
	"testing"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// The geographic article rule must read its vocabulary from the Rust language
// sources: regions and country exonyms live in
// BrainRust/lexicon/languages/it/ and reach Go through the generated profile.
func TestGeoNameWordSetsComeFromTheRustLanguageSources(t *testing.T) {
	registry, err := linguistics.NewProfiles()
	if err != nil {
		t.Fatalf("load canonical lexicon: %v", err)
	}
	it, err := registry.ResolveRequired("it")
	if err != nil {
		t.Fatalf("resolve it profile: %v", err)
	}
	for _, region := range []string{"toscana", "lombardia", "valle d'aosta", "emilia-romagna"} {
		if _, ok := it.WordSets["admin_regions"][region]; !ok {
			t.Errorf("Italian admin_regions is missing %q", region)
		}
	}
	for _, country := range []string{"italia", "francia", "paesi bassi", "regno unito", "stati uniti"} {
		if _, ok := it.WordSets["country_names"][country]; !ok {
			t.Errorf("Italian country_names is missing %q", country)
		}
	}
	// A language that has not curated the set has no regions: it must never
	// inherit Italian geography.
	en, err := registry.ResolveRequired("en")
	if err != nil {
		t.Fatalf("resolve en profile: %v", err)
	}
	if _, ok := en.WordSets["admin_regions"]; ok {
		t.Fatal("English profile must not carry Italian admin regions")
	}
}

// Generic place labels are data too: the language file lists its own common
// nouns and the cross-linguistic fallback covers the shared ones.
func TestGenericPlaceLabelsComeFromThePacks(t *testing.T) {
	for _, tc := range []struct {
		name, language string
		want           bool
	}{
		{"Paese", "it", true},
		{"dopo", "it", true},
		{"l'ergastolo", "it", true},
		{"city", "en", true},
		{"nearby", "it", true},
		{"Coimbra", "it", false},
		{"Pavia", "en", false},
	} {
		if got := isGenericPlaceLabel(tc.name, tc.language); got != tc.want {
			t.Errorf("isGenericPlaceLabel(%q, %q) = %v, want %v", tc.name, tc.language, got, tc.want)
		}
	}
}

// The behaviour that motivated the data lives on: a known region loses the
// article, a city that starts with the same article keeps it.
func TestArticleStrippingUsesTheCuratedGeoNameSets(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"La Toscana", "Toscana"},
		{"L'Italia", "Italia"},
		{"La Francia", "Francia"},
		{"La Spezia", "La Spezia"},
		{"L'Aquila", "L'Aquila"},
		{"Il Veneto", "Veneto"},
		{"La California", "La California"},
	} {
		if got := normalizeGeocodePlaceQuery(tc.in, "it"); got != tc.want {
			t.Errorf("normalizeGeocodePlaceQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// No article stripping for a language without the curated sets.
	if got := normalizeGeocodePlaceQuery("La Toscana", "en"); got != "La Toscana" {
		t.Errorf("English normalization rewrote an Italian phrase into %q", got)
	}
}

// A country query never inherits the scene country as a provider filter, and
// the country vocabulary is data rather than a Go list.
func TestCountryLookupContextIsDrivenByTheProfile(t *testing.T) {
	italia := capabilitygeocoding.Context{Country: "Italia", CountryCode: "it"}
	ctx := placeLookupContext("Francia", "it", &italia)
	if ctx.CountryCode != "" || ctx.Country != "" {
		t.Fatalf("country query kept the scene frame: %+v", ctx)
	}
	pavia := placeLookupContext("Pavia", "it", &italia)
	if pavia.CountryCode != "it" {
		t.Fatalf("city query lost the scene frame: %+v", pavia)
	}
}
