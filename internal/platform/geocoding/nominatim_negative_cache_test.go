package geocoding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	capgeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
)

// A certified no-result is an answer, not an outage: repeating the same query
// inside the retry window must be served locally instead of paying another
// provider round-trip (and another timeout) for it.
func TestNominatimCachesNoResultForTTL(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	adapter, err := NewNominatim(NominatimConfig{
		BaseURL: server.URL, UserAgent: "PipelineGen-test", CacheDir: t.TempDir(),
		HTTPClient: server.Client(), MinimumInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := capgeocoding.Request{Query: "Nowhere", Language: "it"}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := adapter.Geocode(context.Background(), request); !errors.Is(err, capgeocoding.ErrNoResult) {
			t.Fatalf("attempt %d: error = %v, want ErrNoResult", attempt, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (the second lookup must hit the negative cache)", got)
	}

	normalized, err := capgeocoding.NormalizeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	path := adapter.cachePath(normalized)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if !entry.NoResult || entry.CachedAt.IsZero() {
		t.Fatalf("cached entry is not a dated no-result: %+v", entry)
	}

	// Past the TTL the refusal is a miss again: the provider is asked once
	// more so a corrected name is not blocked for the life of the cache.
	entry.CachedAt = time.Now().Add(-negativeCacheTTL - time.Second)
	data, err = json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, data); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Geocode(context.Background(), request); !errors.Is(err, capgeocoding.ErrNoResult) {
		t.Fatalf("expired entry: error = %v, want ErrNoResult", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider calls after expiry = %d, want 2", got)
	}
}
