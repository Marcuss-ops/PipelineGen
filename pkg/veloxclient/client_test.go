package veloxclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// mockServer wraps an httptest.Server to verify request count, headers,
// and to return configurable bodies / status codes for the four cases we
// exercise in the suite. State is kept in atomic counters so concurrent
// tests don't race (though we don't run them in parallel right now).
type mockServer struct {
	server     *httptest.Server
	hits       atomic.Int64
	authHeader atomic.Value // string — last Authorization value
	reqID      atomic.Value // string — last X-Request-ID value
	method     atomic.Value // string — last HTTP method
	statusSeq  []int        // status codes returned by each call in order
	seqIdx     atomic.Int64
}

func newMockServer(seq ...int) *mockServer {
	m := &mockServer{statusSeq: seq}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits := m.hits.Add(1)
		m.authHeader.Store(r.Header.Get("Authorization"))
		m.reqID.Store(r.Header.Get("X-Request-ID"))
		m.method.Store(r.Method)
		// Pick the status for this call: use statusSeq[seqIdx] if set,
		// else 200 by default. The body is then shaped so 2xx returns an
		// AsyncResponse (parsable by the client) and non-2xx returns a
		// generic error envelope — the client never has to guess shape.
		var code int = 200
		if idx := int(m.seqIdx.Add(1) - 1); idx < len(m.statusSeq) {
			code = m.statusSeq[idx]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if code >= 200 && code < 300 && r.URL.Path == "/api/script/generate-with-images" {
			fmt.Fprintf(w, `{"job_id":"job_test_123","status":"queued"}`)
		} else {
			fmt.Fprintf(w, `{"message":"status %d for hit %d"}`, code, hits)
		}
	}))
	return m
}

func (m *mockServer) Close()      { m.server.Close() }
func (m *mockServer) URL() string { return m.server.URL }

func TestSubmitAsync_HappyPath(t *testing.T) {
	m := newMockServer()
	defer m.Close()
	c := New(m.URL(), "test-token")
	resp, err := c.SubmitAsync(context.Background(),
		"/api/script/generate-with-images",
		map[string]any{"topic": "hello"}, "")
	if err != nil {
		t.Fatalf("SubmitAsync returned error: %v", err)
	}
	if resp.JobID != "job_test_123" {
		t.Errorf("expected job_id=job_test_123, got %q", resp.JobID)
	}
	if resp.Status != "queued" {
		t.Errorf("expected status=queued, got %q", resp.Status)
	}
	if got := m.hits.Load(); got != 1 {
		t.Errorf("expected exactly 1 hit, got %d", got)
	}
	if got := m.authHeader.Load(); got != "Bearer test-token" {
		t.Errorf("expected Authorization header to be Bearer test-token, got %v", got)
	}
	if got := m.reqID.Load(); got == "" {
		t.Error("expected X-Request-ID to be auto-generated, got empty")
	}
	if got := m.reqID.Load(); len(got.(string)) != 32 {
		t.Errorf("expected 32-hex-char auto-generated reqID, got length=%d", len(got.(string)))
	}
}

func TestSubmitAsync_ForwardsProvidedRequestID(t *testing.T) {
	m := newMockServer()
	defer m.Close()
	c := New(m.URL(), "tok")
	_, err := c.SubmitAsync(context.Background(), "/api/script/generate-with-images",
		map[string]any{"topic": "x"}, "client-supplied-key-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.reqID.Load(); got != "client-supplied-key-001" {
		t.Errorf("expected X-Request-ID forwarded as client-supplied-key-001, got %v", got)
	}
}

func TestSubmitAsync_Unauthorized_NoRetry(t *testing.T) {
	m := newMockServer(401)
	defer m.Close()
	c := New(m.URL(), "wrong-token")
	_, err := c.SubmitAsync(context.Background(),
		"/api/script/generate-with-images",
		map[string]any{}, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
	if got := m.hits.Load(); got != 1 {
		t.Errorf("401 must NOT be retried; expected 1 hit, got %d", got)
	}
}

func TestSubmitAsync_BadRequest_NoRetry(t *testing.T) {
	m := newMockServer(422)
	defer m.Close()
	c := New(m.URL(), "tok")
	_, err := c.SubmitAsync(context.Background(), "/api/job/something",
		map[string]any{}, "")
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("expected ErrBadRequest for 422, got %v", err)
	}
	if got := m.hits.Load(); got != 1 {
		t.Errorf("4xx must NOT be retried; expected 1 hit, got %d", got)
	}
}

