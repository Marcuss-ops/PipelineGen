package geocoding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	capgeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
)

// TestNominatimCarriesCountryCodeAndUsesItAsContext pins the fix for the
// observed "La Toscana" mis-resolution: without context the Venezuelan town
// ranked first; a grounded Italian country code must select the Italian hit
// and the returned Result must carry the addressed country for later lookups.
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
