// Package duckduckgo owns the one fact every DuckDuckGo client needs to share:
// when the upstream is not answering queries but challenging the caller, no
// amount of retrying helps. The detector classifies the challenge payload and
// the breaker stops a run from paying the same latency once per query.
//
// The package is deliberately network-free. It never issues a request and it
// never pretends a challenge is an empty result: callers receive a typed error
// and decide whether they have a licensed fallback.
package duckduckgo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultCooldown is how long a client stays on the local fallback after the
// upstream answered with an anti-bot challenge. The block is IP- and
// rate-shaped, not permanent (the same host can answer normally minutes
// later), so the cooldown is run-scale: long enough that a burst of
// compositions does not each pay the refusal, short enough that a recovered
// upstream is used again without a restart.
const DefaultCooldown = 3 * time.Minute

// ErrChallenged is the sentinel every challenge satisfies. Callers use
// errors.Is(err, duckduckgo.ErrChallenged) to route to a fallback instead of
// treating the empty candidate list as "nothing exists".
var ErrChallenged = errors.New("duckduckgo: anti-bot challenge")

// Challenge describes one refused request. Status is the HTTP status when a
// response was received (0 for an err returned before a response); Body is
// the short preview used to classify the page.
type Challenge struct {
	Status     int
	Body       []byte
	RetryAfter time.Duration
	Reason     string
}

func (c *Challenge) Error() string {
	if c == nil {
		return ErrChallenged.Error()
	}
	reason := c.Reason
	if reason == "" {
		reason = "challenge"
	}
	if c.Status == 0 {
		return fmt.Sprintf("duckduckgo: %s (no HTTP status)", reason)
	}
	return fmt.Sprintf("duckduckgo: %s (HTTP %d)", reason, c.Status)
}

// Is makes errors.Is(err, ErrChallenged) true for a structured challenge.
func (c *Challenge) Is(target error) bool { return target == ErrChallenged }

// DetectStatus classifies a refused response by status alone. A 202 is DDG's
// anomaly challenge; a 403 is the hard block returned once the challenge is
// considered unresolved. Both are challenges, not transient transport errors.
func DetectStatus(status int) bool {
	return status == http.StatusAccepted || status == http.StatusForbidden
}

// DetectBody recognises the documented challenge payloads:
//   - the JSON envelope {"challenge":{"cc":"sre"|"botnet",...}} served by
//     /i.js and the HTML endpoint;
//   - the anomaly modal embedded in the SPA shell.
func DetectBody(body []byte) (string, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "", false
	}
	if trimmed[0] == '{' {
		var payload struct {
			Challenge struct {
				CC string `json:"cc"`
			} `json:"challenge"`
		}
		if err := json.Unmarshal(trimmed, &payload); err == nil && payload.Challenge.CC != "" {
			return "challenge " + payload.Challenge.CC, true
		}
	}
	lower := strings.ToLower(string(trimmed))
	if strings.Contains(lower, "anomaly-modal") || strings.Contains(lower, "anomaly_modal") {
		return "anomaly modal", true
	}
	return "", false
}

// AsChallenge converts a failed attempt into a *Challenge when the upstream
// refused the caller. It returns nil for transport failures and for plain
// server errors: those remain retryable and must not open the breaker.
func AsChallenge(status int, body []byte, retryAfter time.Duration) *Challenge {
	if status == 0 {
		return nil
	}
	reason, ok := DetectBody(body)
	if !ok && !DetectStatus(status) {
		return nil
	}
	if reason == "" {
		reason = fmt.Sprintf("HTTP %d", status)
	}
	return &Challenge{Status: status, Body: body, RetryAfter: retryAfter, Reason: reason}
}

// Breaker is a process-local cooldown. Once tripped, Open reports true until
// the cooldown elapses, so every query in the window fails fast without a
// network round trip. The zero value is usable and uses DefaultCooldown.
type Breaker struct {
	mu       sync.Mutex
	until    time.Time
	cooldown time.Duration
	now      func() time.Time
	last     *Challenge
}

// NewBreaker returns a breaker with the supplied cooldown. A non-positive
// cooldown falls back to DefaultCooldown.
func NewBreaker(cooldown time.Duration) *Breaker {
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &Breaker{cooldown: cooldown, now: time.Now}
}

// Open reports whether the cooldown is still active.
func (b *Breaker) Open() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.now().Before(b.until)
}

// Remaining is the time left in the cooldown, zero when closed.
func (b *Breaker) Remaining() time.Duration {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	left := b.until.Sub(b.now())
	if left < 0 {
		return 0
	}
	return left
}

// Trip opens the breaker. A challenge carrying Retry-After uses the larger of
// that hint and the configured cooldown, because the upstream is the authority
// on its own block window. It returns the applied duration.
func (b *Breaker) Trip(challenge *Challenge) time.Duration {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	window := b.cooldown
	if challenge != nil && challenge.RetryAfter > window {
		window = challenge.RetryAfter
	}
	b.until = b.now().Add(window)
	b.last = challenge
	return window
}

// Last returns the challenge that most recently opened the breaker.
func (b *Breaker) Last() *Challenge {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last
}

// Reset closes the breaker. Tests use it to isolate cases; production never
// resets early because the upstream decides when the block ends.
func (b *Breaker) Reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.until = time.Time{}
	b.last = nil
}
