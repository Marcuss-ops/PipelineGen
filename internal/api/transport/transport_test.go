package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeUseCase is a deterministic UseCase[In, Out] used to verify the
// five-step pipeline (bind → validate → invoke → map → respond) without
// touching the real composition root.
type fakeUseCase[In any, Out any] struct {
	handle func(ctx context.Context, req In) (Out, error)
}

func (f fakeUseCase[In, Out]) Handle(ctx context.Context, req In) (Out, error) {
	return f.handle(ctx, req)
}

// validRequest is the happy-path JSON body used across tests.
type validRequest struct {
	Name string `json:"name"`
}

func (validRequest) Validate() error { return nil }

// validResponse is the corresponding use-case response.
type validResponse struct {
	Greeting string `json:"greeting"`
}

// setup returns a gin engine with POST /test wired to the supplied
// arguments. We avoid touching internal/api routes directly so the
// transport package stays self-contained.
func setup[In any, Out any](uc UseCase[In, Out], mapper ErrorMapper, req any) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/test", func(c *gin.Context) {
		JSON[In, Out](c, uc, mapper)
	})
	return r
}

// do sends a POST /test with a JSON body and returns the recorder.
func do(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	var b strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		b = *strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(http.MethodPost, "/test", &b)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestJSON_HappyPath(t *testing.T) {
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, r validRequest) (validResponse, error) {
			return validResponse{Greeting: "hello " + r.Name}, nil
		},
	}
	r := setup[validRequest, validResponse](uc, nil, validRequest{Name: "world"})
	w := do(t, r, validRequest{Name: "world"})

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var got validResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Greeting != "hello world" {
		t.Fatalf("greeting: want %q, got %q", "hello world", got.Greeting)
	}
}

// invalidRequest simulates a typed Validate() failure (e.g. business
// rule violation) — must surface as 400, not 500.
type invalidRequest struct {
	Token string `json:"token"`
}

type invalidErr struct{ msg string }

func (e invalidErr) Error() string { return e.msg }

func (invalidRequest) Validate() error { return invalidErr{msg: "token must be non-empty"} }

func TestJSON_ValidateFailure(t *testing.T) {
	// Validate is called BEFORE the use case, so the use case never runs.
	called := false
	uc := fakeUseCase[invalidRequest, validResponse]{
		handle: func(_ context.Context, _ invalidRequest) (validResponse, error) {
			called = true
			return validResponse{}, nil
		},
	}
	r := setup[invalidRequest, validResponse](uc, nil, invalidRequest{Token: ""})
	w := do(t, r, invalidRequest{Token: ""})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("use case should not have been invoked after Validate() failure")
	}
}

func TestJSON_BindFailure(t *testing.T) {
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, _ validRequest) (validResponse, error) {
			t.Fatalf("use case must not run on bind failure")
			return validResponse{}, nil
		},
	}
	r := setup[validRequest, validResponse](uc, nil, nil)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	// Malformed JSON
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestJSON_UseCaseError_NilMapper_FallsBackTo500(t *testing.T) {
	boom := errors.New("boom")
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, _ validRequest) (validResponse, error) {
			return validResponse{}, boom
		},
	}
	r := setup[validRequest, validResponse](uc, nil, validRequest{Name: "x"})
	w := do(t, r, validRequest{Name: "x"})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", w.Code)
	}
}

func TestJSON_UseCaseError_CustomMapper_4xx(t *testing.T) {
	boom := errors.New("not found in db")
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, _ validRequest) (validResponse, error) {
			return validResponse{}, boom
		},
	}
	mapper := func(err error) (int, string) {
		if errors.Is(err, boom) {
			return http.StatusNotFound, "resource gone"
		}
		return 0, ""
	}
	r := setup[validRequest, validResponse](uc, mapper, validRequest{Name: "x"})
	w := do(t, r, validRequest{Name: "x"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "resource gone") {
		t.Fatalf("body: want mapped message, got %s", w.Body.String())
	}
}

func TestJSON_UseCaseError_MapperFallBacks(t *testing.T) {
	boom := errors.New("downstream exploded")
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, _ validRequest) (validResponse, error) {
			return validResponse{}, boom
		},
	}
	// mapper returns status==0 (→ 500) and msg=="" (→ err.Error()).
	// For 500, JSON calls api.InternalError(err) which surfaces err in
	// the wire body, so the original error is still observable.
	mapper := func(error) (int, string) { return 0, "" }
	r := setup[validRequest, validResponse](uc, mapper, validRequest{Name: "x"})
	w := do(t, r, validRequest{Name: "x"})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "downstream exploded") {
		t.Fatalf("body: want err.Error() surfaced by InternalError, got %s", w.Body.String())
	}
}

// TestJSON_UseCaseError_MapperEmptyMsgOn4xx_HidesErrString pins the
// safe-default behaviour: a mapper returning (4xx, "") MUST NOT leak
// err.Error() to the client. JSON falls back to the public "request
// rejected" message instead.
func TestJSON_UseCaseError_MapperEmptyMsgOn4xx_HidesErrString(t *testing.T) {
	boom := errors.New("sqlite: no such column: secret_tokens")
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, _ validRequest) (validResponse, error) {
			return validResponse{}, boom
		},
	}
	mapper := func(error) (int, string) { return http.StatusNotFound, "" }
	r := setup[validRequest, validResponse](uc, mapper, validRequest{Name: "x"})
	w := do(t, r, validRequest{Name: "x"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), boom.Error()) {
		t.Fatalf("body leaked err.Error(): %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "request rejected") {
		t.Fatalf("body: want safe generic message, got %s", w.Body.String())
	}
}

// TestJSON_UseCaseError_MapperCustom5xx pins that custom 5xx mappers
// still route through InternalError (which logs the chain), giving
// ops the full error context.
func TestJSON_UseCaseError_MapperCustom5xx(t *testing.T) {
	boom := errors.New("upstream ollama timeout")
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, _ validRequest) (validResponse, error) {
			return validResponse{}, boom
		},
	}
	mapper := func(error) (int, string) {
		return http.StatusBadGateway, "ai service unavailable"
	}
	r := setup[validRequest, validResponse](uc, mapper, validRequest{Name: "x"})
	w := do(t, r, validRequest{Name: "x"})

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d", w.Code)
	}
	// A 5xx route uses InternalError(c, err), so the body may include
	// err.Error() in addition to the mapped public message. We only
	// assert the status code is preserved.
}

// TestJSON_EmptyBody_BindFailure pins what happens when a handler
// targeting a JSON body receives an entirely empty body — gin returns
// EOF, transport must surface 400.
func TestJSON_EmptyBody_BindFailure(t *testing.T) {
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, _ validRequest) (validResponse, error) {
			t.Fatalf("use case must not run on empty body")
			return validResponse{}, nil
		},
	}
	r := setup[validRequest, validResponse](uc, nil, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestJSON_ZeroValueIn_HitsUseCase ensures that a JSON-empty body that
// parses successfully (all fields are optional) still reaches the use
// case with the zero value of In. This is the contract for handlers
// whose request type has no required fields.
func TestJSON_ZeroValueIn_HitsUseCase(t *testing.T) {
	var receivedName string
	uc := fakeUseCase[validRequest, validResponse]{
		handle: func(_ context.Context, r validRequest) (validResponse, error) {
			receivedName = r.Name
			return validResponse{Greeting: "ok"}, nil
		},
	}
	r := setup[validRequest, validResponse](uc, nil, validRequest{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if receivedName != "" {
		t.Fatalf("use case should receive zero value of Name, got %q", receivedName)
	}
}
