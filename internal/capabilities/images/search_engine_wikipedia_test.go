package images

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func resetWikipediaLaneForTest() {
	wikipediaLane.mu.Lock()
	wikipediaLane.lastSearch = time.Time{}
	wikipediaLane.cooldownUntil = time.Time{}
	wikipediaLane.mu.Unlock()
}

func TestSearchWikipediaHonorsRetryAfterCooldown(t *testing.T) {
	resetWikipediaLaneForTest()
	defer resetWikipediaLaneForTest()
	requests := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"60"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    req,
		}, nil
	})}
	service := &ImageStorageService{client: client, log: zap.NewNop()}

	if got, _ := service.searchWikipedia(context.Background(), "Corrado Augias", "it"); got != "" {
		t.Fatalf("rate-limited query returned %q, want empty", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (429 must not be retried inline)", requests)
	}

	// The next scene must not pay another Wikipedia round trip while the
	// Retry-After window is open: it has to fall back to the other providers.
	if got, _ := service.searchWikipedia(context.Background(), "Roma", "it"); got != "" {
		t.Fatalf("cooling-down query returned %q, want empty", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (cooldown must short-circuit the next query)", requests)
	}
}

// TestWikipediaCooldownIsSharedAcrossServiceInstances pins the production
// failure mode: the composition can hold more than one ImageStorageService,
// and each one used to grant itself a fresh Wikipedia burst allowance. The
// Retry-After window must belong to the process, not to a struct instance.
func TestWikipediaCooldownIsSharedAcrossServiceInstances(t *testing.T) {
	resetWikipediaLaneForTest()
	defer resetWikipediaLaneForTest()

	requests := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"60"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    req,
		}, nil
	})}
	first := &ImageStorageService{client: client, log: zap.NewNop()}
	second := &ImageStorageService{client: client, log: zap.NewNop()}

	if got, _ := first.searchWikipedia(context.Background(), "Corrado Augias", "it"); got != "" {
		t.Fatalf("rate-limited query returned %q, want empty", got)
	}
	if got, _ := second.searchWikipedia(context.Background(), "Roma", "it"); got != "" {
		t.Fatalf("second service cooling-down query returned %q, want empty", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (cooldown must be shared by every service instance)", requests)
	}
}
