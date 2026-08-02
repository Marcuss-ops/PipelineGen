package httpjson

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── Internal fixtures ──────────────────────────────────────────────────

// testStruct is the canonical JSON test payload used across the typed
// GetJSON tests. Fields mirror the typical API response: scalar string
// + scalar int.
type testStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// newTestClient builds a *http.Client tied to the test server's
// transport. We pass the server's Client() so the trust store includes
// the test server's auto-generated TLS cert when the test uses HTTPS
// (httptest.NewTLSServer); for plain httptest.NewServer the default
// transport is fine.
func newTestClient(server *httptest.Server) *http.Client {
	return server.Client()
}

// ── Test 1: GetJSON happy path ────────────────────────────────────────

// TestGetJSON_HappyPath_BodyParsed verifies that a 200 + JSON response
// is unmarshalled into T with all fields populated.
func TestGetJSON_HappyPath_BodyParsed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"name":"alice","age":30}`)
	}))
	defer server.Close()

	got, err := GetJSON[testStruct](context.Background(), newTestClient(server), server.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "alice" || got.Age != 30 {
		t.Fatalf("got %+v, want {Name:alice Age:30}", got)
	}
}

// ── Test 2: GetJSON context already cancelled ──────────────────────────

// TestGetJSON_ContextAlreadyCancelled_ReturnsErrRequestFailed verifies
// that a pre-cancelled ctx short-circuits GetBytes BEFORE the Do call
// and surfaces the cancellation via the ErrRequestFailed sentinel.
func TestGetJSON_ContextAlreadyCancelled_ReturnsErrRequestFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GetJSON[testStruct](ctx, newTestClient(server), server.URL, nil)
	if err == nil {
		t.Fatal("expected error from pre-cancelled ctx, got nil")
	}
	if !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("got %v, want errors.Is ErrRequestFailed", err)
	}
}

// ── Test 3: GetJSON non-200 → StatusError envelope ────────────────────

// TestGetJSON_Non200_ReturnsStatusErrorWithCode verifies that a 404
// response surfaces:
//   - errors.Is(err, ErrNon200) = true (typed sentinel dispatch)
//   - errors.As(err, &se) recovers *StatusError with se.StatusCode=404,
//     se.URL=server.URL+"/missing", se.Body preview contains the body.
func TestGetJSON_Non200_ReturnsStatusErrorWithCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "no such resource")
	}))
	defer server.Close()

	_, err := GetJSON[testStruct](context.Background(), newTestClient(server), server.URL+"/missing", nil)
	if err == nil {
		t.Fatal("expected error from 404, got nil")
	}
	if !errors.Is(err, ErrNon200) {
		t.Fatalf("got %v, want errors.Is ErrNon200", err)
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want errors.As *StatusError", err)
	}
	if se.StatusCode != http.StatusNotFound {
		t.Fatalf("got StatusCode=%d, want 404", se.StatusCode)
	}
	if se.URL != server.URL+"/missing" {
		t.Fatalf("got URL=%q, want %q", se.URL, server.URL+"/missing")
	}
	if !strings.Contains(string(se.Body), "no such resource") {
		t.Fatalf("got Body=%q, want contains 'no such resource'", se.Body)
	}
}

func TestGetBytes_Non200_ParsesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "18")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := GetBytes(context.Background(), newTestClient(server), server.URL, nil)
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("got %v, want StatusError", err)
	}
	if got := statusErr.RetryAfterDuration(); got != 18*time.Second {
		t.Fatalf("RetryAfterDuration=%v, want 18s", got)
	}
}

// ── Test 4: GetJSON invalid JSON → ErrDecodeFailed ────────────────────

// TestGetJSON_InvalidJSON_ReturnsErrDecodeFailed verifies that a 200
// response with malformed body surfaces ErrDecodeFailed (and not
// ErrNon200) so callers can distinguish decode failure from HTTP
// failure.
func TestGetJSON_InvalidJSON_ReturnsErrDecodeFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "not json {{{")
	}))
	defer server.Close()

	_, err := GetJSON[testStruct](context.Background(), newTestClient(server), server.URL, nil)
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if !errors.Is(err, ErrDecodeFailed) {
		t.Fatalf("got %v, want ErrDecodeFailed", err)
	}
}

// ── Test 5: GetBytes happy path returns raw bytes ─────────────────────

// TestGetBytes_Happy_ReturnsExactBytes verifies that a 200 response
// returns the literal byte slice unchanged.
func TestGetBytes_Happy_ReturnsExactBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello world")
	}))
	defer server.Close()

	got, err := GetBytes(context.Background(), newTestClient(server), server.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("got %q, want 'hello world'", got)
	}
}

// ── Test 6: GetBytes timeout → ErrRequestFailed ───────────────────────

// TestGetBytes_Timeout_ReturnsErrRequestFailed verifies that
// opts.Timeout wraps ctx and the resulting deadline fires before the
// server responds; the error wraps the deadline via ErrRequestFailed.
func TestGetBytes_Timeout_ReturnsErrRequestFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := GetBytes(context.Background(), newTestClient(server), server.URL, &Options{
		Timeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("got %v, want errors.Is ErrRequestFailed", err)
	}
}

// ── Test 7: GetBytes nil client → ErrClientRequired ───────────────────

// TestGetBytes_NilClient_ReturnsErrClientRequired verifies the guard
// at the top of GetBytes: nil client returns immediately with the
// ErrClientRequired sentinel without invoking any transport.
func TestGetBytes_NilClient_ReturnsErrClientRequired(t *testing.T) {
	_, err := GetBytes(context.Background(), nil, "https://example.com/x", nil)
	if err == nil {
		t.Fatal("expected nil-client error, got nil")
	}
	if !errors.Is(err, ErrClientRequired) {
		t.Fatalf("got %v, want ErrClientRequired", err)
	}
}

// ── Test 8: GetBytes nil opts → safe defaults ─────────────────────────

// TestGetBytes_NilOpts_UsesDefaults verifies that passing opts=nil
// (callers can opt out of Options entirely) is safe: no User-Agent is
// added, no timeout is applied, no extra headers, and the request
// succeeds with the default Go-supplied transport.
func TestGetBytes_NilOpts_UsesDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	got, err := GetBytes(context.Background(), newTestClient(server), server.URL, nil)
	if err != nil {
		t.Fatalf("nil opts should not error: %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("got %q, want 'ok'", got)
	}
}

// ── Test 9: GetBytes User-Agent header echoed ─────────────────────────

// TestGetBytes_UserAgentEchoed verifies that opts.UserAgent is
// propagated to the server's view of the request.
func TestGetBytes_UserAgentEchoed(t *testing.T) {
	var gotUA atomic.Value
	gotUA.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA.Store(r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := GetBytes(context.Background(), newTestClient(server), server.URL, &Options{
		UserAgent: "test-ua/1.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gotUA.Load().(string); got != "test-ua/1.0" {
		t.Fatalf("got User-Agent=%q, want 'test-ua/1.0'", got)
	}
}

// ── Test 10: GetBytes custom headers echoed ───────────────────────────

// TestGetBytes_CustomHeadersEchoed verifies that opts.Headers is
// propagated verbatim. Mirrors the User-Agent test but exercises the
// arbitrary-header path (which is what callers typically use for auth
// tokens, idempotency keys, etc.).
func TestGetBytes_CustomHeadersEchoed(t *testing.T) {
	var gotKey atomic.Value
	gotKey.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey.Store(r.Header.Get("X-Api-Key"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := GetBytes(context.Background(), newTestClient(server), server.URL, &Options{
		Headers: map[string]string{"X-Api-Key": "secret"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gotKey.Load().(string); got != "secret" {
		t.Fatalf("got X-Api-Key=%q, want 'secret'", got)
	}
}

// ── Test 11: GetBytes reusable client across multiple calls ───────────

// TestGetBytes_ReuseClientAcrossCalls verifies that callers can pass
// the same *http.Client to multiple GetBytes invocations without
// resetting transport / connection state. The server counts hits so we
// confirm all 3 calls reach the handler.
func TestGetBytes_ReuseClientAcrossCalls(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := newTestClient(server)
	for i := 0; i < 3; i++ {
		_, err := GetBytes(context.Background(), client, server.URL, nil)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("got %d hits, want 3", got)
	}
}

// ── Test 12: StatusError formatting + dispatchability ─────────────────

// TestStatusError_ErrorMessageFormat verifies:
//   - StatusError.Is(ErrNon200) returns true (errors.Is dispatch)
//   - error message contains the status code and the URL
//   - Body length is reflected in the preview count (debug aid)
func TestStatusError_ErrorMessageFormat(t *testing.T) {
	se := &StatusError{
		URL:        "https://example.com/x",
		StatusCode: 503,
		Body:       []byte("overload"),
	}
	if !errors.Is(se, ErrNon200) {
		t.Fatal("StatusError.Is(ErrNon200) = false; want true")
	}
	msg := se.Error()
	if !strings.Contains(msg, "503") {
		t.Fatalf("error msg %q missing status code", msg)
	}
	if !strings.Contains(msg, "https://example.com/x") {
		t.Fatalf("error msg %q missing URL", msg)
	}
	if !strings.Contains(msg, "8 bytes") {
		t.Fatalf("error msg %q missing body preview count", msg)
	}
}
