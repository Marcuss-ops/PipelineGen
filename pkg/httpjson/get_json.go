// Package httpjson — typed HTTP GET helpers for JSON + raw bytes.
//
// Leaf package (godlike/06 SSOT one-owner-per-fact). Provides fail-closed
// GetJSON[T] + GetBytes with the typed-error contract documented in
// AGENTS.md godlike/07.
//
// Conventions:
//   - godlike/06 SSOT: pkg/httpjson is the canonical owner of the
//     "HTTP GET single-shot request + decode" pattern.
//   - godlike/07 fail-closed: every error path is typed. Never silent.
//   - AGENTS.md Pattern 0 (port abstraction) is intentionally NOT used
//     here — the helper takes the typed *http.Client the caller already
//     owns; no implicit transport injection.
//
// B4 (PR-IMAGES-AI-VS-NORMAL-PLAN, July 2026):
//   - Targets storage_search.go's 9 inline http.NewRequest + Do +
//     ReadAll copies. Pre-B4: 9 copies, ~180 LoC of boilerplate. Post-B4:
//     9 single-call sites, exit-gate rg "http.NewRequest" storage_search
//     → 0.
package httpjson

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ── Options ────────────────────────────────────────────────────────────

// Options configures one HTTP request. All fields are zero-value safe.
//
// Headers      extra request headers (set on the request; User-Agent is
//
//	configured separately so callers don't fight for the key)
//
// UserAgent    sets User-Agent; empty means omit the header
// Timeout      > 0 wraps ctx with context.WithTimeout
// MaxBodyBytes > 0 caps ReadAll via io.LimitReader (0 = no cap, matches
//
//	io.ReadAll behaviour for sites that read the full body)
type Options struct {
	Headers      map[string]string
	UserAgent    string
	Timeout      time.Duration
	MaxBodyBytes int64
}

// ── Sentinels (godlike/07 typed-error contract) ────────────────────────

var (
	ErrClientRequired = errors.New("httpjson: client required")
	ErrRequestFailed  = errors.New("httpjson: request failed")
	ErrNon200         = errors.New("httpjson: non-200 status code")
	ErrDecodeFailed   = errors.New("httpjson: decode failed")
)

// ── StatusError envelope (typed-data for errors.As dispatch) ──────────

// StatusError carries URL + status code + body preview for a non-2xx
// response. It implements the Is(target error) bool method so the chain
// satisfies errors.Is(err, ErrNon200), AND it is the concrete type
// recovered by errors.As(err, &se) so callers can dispatch on the
// actual status code (404 / 429 / 5xx).
//
// Typical usage:
//
//	if errors.Is(err, ErrNon200) {
//	    var se *httpjson.StatusError
//	    if errors.As(err, &se) {
//	        switch {
//	        case se.StatusCode == http.StatusNotFound:                // 404
//	        case se.StatusCode == http.StatusTooManyRequests ||
//	             se.StatusCode >= 500:                                  // 429 / 5xx
//	        default:                                                    // other 4xx
//	        }
//	    }
//	}
type StatusError struct {
	URL        string
	StatusCode int
	Body       []byte // preview, capped at 4 KiB inside GetBytes
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("httpjson: non-200 status %d for %q (preview=%d bytes)",
		e.StatusCode, e.URL, len(e.Body))
}

// Is makes the chain satisfy errors.Is(err, ErrNon200) on top of the
// concrete *StatusError type recovered by errors.As.
func (e *StatusError) Is(target error) bool {
	return target == ErrNon200
}

// RetryAfterDuration lets the shared retry engine honor provider backpressure
// without teaching each caller how to parse HTTP Retry-After headers.
func (e *StatusError) RetryAfterDuration() time.Duration {
	if e == nil || e.RetryAfter < 0 {
		return 0
	}
	return e.RetryAfter
}

// ── GetBytes ───────────────────────────────────────────────────────────

// GetBytes issues a single GET request and returns the payload.
//
// Errors:
//   - ErrClientRequired   client is nil
//   - ErrRequestFailed    transport error / ctx cancelled / ctx deadline
//     / request creation failure
//   - ErrNon200           response status < 200 or >= 300 (wraps a
//     *StatusError so errors.As recovers status,
//     URL, and body preview)
//
// IO: reads up to opts.MaxBodyBytes via io.LimitReader. 0 = no cap
// (byte-for-byte behaviour with pre-B4 io.ReadAll on sites that did
// not enforce a size cap).
func GetBytes(ctx context.Context, client *http.Client, targetURL string, opts *Options) ([]byte, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: url=%s", ErrClientRequired, targetURL)
	}
	if opts == nil {
		opts = &Options{}
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v (url=%s)", ErrRequestFailed, err, targetURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %v (url=%s)", ErrRequestFailed, err, targetURL)
	}
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: do: %v (url=%s)", ErrRequestFailed, err, targetURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &StatusError{
			URL:        targetURL,
			StatusCode: resp.StatusCode,
			Body:       preview,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if opts.MaxBodyBytes > 0 {
		return io.ReadAll(io.LimitReader(resp.Body, opts.MaxBodyBytes))
	}
	return io.ReadAll(resp.Body)
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if wait := time.Until(when); wait > 0 {
		return wait
	}
	return 0
}

// ── GetJSON ────────────────────────────────────────────────────────────

// GetJSON issues a GET and unmarshals the JSON payload into T.
//
// Errors:
//   - ErrClientRequired   nil client
//   - ErrRequestFailed    transport / ctx / request creation failure
//   - ErrNon200           non-2xx (wraps *StatusError, accessible via
//     errors.As so caller can dispatch on status)
//   - ErrDecodeFailed     200 + malformed JSON (wraps the decoder
//     error with the URL context)
//
// Zero-value of T is returned alongside any error so callers never see
// a half-initialised value.
func GetJSON[T any](ctx context.Context, client *http.Client, targetURL string, opts *Options) (T, error) {
	var zero T
	b, err := GetBytes(ctx, client, targetURL, opts)
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return zero, fmt.Errorf("%w: %v (url=%s)", ErrDecodeFailed, err, targetURL)
	}
	return v, nil
}
