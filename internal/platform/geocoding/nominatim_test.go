package geocoding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		if got := r.URL.Query().Get("limit"); got != "1" {
			t.Errorf("limit = %q", got)
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
	var cached capgeocoding.Result
	data, err := os.ReadFile(filepath.Join(cacheDir, entries[0].Name()))
	if err != nil || json.Unmarshal(data, &cached) != nil || cached != (capgeocoding.Result{Latitude: -23.550520, Longitude: -46.633308, DisplayName: "São Paulo, Brasil"}) {
		t.Fatalf("invalid cached result %+v, err=%v", cached, err)
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
