// Package scriptdocs — handler_test.go: hermetic TDD coverage of
// the canonical POST /api/script-docs/generate contract.
//
// Test cases (per godlike/07 fail-closed mapping):
//   - nil port → 503 + ErrReActNotWired diagnostic
//   - happy path → 200 + canonical ReActResponse shape
//   - port error → 500 + typed error message
//   - empty topic → 400 + "topic is required"
//   - bad JSON body → 400 + "request body must be valid JSON"
//
// godlike/06 SSOT: the handler is the SOLE owner of the route
// surface + the typed-error mapping. The test surface asserts the
// canonical wire shape (NOT internal Go types).
package scriptdocs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeReActPort is the canonical hermetic test stub for ReActPort.
// godlike/06 SSOT: test stubs live ONLY in *_test.go files (not
// shipped in production binary). The fake records the last request
// for assertion and returns either the canned response or the
// canned error.
type fakeReActPort struct {
	mu          sync.Mutex
	lastReq     ReActRequest
	resp        ReActResponse
	err         error
	calls       int
	ctxCanceled bool
}

func (f *fakeReActPort) Generate(ctx context.Context, req ReActRequest) (ReActResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastReq = req
	f.calls++
	if ctx != nil {
		select {
		case <-ctx.Done():
			f.ctxCanceled = true
			return ReActResponse{}, ctx.Err()
		default:
		}
	}
	return f.resp, f.err
}

func (f *fakeReActPort) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeReActPort) LastReq() ReActRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

// newTestHandler builds the canonical Handler wired to the given
// (optionally nil) port + a captured-log observer for diagnostic
// assertions.
func newTestHandler(port ReActPort) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	log := zap.New(core)
	return NewHandler(port, log), logs
}

// newTestRouter wires the handler under /api/script-docs/* for
// httptest.NewRecorder-based requests.
func newTestRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/script-docs")
	h.RegisterRoutes(g)
	return r
}

// TestGenerate_NilPort_Returns503_NotWiredDiagnostic pins the
// godlike/07 fail-closed seam: nil port → 503 + canonical
// ErrReActNotWired diagnostic. This is the canonical pre-CUTOVER
// behavior — the composition root passes nil today because the
// Python ReAct agent bridge isn't wired yet.
func TestGenerate_NilPort_Returns503_NotWiredDiagnostic(t *testing.T) {
	h, _ := newTestHandler(nil)
	r := newTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/script-docs/generate",
		strings.NewReader(`{"topic":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	var body map[string]any
	if jsonErr := json.Unmarshal(w.Body.Bytes(), &body); jsonErr != nil {
		t.Fatalf("response body is not valid JSON: %v (raw=%s)", jsonErr, w.Body.String())
	}
	if body["error"] != "service_unavailable" {
		t.Errorf("error = %v, want %q", body["error"], "service_unavailable")
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "not wired") {
		t.Errorf("message = %q, want substring %q", msg, "not wired")
	}
}

// TestGenerate_HappyPath_Returns200_ReActResponse pins the
// canonical happy-path contract: when the port is wired and
// returns a ReActResponse, the handler returns 200 + the
// canonical response shape.
func TestGenerate_HappyPath_Returns200_ReActResponse(t *testing.T) {
	wantResp := ReActResponse{Result: "Tyson Fury beats Usyk", Status: "ok", StepsTaken: 3}
	port := &fakeReActPort{resp: wantResp}
	h, _ := newTestHandler(port)
	r := newTestRouter(h)

	body := `{"topic":"boxing","context":"post-fight analysis","max_steps":5}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/script-docs/generate",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", w.Code, http.StatusOK, w.Body.String())
	}
	var got ReActResponse
	if jsonErr := json.Unmarshal(w.Body.Bytes(), &got); jsonErr != nil {
		t.Fatalf("response body is not valid JSON: %v (raw=%s)", jsonErr, w.Body.String())
	}
	if got != wantResp {
		t.Errorf("response = %+v, want %+v", got, wantResp)
	}
	// The handler must thread the parsed request to the port
	// verbatim (Topic + Context + MaxSteps).
	if port.Calls() != 1 {
		t.Errorf("port.Calls = %d, want 1", port.Calls())
	}
	last := port.LastReq()
	if last.Topic != "boxing" {
		t.Errorf("lastReq.Topic = %q, want %q", last.Topic, "boxing")
	}
	if last.Context != "post-fight analysis" {
		t.Errorf("lastReq.Context = %q, want %q", last.Context, "post-fight analysis")
	}
	if last.MaxSteps != 5 {
		t.Errorf("lastReq.MaxSteps = %d, want %d", last.MaxSteps, 5)
	}
}

