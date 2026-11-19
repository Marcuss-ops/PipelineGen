package geocoding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	capgeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
)

func TestNominatimUsesGroundedStateToRejectNamesake(t *testing.T) {
	hits := []searchHit{
		{PlaceID: "1", Name: "Canterbury", Latitude: "43.337165", Longitude: "-71.565628", DisplayName: "Canterbury, New Hampshire, United States", Address: map[string]string{"state": "New Hampshire", "country": "United States", "country_code": "us"}},
		{PlaceID: "2", Name: "Canterbury", Latitude: "39.768333", Longitude: "-86.158350", DisplayName: "Canterbury, Indianapolis, Indiana, United States", Address: map[string]string{"city": "Indianapolis", "state": "Indiana", "country": "United States", "country_code": "us"}},
	}
	got, _, _, ok := selectCandidate("Canterbury", capgeocoding.Context{CountryCode: "us", Region: "Indiana"}, hits)
	if !ok || got.PlaceID.String() != "2" {
		t.Fatalf("grounded Indiana context selected place %q (ok=%t), want the Indiana candidate 2", got.PlaceID, ok)
	}
	if _, _, _, ok := selectCandidate("Canterbury", capgeocoding.Context{CountryCode: "us", Region: "Indiana"}, hits[:1]); ok {
		t.Fatal("accepted Canterbury, New Hampshire in an Indiana scene")
	}
}

// TestNominatimCarriesCountryCodeAndUsesItAsContext pins the fix for the
// observed "La Toscana" mis-resolution: without context the Venezuelan town
// ranked first; a grounded Italian country code must select the Italian hit
// and the returned Result must carry the addressed country for later lookups.
// TestNominatimRefusesLoneForeignNamesake pins the Pavia/Sweden incident: the
// grounded scene was Italy+Lombardy, the provider returned exactly one hit in
// Sweden, and the scoring pass accepted it because nothing contradicted the
// country. Grounded country context is a hard constraint, not a bonus.
// TestNominatimPrefersCityOverSameNameCounty pins the Pavia/Lombardia case:
// the provider returns the county boundary first and the city second. Both
// clamp at 1.00, so provider order used to win and the ambiguity rule refused
// the place entirely (no map pin). Equal scores must prefer the city.
func TestNominatimPrefersCityOverSameNameCounty(t *testing.T) {
	hits := []searchHit{
		{PlaceID: "76412187", Name: "Pavia", Latitude: "45.0368546", Longitude: "9.1378251", DisplayName: "Pavia, Lombardia, Italia", Addresstype: "county", Importance: 0.72, Address: map[string]string{"county": "Pavia", "state": "Lombardia", "country": "Italia", "country_code": "it"}},
		{PlaceID: "76412206", Name: "Pavia", Latitude: "45.1860043", Longitude: "9.1546375", DisplayName: "Pavia, Lombardia, 27100, Italia", Addresstype: "city", Importance: 0.55, Address: map[string]string{"city": "Pavia", "state": "Lombardia", "country": "Italia", "country_code": "it"}},
	}
	got, _, rationale, ok := selectCandidate("Pavia", capgeocoding.Context{Country: "Italia", CountryCode: "it", Region: "Lombardia"}, hits)
	if !ok {
		t.Fatalf("refused a city with an exact name match (%s)", rationale)
	}
	if got.Addresstype != "city" {
		t.Fatalf("selected %q Pavia (%s), want the city boundary", got.Addresstype, rationale)
	}
}

func TestNominatimRefusesLoneForeignNamesake(t *testing.T) {
	hits := []searchHit{{
		PlaceID: "421948660", Name: "Pavia",
		Latitude: "59.3477835", Longitude: "18.0308057",
		DisplayName: "Pavia, Stoccolma, Svezia",
		Address:     map[string]string{"country": "Svezia", "country_code": "se"},
	}}
	if _, _, _, ok := selectCandidate("Pavia", capgeocoding.Context{Country: "Italia", CountryCode: "it", Region: "Lombardia"}, hits); ok {
		t.Fatal("accepted the Swedish Pavia in a grounded Italian scene")
	}
}

// TestNominatimNarrowsProviderByGroundedCountry proves the adapter sends the
// grounded code to the provider (countrycodes=it) so foreign namesakes never
// enter the candidate set, and that a contradicting cached answer is healed by
// one fresh lookup instead of being returned forever.
func TestNominatimNarrowsProviderByGroundedCountryAndHealsCache(t *testing.T) {
	var mu sync.Mutex
	var requestedCountryCodes string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		requestedCountryCodes = r.URL.Query().Get("countrycodes")
		mu.Unlock()
		_, _ = w.Write([]byte(`[{"place_id":2,"lat":"45.1847","lon":"9.1582","name":"Pavia","display_name":"Pavia, Lombardia, Italia","addresstype":"city","address":{"city":"Pavia","state":"Lombardia","country":"Italia","country_code":"it"}}]`))
	}))
	defer server.Close()
	adapter, err := NewNominatim(NominatimConfig{
		BaseURL: server.URL, UserAgent: "PipelineGen-test", CacheDir: t.TempDir(),
		HTTPClient: server.Client(), MinimumInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := capgeocoding.Request{
		Query: "Pavia", Language: "it",
		Context: capgeocoding.Context{Country: "Italia", CountryCode: "it", Region: "Lombardia"},
	}
	// Reproduce the poisoned cache: the same context key already holds the
	// Swedish answer, so a naive cache read would keep it.
	if err := writeCached(adapter.cachePath(request), capgeocoding.Result{
		Latitude: 59.3477835, Longitude: 18.0308057, DisplayName: "Pavia, Stoccolma, Svezia",
		Country: "Svezia", CountryCode: "se", Provider: "nominatim", Score: 0.5,
	}, request.Context); err != nil {
		t.Fatal(err)
	}

	result, err := adapter.Geocode(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.CountryCode != "it" || result.PlaceID != "2" {
		t.Fatalf("healed result = %+v, want the Italian Pavia", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider calls = %d, want the contradicting cache to be refreshed once", calls)
	}
	if requestedCountryCodes != "it" {
		t.Fatalf("countrycodes sent to provider = %q, want it", requestedCountryCodes)
	}
}

func TestNominatimCarriesCountryCodeAndUsesItAsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"place_id":1,"lat":"9.8035041","lon":"-63.3290673","name":"La Toscana","display_name":"La Toscana, Monagas, Venezuela","importance":0.9,"address":{"town":"La Toscana","state":"Monagas","country":"Venezuela","country_code":"ve"}},
			{"place_id":2,"lat":"42.0385912","lon":"11.9167771","name":"La Toscana","display_name":"La Toscana, Santa Marinella, Lazio, Italia","importance":0.4,"address":{"beach":"La Toscana","state":"Lazio","country":"Italia","country_code":"it"}}
		]`))
	}))
	defer server.Close()
	adapter, err := NewNominatim(NominatimConfig{
		BaseURL: server.URL, UserAgent: "PipelineGen-test", CacheDir: t.TempDir(),
		HTTPClient: server.Client(), MinimumInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Geocode(context.Background(), capgeocoding.Request{
		Query: "La Toscana", Language: "it",
		Context: capgeocoding.Context{Country: "Italia", CountryCode: "it"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PlaceID != "2" {
		t.Fatalf("country-code context selected place %q, want the Italian hit 2: %+v", result.PlaceID, result)
	}
	if result.Country != "Italia" || result.CountryCode != "it" {
		t.Fatalf("result country provenance = (%q,%q), want (Italia,it)", result.Country, result.CountryCode)
	}
}
