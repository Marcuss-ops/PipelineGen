package geocoding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	capgeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
)

func TestNominatimGeocodeCachesValidatedPositiveResult(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("User-Agent"); got != "PipelineGen-geocoder-test/1.0 (mailto:ops@example.test)" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "São Paulo" {
			t.Errorf("query = %q", got)
		}
		if got := r.URL.Query().Get("format"); got != "jsonv2" {
			t.Errorf("format = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit = %q (disambiguation needs to see several candidates)", got)
		}
		if got := r.URL.Query().Get("accept-language"); got != "pt" {
			t.Errorf("language = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"lat":"-23.550520","lon":"-46.633308","display_name":"São Paulo, Brasil"}]`))
	}))
	defer server.Close()

	cacheDir := filepath.Join(t.TempDir(), "cache")
	adapter, err := NewNominatim(NominatimConfig{
		BaseURL: server.URL, UserAgent: "PipelineGen-geocoder-test/1.0 (mailto:ops@example.test)",
		CacheDir: cacheDir, HTTPClient: server.Client(), MinimumInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := capgeocoding.Request{Query: " São   Paulo ", Language: "PT"}
	for i := 0; i < 2; i++ {
		result, err := adapter.Geocode(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.Latitude != -23.550520 || result.Longitude != -46.633308 || result.DisplayName != "São Paulo, Brasil" {
			t.Fatalf("result = %+v", result)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("network calls = %d, want one cache miss only", got)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("cache entries = %v, err=%v", entries, err)
	}
	var cached cacheEntry
	data, err := os.ReadFile(filepath.Join(cacheDir, entries[0].Name()))
	if err != nil || json.Unmarshal(data, &cached) != nil {
		t.Fatalf("invalid cached record: %v", err)
	}
	if cached.Latitude != -23.550520 || cached.Longitude != -46.633308 || cached.DisplayName != "São Paulo, Brasil" {
		t.Fatalf("invalid cached coordinates/display %+v", cached.Result)
	}
	// The cache must record WHY the hit was accepted: provider identity,
	// coordinates and the selection rationale are all auditable from disk.
	if cached.Provider == "" || cached.Rationale == "" || cached.Score <= 0 {
		t.Fatalf("cached selection identity missing: provider=%q score=%v rationale=%q",
			cached.Provider, cached.Score, cached.Rationale)
	}
}

// TestNominatimRefusesAmbiguousNameWithoutContext proves the adapter no
// longer pins the first provider hit for a name several places share: without
// grounded context the lookup is certified-refused (ErrNoResult), so no map
// pin is invented.
func TestNominatimRefusesAmbiguousNameWithoutContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"lat":"48.8566","lon":"2.3522","name":"Paris","display_name":"Paris, France","importance":0.9,"address":{"country":"France","country_code":"fr"}},
			{"lat":"33.6617","lon":"-95.5555","name":"Paris","display_name":"Paris, Texas, United States","importance":0.6,"address":{"country":"United States","country_code":"us"}}
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
	_, err = adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Paris", Language: "en"})
	if !errors.Is(err, capgeocoding.ErrNoResult) {
		t.Fatalf("ambiguous lookup must refuse with ErrNoResult, got %v", err)
	}
}

// TestNominatimResolvesAmbiguityWithGroundedContext proves the same ambiguous
// name resolves to the context-consistent locality once the scene's grounded
// country is supplied, and that the rationale records the decision.
func TestNominatimResolvesAmbiguityWithGroundedContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"place_id":1,"lat":"33.6617","lon":"-95.5555","name":"Paris","display_name":"Paris, Texas, United States","importance":0.6,"address":{"country":"United States","country_code":"us"}},
			{"place_id":2,"lat":"48.8566","lon":"2.3522","name":"Paris","display_name":"Paris, France","importance":0.9,"address":{"country":"France","country_code":"fr"}}
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
		Query: "Paris", Language: "fr", Context: capgeocoding.Context{Country: "France"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Latitude != 48.8566 || result.PlaceID != "2" || !strings.Contains(result.Rationale, "context_decided=true") {
		t.Fatalf("context did not decide the homonym: %+v", result)
	}
}

