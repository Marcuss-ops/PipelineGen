package duckduckgo

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestDetectBodyRecognisesJSONChallenge(t *testing.T) {
	reason, ok := DetectBody([]byte(`{"challenge":{"cc":"sre","e":0,"gk":"x"}}`))
	if !ok || reason != "challenge sre" {
		t.Fatalf("DetectBody = (%q, %v), want (challenge sre, true)", reason, ok)
	}
}

func TestDetectBodyRecognisesAnomalyModal(t *testing.T) {
	if _, ok := DetectBody([]byte(`<div class="anomaly-modal__title">Unfortunately, bots use DuckDuckGo too.</div>`)); !ok {
		t.Fatal("anomaly modal not detected")
	}
}

func TestDetectBodyRejectsEmptyAndNormalHTML(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("  "), []byte(`<html><body>results</body></html>`), []byte(`{"results":[]}`)} {
		if reason, ok := DetectBody(body); ok {
			t.Fatalf("DetectBody(%q) = (%q, true), want not a challenge", body, reason)
		}
	}
}

func TestAsChallengeClassifiesStatus(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{http.StatusAccepted, true},
		{http.StatusForbidden, true},
		{http.StatusInternalServerError, false},
		{http.StatusTooManyRequests, false},
		{0, false},
	}
	for _, tc := range cases {
		got := AsChallenge(tc.status, []byte("body"), 0) != nil
		if got != tc.want {
			t.Errorf("AsChallenge(%d) challenge=%v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestAsChallengeKeepsRetryAfter(t *testing.T) {
	challenge := AsChallenge(http.StatusAccepted, []byte(`{"challenge":{"cc":"sre"}}`), 30*time.Minute)
	if challenge == nil {
		t.Fatal("expected a challenge")
	}
	if challenge.RetryAfter != 30*time.Minute {
		t.Fatalf("RetryAfter = %v, want 30m", challenge.RetryAfter)
	}
	if !errors.Is(challenge, ErrChallenged) {
		t.Fatal("challenge does not satisfy errors.Is(err, ErrChallenged)")
	}
}

func TestBreakerCooldownExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := NewBreaker(10 * time.Minute)
	b.now = func() time.Time { return now }
	if b.Open() {
		t.Fatal("new breaker is open")
	}
	window := b.Trip(&Challenge{Status: 202, Reason: "challenge sre"})
	if window != 10*time.Minute {
		t.Fatalf("window = %v, want 10m", window)
	}
	if !b.Open() {
		t.Fatal("breaker closed immediately after Trip")
	}
	now = now.Add(9 * time.Minute)
	if !b.Open() {
		t.Fatal("breaker closed before the cooldown elapsed")
	}
	now = now.Add(2 * time.Minute)
	if b.Open() {
		t.Fatal("breaker still open after the cooldown")
	}
}

func TestBreakerHonoursLargerRetryAfter(t *testing.T) {
	b := NewBreaker(10 * time.Minute)
	window := b.Trip(&Challenge{Status: 202, RetryAfter: time.Hour})
	if window != time.Hour {
		t.Fatalf("window = %v, want the upstream Retry-After (1h)", window)
	}
	if !b.Open() {
		t.Fatal("breaker closed despite an hour-long Retry-After")
	}
}

func TestBreakerReset(t *testing.T) {
	b := NewBreaker(time.Hour)
	b.Trip(&Challenge{Status: 403})
	b.Reset()
	if b.Open() {
		t.Fatal("Reset left the breaker open")
	}
	if b.Last() != nil {
		t.Fatal("Reset did not clear the last challenge")
	}
}

func TestNilBreakerIsClosed(t *testing.T) {
	var b *Breaker
	if b.Open() {
		t.Fatal("nil breaker reports open")
	}
	if b.Trip(nil) != 0 {
		t.Fatal("nil breaker reported a cooldown")
	}
}