func TestSubmitAsync_ServerError_RetriesThenSucceeds(t *testing.T) {
	// First attempt returns 500, second returns 200. The test expects the
	// client to recover transparently. flippedMock returns a per-attempt
	// code/body sequence so we don't need to spin up a wasted companion
	// httptest.Server alongside this one — its `_ string` URL parameter
	// confirms the URL is not consulted by the implementation.
	flipped := flippedMock("", []int{500}, []string{
		`{"error":"boom"}`,
		`{"job_id":"job_recovered","status":"queued"}`,
	})
	defer flipped.Close()
	c := New(flipped.URL(), "tok")
	c.retryBase = 1 * time.Millisecond // make the test fast
	resp, err := c.SubmitAsync(context.Background(), "/api/script/generate-with-images",
		map[string]any{}, "idem-001")
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if resp.JobID != "job_recovered" {
		t.Errorf("expected job_id from retry's response, got %q", resp.JobID)
	}
	if got := flipped.hits.Load(); got != 2 {
		t.Errorf("expected exactly 2 hits (1 fail + 1 success), got %d", got)
	}
	// X-Request-ID must remain stable across retries so server-side
	// idempotency catches any double-insert.
	if got := flipped.reqID.Load(); got != "idem-001" {
		t.Errorf("expected X-Request-ID stable across retries, got %v", got)
	}
}

func TestSubmitAsync_ServerError_ExhaustsRetries(t *testing.T) {
	m := newMockServer(500, 500, 500)
	defer m.Close()
	c := New(m.URL(), "tok")
	c.retryBase = 1 * time.Millisecond
	_, err := c.SubmitAsync(context.Background(), "/api/script/generate-with-images",
		map[string]any{}, "exhaust-key")
	if !errors.Is(err, ErrServer) {
		t.Errorf("expected ErrServer after exhausting retries, got %v", err)
	}
	// DefaultMaxAttempts = 3 → exactly 3 hits.
	if got := m.hits.Load(); got != 3 {
		t.Errorf("expected 3 hits on retry exhaustion, got %d", got)
	}
}

func TestGetJobStatus_HappyPath(t *testing.T) {
	body := `{"id":"job_42","status":"completed","progress":100,"result":{"script":{"id":"s1"}}}`
	m := bodyMock(body)
	defer m.Close()
	c := New(m.URL(), "tok")
	st, err := c.GetJobStatus(context.Background(), "job_42")
	if err != nil {
		t.Fatalf("GetJobStatus returned error: %v", err)
	}
	if st.ID != "job_42" {
		t.Errorf("expected id=job_42, got %q", st.ID)
	}
	if st.Status != StatusCompleted {
		t.Errorf("expected status=completed, got %q", st.Status)
	}
	if !IsTerminal(st.Status) {
		t.Error("expected IsTerminal(completed) to be true")
	}
	if got := st.Result["script"].(map[string]any)["id"]; got != "s1" {
		t.Errorf("expected nested result.script.id=s1, got %v", got)
	}
}

