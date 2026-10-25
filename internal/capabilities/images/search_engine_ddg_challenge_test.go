package images

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/duckduckgo"
	"go.uber.org/zap"
)

func TestSearchDDGWideManyChallengeTripsCooldown(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"challenge":{"cc":"sre","e":0}}`)),
			Request:    req,
		}, nil
	})}
	breaker := duckduckgo.NewBreaker(time.Hour)
	service := &ImageStorageService{client: client, log: zap.NewNop(), ddgBreaker: breaker}

	if got := service.searchDDGWideMany(context.Background(), "Alberto Garlasco", 8); len(got) != 0 {
		t.Fatalf("challenged search returned %v, want no candidates", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (challenge must not be retried)", requests)
	}
	if !breaker.Open() {
		t.Fatal("challenge did not trip the cooldown breaker")
	}

	// The second composition must not pay another round trip while the
	// cooldown is active.
	if got := service.searchDDGWideMany(context.Background(), "Sempio", 8); len(got) != 0 {
		t.Fatalf("cooldown search returned %v, want no candidates", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (cooldown must short-circuit the next query)", requests)
	}
}

func TestSearchDDGWideManyForbiddenBodyTripsCooldown(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("If this error persists, please let us know: ops@duckduckgo.com")),
			Request:    req,
		}, nil
	})}
	breaker := duckduckgo.NewBreaker(time.Hour)
	service := &ImageStorageService{client: client, log: zap.NewNop(), ddgBreaker: breaker}

	_ = service.searchDDGWideMany(context.Background(), "Garlasco", 4)
	if !breaker.Open() {
		t.Fatal("403 did not trip the cooldown breaker")
	}
}

// TestLiveDDGChallengeTripsCooldown is the opt-in acceptance probe for a real
// IP. It asserts the two honest outcomes: DDG answers and returns candidates,
// or it refuses and the cooldown opens within a single round trip. An empty
// result with a closed breaker is the regression this probe exists to catch,
// because that is indistinguishable from "the subject has no images".
func TestLiveDDGChallengeTripsCooldown(t *testing.T) {
	if os.Getenv("DDG_LIVE_PROBE") != "1" {
		t.Skip("set DDG_LIVE_PROBE=1 to run the live challenge probe")
	}
	breaker := duckduckgo.NewBreaker(time.Hour)
	service := &ImageStorageService{client: &http.Client{Timeout: 15 * time.Second}, log: zap.NewNop(), ddgBreaker: breaker}
	started := time.Now()
	got := service.searchDDGWideMany(context.Background(), "Alberto Garlasco", 8)
	elapsed := time.Since(started)
	t.Logf("live DDG: candidates=%d challenged=%v elapsed=%s", len(got), breaker.Open(), elapsed)
	if len(got) == 0 && !breaker.Open() {
		t.Fatal("DDG returned nothing without classifying a challenge: the caller would mistake a block for an empty corpus")
	}
	if len(got) == 0 && elapsed > 5*time.Second {
		t.Fatalf("challenge detection took %s; it must fail fast, not retry", elapsed)
	}
}

func TestSearchDDGWideManyServerErrorDoesNotTripCooldown(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("upstream error")),
			Request:    req,
		}, nil
	})}
	breaker := duckduckgo.NewBreaker(time.Hour)
	service := &ImageStorageService{client: client, log: zap.NewNop(), ddgBreaker: breaker}

	_ = service.searchDDGWideMany(context.Background(), "Garlasco", 4)
	if breaker.Open() {
		t.Fatal("a 5xx is retryable and must not open the anti-bot cooldown")
	}
}