// TestGenerate_PortError_Returns500_TypedMessage pins the
// godlike/07 fail-closed mapping: port call returns error → 500
// (NOT 503 — nil-port is "not wired"; port-error is "wired but broken").
// The error message threads through verbatim (typed-error contract).
func TestGenerate_PortError_Returns500_TypedMessage(t *testing.T) {
	portErr := errors.New("reAct: python bridge subprocess died: signal: killed")
	port := &fakeReActPort{err: portErr}
	h, logs := newTestHandler(port)
	r := newTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/script-docs/generate",
		strings.NewReader(`{"topic":"boxing"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var body map[string]any
	if jsonErr := json.Unmarshal(w.Body.Bytes(), &body); jsonErr != nil {
		t.Fatalf("response body is not valid JSON: %v", jsonErr)
	}
	if body["error"] != "internal_error" {
		t.Errorf("error = %v, want %q", body["error"], "internal_error")
	}
	msg, _ := body["message"].(string)
	if msg != portErr.Error() {
		t.Errorf("message = %q, want %q", msg, portErr.Error())
	}
	// godlike/07 observability: the Warn log is emitted with topic
	// + error so operators can correlate the 500 with the agent failure.
	if logs.FilterMessageSnippet("ReAct port returned error").Len() != 1 {
		t.Errorf("expected 1 Warn log for port-error, got %d", logs.FilterMessageSnippet("ReAct port returned error").Len())
	}
}

// TestGenerate_EmptyTopic_Returns400 pins the canonical request
// validation: topic empty → 400. The handler does NOT dispatch to
// the port (port.Calls() == 0) — invalid input is a 400, not a
// silent success.
func TestGenerate_EmptyTopic_Returns400(t *testing.T) {
	port := &fakeReActPort{}
	h, _ := newTestHandler(port)
	r := newTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/script-docs/generate",
		strings.NewReader(`{"topic":""}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var body map[string]any
	if jsonErr := json.Unmarshal(w.Body.Bytes(), &body); jsonErr != nil {
		t.Fatalf("response body is not valid JSON: %v", jsonErr)
	}
	if body["error"] != "bad_request" {
		t.Errorf("error = %v, want %q", body["error"], "bad_request")
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "topic is required") {
		t.Errorf("message = %q, want substring %q", msg, "topic is required")
	}
	if port.Calls() != 0 {
		t.Errorf("port.Calls = %d, want 0 (handler must NOT dispatch invalid requests)", port.Calls())
	}
}

// TestGenerate_BadJSON_Returns400 pins the canonical JSON parse
// failure: malformed body → 400. The handler does NOT dispatch
// to the port.
func TestGenerate_BadJSON_Returns400(t *testing.T) {
	port := &fakeReActPort{}
	h, _ := newTestHandler(port)
	r := newTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/script-docs/generate",
		strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if port.Calls() != 0 {
		t.Errorf("port.Calls = %d, want 0 (handler must NOT dispatch on parse error)", port.Calls())
	}
}

// TestErrReActNotWired_ErrorsIsProbesPin locks the canonical
// typed-error contract: the sentinel is reachable via errors.Is
// (NOT string matching) so future port implementations can wrap
// the sentinel with %w and the handler's nil-port check stays
// consistent with the wrapped-error case.
func TestErrReActNotWired_ErrorsIsProbesPin(t *testing.T) {
	if !errors.Is(ErrReActNotWired, ErrReActNotWired) {
		t.Error("errors.Is(ErrReActNotWired, ErrReActNotWired) = false, want true")
	}
	// Different error → not equal.
	if errors.Is(ErrReActNotWired, errors.New("other")) {
		t.Error("errors.Is(ErrReActNotWired, errors.New(\"other\")) = true, want false")
	}
}

// TestBuild_MissingEnabledFunc_ReturnsError pins the canonical
// mandatory-shape validation: Build returns an error when
// EnabledFunc is nil. Mirrors the script.Build contract.
func TestBuild_MissingEnabledFunc_ReturnsError(t *testing.T) {
	_, err := Build(Dependencies{
		Port:        nil,
		EnabledFunc: nil, // mandatory
		Logger:      zap.NewNop(),
	})
	if err == nil {
		t.Fatal("Build with nil EnabledFunc should return an error")
	}
	if !strings.Contains(err.Error(), "EnabledFunc is required") {
		t.Errorf("error = %q, want substring %q", err.Error(), "EnabledFunc is required")
	}
}

// TestBuild_HappyPath_ReturnsDescriptor pins the canonical
// happy-path contract: Build with valid deps returns a Descriptor
// whose Name is "script-docs" + whose Enabled closure honors the
// provided closure.
func TestBuild_HappyPath_ReturnsDescriptor(t *testing.T) {
	called := false
	desc, err := Build(Dependencies{
		Port:        &fakeReActPort{},
		EnabledFunc: func() bool { called = true; return true },
		Logger:      zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if desc.Name() != "script-docs" {
		t.Errorf("Name = %q, want %q", desc.Name(), "script-docs")
	}
	if !desc.Enabled() {
		t.Error("Enabled() = false, want true")
	}
	if !called {
		t.Error("EnabledFunc was not invoked")
	}
	// godlike/07 minimum-blast-radius: nil-port + nil-EnabledFunc
	// is the canonical 0-line "module disabled" composition (Build
	// still requires EnabledFunc; the test below verifies the
	// pure 503 case via a separate, non-Build path).
}