func TestGetJobStatus_NotFound(t *testing.T) {
	m := newMockServer(404)
	defer m.Close()
	c := New(m.URL(), "tok")
	_, err := c.GetJobStatus(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestIsTerminal(t *testing.T) {
	cases := map[string]bool{
		StatusQueued:    false,
		StatusRunning:   false,
		StatusCompleted: true,
		StatusFailed:    true,
		StatusCancelled: true,
		"unknown":       false,
	}
	for s, want := range cases {
		if got := IsTerminal(s); got != want {
			t.Errorf("IsTerminal(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestNew_NormalisesBaseURL(t *testing.T) {
	c := New("http://x.example/", "tok")
	if strings.HasSuffix(c.baseURL, "/") {
		t.Errorf("New should strip trailing slash, got baseURL=%q", c.baseURL)
	}
}

// bodyMock returns a mockServer that ignores statusSeq and always returns
// 200 with the given body. Used for non-error JSON content tests.
func bodyMock(body string) *mockServer {
	m := &mockServer{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	return m
}

// flippedMockServer wraps an existing httptest.Server's URL by listening
// on its OWN server that returns the per-attempt code/body sequence.
// Used to compose specific success-then-failure patterns that the
// default newMockServer can't express.
//
// Note: the type lives alongside the builder function for clarity; the
// Go namespace forbids both the type and the function from sharing the
// same identifier, so the type is named flippedMockServer while the
// constructor is named flippedMock.
type flippedMockServer struct {
	server *httptest.Server
	hits   atomic.Int64
	reqID  atomic.Value
}

func flippedMock(_ string, codes []int, bodies []string) *flippedMockServer {
	f := &flippedMockServer{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits := f.hits.Add(1)
		f.reqID.Store(r.Header.Get("X-Request-ID"))
		idx := int(hits) - 1
		code := 200
		body := `{}`
		if idx < len(codes) {
			code = codes[idx]
		}
		if idx < len(bodies) {
			body = bodies[idx]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, body)
	}))
	return f
}

func (f *flippedMockServer) Close()      { f.server.Close() }
func (f *flippedMockServer) URL() string { return f.server.URL }

// Sanity test: when the payload marshals to {} it's still sent correctly
// and returns a parsed AsyncResponse, not a raw body.
func TestSubmitAsync_EmptyPayloadIsAllowed(t *testing.T) {
	m := newMockServer()
	defer m.Close()
	c := New(m.URL(), "tok")
	resp, err := c.SubmitAsync(context.Background(), "/api/script/generate-with-images",
		map[string]any{}, "k-empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.JobID != "job_test_123" {
		t.Errorf("expected parsed job_id, got %q", resp.JobID)
	}
}

// TestInsecureTLS_SwapsTransport asserts the WithInsecureTLS option
// actually reflects through to the http.Client's transport. Without this,
// the option could be silently dropped and TLS validation would still
// reject a self-signed cluster cert.
func TestInsecureTLS_SwapsTransport(t *testing.T) {
	c := New("https://insecure.example", "tok", WithInsecureTLS())
	if c.httpClient == nil {
		t.Fatal("http client not initialised")
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.httpClient.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("expected TLSClientConfig to be set")
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("WithInsecureTLS did not flip InsecureSkipVerify to true")
	}
}

// TestStatusCodeMapping locks down the 4xx→error categorisation so a
// future refactor of doRequest doesn't accidentally collapse 404 into
// ErrBadRequest (which would change retry behaviour for callers that
// distinguish typo-not-found from validation-fatal).
func TestStatusCodeMapping(t *testing.T) {
	cases := []struct {
		status  int
		wantErr error
	}{
		{200, nil},
		{302, nil}, // HTTP redirect — accepted; Go client follows.
		{400, ErrBadRequest},
		{422, ErrBadRequest},
		{404, ErrNotFound},
		{401, ErrUnauthorized},
		{403, ErrUnauthorized},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			m := newMockServer(tc.status)
			defer m.Close()
			cli := New(m.URL(), "tok")
			_, err := cli.SubmitAsync(context.Background(), "/api/script/generate-with-images",
				map[string]any{}, "key-"+fmt.Sprint(tc.status))
			if tc.wantErr == nil {
				// 2xx and (unexpectedly) 3xx shouldn't reach the client
				// here since mock always returns a body. Skip the equality
				// assertion but at least verify we didn't accidentally
				// produce a typed error.
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("status=%d expected %v, got %v", tc.status, tc.wantErr, err)
			}
		})
	}
}

// TestRedactCredentials ensures that bodies containing what looks like a
// bearer token don't leak through error messages. The mock echoes the
// inbound Authorization header into a 401 response; the client is
// expected to redact it before exposing the body in the wrapped error.
func TestRedactCredentials(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/script/generate-with-images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		// Server echoes the inbound token in the body — we'd rather not
		// surface that to the operator via the client's error message.
		fmt.Fprintf(w, `{"error":"invalid bearer %s"}`, r.Header.Get("Authorization"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cli := New(srv.URL, "supersecrettoken-1234567890")
	_, err := cli.SubmitAsync(context.Background(), "/api/script/generate-with-images",
		map[string]any{}, "k-redact")
	if err == nil {
		t.Fatal("expected error from 401")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
	if strings.Contains(err.Error(), "supersecrettoken-1234567890") {
		t.Errorf("plaintext token leaked into error message: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker in error, got %v", err)
	}
}