func TestNominatimPrefersCityBoundaryOverSameNameCounty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"lat":"38.7077507","lon":"-9.1365919","name":"Lisbona","display_name":"Lisbona, Portogallo","category":"boundary","type":"administrative","addresstype":"city","importance":0.7635,"address":{"city":"Lisbona","country":"Portogallo","country_code":"pt"}},
			{"lat":"38.9952469","lon":"-9.1435928","name":"Lisbona","display_name":"Lisbona, Portogallo","category":"boundary","type":"administrative","addresstype":"county","importance":0.5597,"address":{"county":"Lisbona","country":"Portogallo","country_code":"pt"}}
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
		Query: "Lisbona", Language: "it", Context: capgeocoding.Context{Country: "Portogallo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Latitude != 38.7077507 || result.Longitude != -9.1365919 || result.Scope != "city" {
		t.Fatalf("city boundary was not preferred over same-name county: %+v", result)
	}
}

// TestNominatimCacheSeparatesGeographicContext proves one name under two
// grounded contexts is two lookups: the cache never reuses a locality across
// contexts.
func TestNominatimCacheSeparatesGeographicContext(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Query().Get("accept-language") {
		case "fr":
			_, _ = w.Write([]byte(`[{"lat":"48.8566","lon":"2.3522","name":"Paris","display_name":"Paris, France","address":{"country":"France","country_code":"fr"}}]`))
		default:
			_, _ = w.Write([]byte(`[{"lat":"33.6617","lon":"-95.5555","name":"Paris","display_name":"Paris, Texas, United States","address":{"country":"United States","country_code":"us"}}]`))
		}
	}))
	defer server.Close()
	adapter, err := NewNominatim(NominatimConfig{
		BaseURL: server.URL, UserAgent: "PipelineGen-test", CacheDir: t.TempDir(),
		HTTPClient: server.Client(), MinimumInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	france, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Paris", Language: "fr", Context: capgeocoding.Context{Country: "France"}})
	if err != nil {
		t.Fatal(err)
	}
	usa, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Paris", Language: "en", Context: capgeocoding.Context{Country: "United States"}})
	if err != nil {
		t.Fatal(err)
	}
	if france.Latitude == usa.Latitude {
		t.Fatalf("one cached answer served two contexts: %v == %v", france.Latitude, usa.Latitude)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("network calls = %d, want one per context", got)
	}
	// Replays stay cached per context.
	if _, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Paris", Language: "fr", Context: capgeocoding.Context{Country: "France"}}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("context replay hit the network: %d calls", got)
	}
}

func TestNominatimRequiresIdentifyingUserAgentAndDurableCache(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  NominatimConfig
	}{
		{"user agent", NominatimConfig{CacheDir: t.TempDir()}},
		{"cache directory", NominatimConfig{UserAgent: "PipelineGen/test"}},
		{"invalid endpoint", NominatimConfig{BaseURL: "file:///etc/passwd", UserAgent: "PipelineGen/test", CacheDir: t.TempDir()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewNominatim(tc.cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestNominatimRejectsInvalidGeocodingResultsAndHTTPErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{"out of range", `[{"lat":"91","lon":"0"}]`, http.StatusOK},
		{"malformed", `not-json`, http.StatusOK},
		{"server error", `temporarily unavailable`, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			adapter, err := NewNominatim(NominatimConfig{
				BaseURL: server.URL, UserAgent: "PipelineGen-test", CacheDir: t.TempDir(),
				HTTPClient: server.Client(), MinimumInterval: time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "Paris"}); err == nil {
				t.Fatal("expected fail-closed geocoding error")
			}
		})
	}
}

func TestNominatimPacesPublicRequestsAndHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"lat":"1","lon":"2"}]`))
	}))
	defer server.Close()
	adapter, err := NewNominatim(NominatimConfig{
		BaseURL: server.URL, UserAgent: "PipelineGen-test", CacheDir: t.TempDir(),
		HTTPClient: server.Client(), MinimumInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Geocode(context.Background(), capgeocoding.Request{Query: "A"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Geocode(ctx, capgeocoding.Request{Query: "B"}); err == nil {
		t.Fatal("canceled context must not issue a request")
	}
}
