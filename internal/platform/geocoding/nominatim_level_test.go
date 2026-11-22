package geocoding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	capgeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
)

func newLevelAdapter(t *testing.T, handler http.HandlerFunc) *Nominatim {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adapter, err := NewNominatim(NominatimConfig{
		BaseURL: server.URL, UserAgent: "PipelineGen-geocoder-test/1.0 (mailto:ops@example.test)",
		CacheDir: filepath.Join(t.TempDir(), "cache"), HTTPClient: server.Client(), MinimumInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

// A country-level map asks the provider for countries only: the city name
// never comes back as a coordinate the map could not render.
func TestNominatimCountryLevelSendsFeatureTypeAndRejectsACity(t *testing.T) {
	var seen atomic.Value
	adapter := newLevelAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.URL.Query().Get("featureType"))
		w.Header().Set("Content-Type", "application/json")
		// A compatible endpoint that ignores featureType still answers with the
		// city: the adapter must refuse it rather than cache it.
		_, _ = w.Write([]byte(`[{"lat":"41.8933","lon":"12.4829","display_name":"Roma, Lazio, Italia","addresstype":"city","type":"administrative","class":"place","address":{"city":"Roma","state":"Lazio","country":"Italia","country_code":"it"}}]`))
	})
	_, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Roma", Language: "it", Level: capgeocoding.LevelCountry})
	if !errors.Is(err, capgeocoding.ErrNoResult) {
		t.Fatalf("city answer at country level = %v, want ErrNoResult", err)
	}
	if got, _ := seen.Load().(string); got != "country" {
		t.Fatalf("featureType = %q, want country", got)
	}
}

// A hit that really belongs to the requested level is accepted, and the level
// hint maps onto the provider vocabulary.
func TestNominatimLevelHintMapsOntoProviderVocabulary(t *testing.T) {
	cases := []struct {
		level, featureType, query, hit string
	}{
		{capgeocoding.LevelCountry, "country", "Italia", `[{"lat":"42.6384","lon":"12.6743","display_name":"Italia","addresstype":"country","type":"administrative","class":"boundary","address":{"country":"Italia","country_code":"it"}}]`},
		{capgeocoding.LevelRegion, "state", "Toscana", `[{"lat":"43.3","lon":"11.2","display_name":"Toscana, Italia","addresstype":"state","type":"administrative","class":"boundary","address":{"state":"Toscana","country":"Italia","country_code":"it"}}]`},
		{capgeocoding.LevelMunicipality, "city", "Roma", `[{"lat":"41.8933","lon":"12.4829","display_name":"Roma, Lazio, Italia","addresstype":"city","type":"administrative","class":"place","address":{"city":"Roma","state":"Lazio","country":"Italia","country_code":"it"}}]`},
	}
	for _, tc := range cases {
		var gotFeatureType atomic.Value
		adapter := newLevelAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			gotFeatureType.Store(r.URL.Query().Get("featureType"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.hit))
		})
		if _, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: tc.query, Language: "it", Level: tc.level}); err != nil {
			t.Fatalf("level %s: %v", tc.level, err)
		}
		if got, _ := gotFeatureType.Load().(string); got != tc.featureType {
			t.Fatalf("level %s: featureType = %q, want %q", tc.level, got, tc.featureType)
		}
	}
}

// A continent has no provider featureType; the hint stays empty and the scope
// gate remains the authority.
func TestNominatimContinentLevelSendsNoFeatureType(t *testing.T) {
	var gotFeatureType atomic.Value
	adapter := newLevelAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotFeatureType.Store(r.URL.Query().Get("featureType"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"lat":"48.6909","lon":"9.1406","display_name":"Europa","addresstype":"continent","type":"continent","class":"place","address":{"continent":"Europa"}}]`))
	})
	if _, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Europa", Language: "it", Level: capgeocoding.LevelContinent}); err != nil {
		t.Fatalf("continent lookup: %v", err)
	}
	if got, _ := gotFeatureType.Load().(string); got != "" {
		t.Fatalf("featureType = %q, want no hint", got)
	}
}

// Cached answers are level-scoped: a country hit must not satisfy a later
// municipality lookup for the same name, otherwise the city map inherits a
// country coordinate from the cache.
func TestNominatimCacheIsScopedByLevel(t *testing.T) {
	var calls atomic.Int32
	adapter := newLevelAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("featureType") == "country" {
			_, _ = w.Write([]byte(`[{"lat":"42.6384","lon":"12.6743","name":"Italia","display_name":"Italia","addresstype":"country","type":"administrative","class":"boundary","address":{"country":"Italia","country_code":"it"}}]`))
			return
		}
		// A same-name municipality, so the level-agnostic lookup stays a real
		// candidate under the name-match rule while remaining a DIFFERENT place
		// than the country answer.
		_, _ = w.Write([]byte(`[{"lat":"41.8933","lon":"12.4829","name":"Italia","display_name":"Italia, Lazio, Italia","addresstype":"municipality","type":"administrative","class":"place","address":{"city":"Italia","state":"Lazio","country":"Italia","country_code":"it"}}]`))
	})
	if _, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Italia", Language: "it", Level: capgeocoding.LevelCountry}); err != nil {
		t.Fatalf("country lookup: %v", err)
	}
	// Same name, no level: must not be served the cached country answer's key.
	if _, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Italia", Language: "it"}); err != nil {
		t.Fatalf("level-agnostic lookup: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (one per level)", got)
	}
}
