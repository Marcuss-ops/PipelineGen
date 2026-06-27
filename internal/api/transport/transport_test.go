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

// ── transport.Request suite ──────────────────────────────────────────────
//
// Mirrors transport.JSON's coverage for handlers that bind path +
// query parameters via a custom Binder[In]. The fake use case and gin
// engine setup is reused; only the route/method/binder change.

// errorEnvelope mirrors apiutil.BadRequest's wire shape (see
// pkg/apiutil/apiutil.go::BadRequest) so Request tests can assert
// against the parsed `Error` field instead of substring-matching the
// JSON-escaped wire body. This decouples the assertion from JSON
// string-escaping (e.g. embedded quotes in `limit="abc"`) and pins
// the envelope shape in case someone swaps the response keys.
type errorEnvelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// decodeErrorBody unmarshals an api.{BadRequest,InternalError,Error}
// envelope into errorEnvelope and sanity-asserts OK==false (the
// invariant for any 4xx/5xx response). Returns the parsed error
// string for substring assertions.
// Fails the test immediately if the body is not a valid envelope or
// ok==true.
func decodeErrorBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, w.Body.String())
	}
	if env.OK {
		t.Fatalf("envelope.OK=true on 4xx/5xx (body=%s)", w.Body.String())
	}
	return env.Error
}

type listReq struct {
	PageParams
	Source string
}

func (listReq) Validate() error { return nil }

func bindListReq(c *gin.Context) (listReq, error) {
	page, err := BindPagination(c)
	if err != nil {
		return listReq{}, err
	}
	return listReq{
		PageParams: page,
		Source:     c.Param("source"),
	}, nil
}

// setupRequest mounts a GET /test/:source route that calls
// transport.Request with the supplied use case, error mapper, and binder.
// Mirrors setup() in contract; chose different path to keep both helpers
// available in the same test binary without route conflicts.
func setupRequest[In any, Out any](uc UseCase[In, Out], mapper ErrorMapper, bind Binder[In]) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test/:source", func(c *gin.Context) {
		Request[In, Out](c, uc, mapper, bind)
	})
	return r
}

func TestRequest_HappyPath(t *testing.T) {
	uc := fakeUseCase[listReq, validResponse]{
		handle: func(_ context.Context, r listReq) (validResponse, error) {
			return validResponse{Greeting: "ok-" + r.Source}, nil
		},
	}
	r := setupRequest[listReq, validResponse](uc, nil, bindListReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/youtube?limit=10&offset=5", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var got validResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Greeting != "ok-youtube" {
		t.Fatalf("greeting: want %q, got %q", "ok-youtube", got.Greeting)
	}
}

// TestRequest_BindFailure_BadPagination exercises the binder error
// path: ?limit=abc should NOT silently default to 0, NOT reach the use
// case, and instead produce 400 with the typed-from-Atoi error
// (limit="abc" is not a valid integer) wrapped by BindPagination.
//
// Asserting on the decoded envelope (api.BadRequest writes
// {"ok":false,"error":"..."}) sidesteps JSON string-escaping that
// would otherwise double-backslash the embedded quotes around "abc".
func TestRequest_BindFailure_BadPagination(t *testing.T) {
	called := false
	uc := fakeUseCase[listReq, validResponse]{
		handle: func(_ context.Context, _ listReq) (validResponse, error) {
			called = true
			return validResponse{}, nil
		},
	}
	r := setupRequest[listReq, validResponse](uc, nil, bindListReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/youtube?limit=abc", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("use case must not run on bind failure")
	}
	msg := decodeErrorBody(t, w)
	wantSubstr := `limit="abc" is not a valid integer`
	if !strings.Contains(msg, wantSubstr) {
		t.Fatalf("error field: want substring %q, got %q", wantSubstr, msg)
	}
}

// TestRequest_BindFailure_LimitOutOfRange asserts that ?limit=10000
// (above MaxPageLimit=500) is REJECTED — silent clamp is the contract
// violation we're guarding against in transport.Request vs naive
// handler that does `limit, _ := strconv.Atoi(...)`.
func TestRequest_BindFailure_LimitOutOfRange(t *testing.T) {
	uc := fakeUseCase[listReq, validResponse]{
		handle: func(_ context.Context, _ listReq) (validResponse, error) { return validResponse{}, nil },
	}
	r := setupRequest[listReq, validResponse](uc, nil, bindListReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/youtube?limit=10000", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	msg := decodeErrorBody(t, w)
	wantSubstr := "limit=10000 above maximum 500"
	if !strings.Contains(msg, wantSubstr) {
		t.Fatalf("error field: want substring %q, got %q", wantSubstr, msg)
	}
}

// TestRequest_UseCaseError_MappedTo404 confirms the mapper path
// works on transport.Request the same way as transport.JSON.
func TestRequest_UseCaseError_MappedTo404(t *testing.T) {
	boom := errors.New("resource gone")
	uc := fakeUseCase[listReq, validResponse]{
		handle: func(_ context.Context, _ listReq) (validResponse, error) {
			return validResponse{}, boom
		},
	}
	mapper := func(err error) (int, string) {
		if errors.Is(err, boom) {
			return http.StatusNotFound, "missing"
		}
		return 0, ""
	}
	r := setupRequest[listReq, validResponse](uc, mapper, bindListReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/youtube?limit=10", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestRequest_ValidateFailure exercises the JSONBound interface on a
// binder-derived request — critical because the binder path skips the
// bindJSON JSON-tag validation entirely and uses Validate() as the only
// catcher.
type invalidListReq struct {
	PageParams
}

func (invalidListReq) Validate() error { return errors.New("invalid-limit") }

func bindInvalidListReq(c *gin.Context) (invalidListReq, error) {
	page, err := BindPagination(c)
	if err != nil {
		return invalidListReq{}, err
	}
	return invalidListReq{PageParams: page}, nil
}

func TestRequest_ValidateFailure(t *testing.T) {
	called := false
	uc := fakeUseCase[invalidListReq, validResponse]{
		handle: func(_ context.Context, _ invalidListReq) (validResponse, error) {
			called = true
			return validResponse{}, nil
		},
	}
	r := setupRequest[invalidListReq, validResponse](uc, nil, bindInvalidListReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/youtube?limit=10", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("use case must not run after Validate() failure")
	}
	msg := decodeErrorBody(t, w)
	if !strings.Contains(msg, "invalid-limit") {
		t.Fatalf("error field: want Validate() error message, got %q", msg)
	}
}

// TestRequest_PathAndQuery_BoundPaginationBounds is the canonical
// integration test that proves the path + query + pagination mixed
// scenario works end-to-end. It mimics the kind of handler that
// Punto 3b will refactor (e.g. clip enumeration: /clips/:source/:id
// with ?limit=10&offset=5&q=foo). The binder combines c.Param (path)
// with BindPagination (query) AND a custom parse for `id` — three
// independent sources of input that all flow through the same
// bind → validate → invoke pipeline.

type clipDetailReq struct {
	PageParams
	Source string
	ID     string
	Q      string
}

func (clipDetailReq) Validate() error { return nil }

func bindClipDetail(c *gin.Context) (clipDetailReq, error) {
	page, err := BindPagination(c)
	if err != nil {
		return clipDetailReq{}, err
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		return clipDetailReq{}, errors.New("id path parameter is required")
	}
	return clipDetailReq{
		PageParams: page,
		Source:     c.Param("source"),
		ID:         id,
		Q:          strings.TrimSpace(c.Query("q")),
	}, nil
}

func setupClipDetail[In any, Out any](uc UseCase[In, Out], mapper ErrorMapper, bind Binder[In]) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/clips/:source/:id", func(c *gin.Context) {
		Request[In, Out](c, uc, mapper, bind)
	})
	return r
}

func TestRequest_PathAndQuery_BoundPaginationBounds(t *testing.T) {
	received := clipDetailReq{}
	uc := fakeUseCase[clipDetailReq, validResponse]{
		handle: func(_ context.Context, r clipDetailReq) (validResponse, error) {
			received = r
			return validResponse{Greeting: "ok"}, nil
		},
	}
	r := setupClipDetail[clipDetailReq, validResponse](uc, nil, bindClipDetail)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/clips/youtube/clip_abc?limit=10&offset=5&q=foo", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if received.Source != "youtube" {
		t.Fatalf("Source: want %q, got %q", "youtube", received.Source)
	}
	if received.ID != "clip_abc" {
		t.Fatalf("ID: want %q, got %q", "clip_abc", received.ID)
	}
	if received.Limit != 10 {
		t.Fatalf("Limit: want 10, got %d", received.Limit)
	}
	if received.Offset != 5 {
		t.Fatalf("Offset: want 5, got %d", received.Offset)
	}
	if received.Q != "foo" {
		t.Fatalf("Q: want %q, got %q", "foo", received.Q)
	}
}

// TestRequest_ClipDetail_MissingIDPathParam covers the binder-level
// failure path: /clips/youtube/ (no :id segment) is rejected with 400
// BEFORE the use case is invoked — confirms the binder error → 400
// mapping flows as expected.
func TestRequest_ClipDetail_MissingIDPathParam(t *testing.T) {
	called := false
	uc := fakeUseCase[clipDetailReq, validResponse]{
		handle: func(_ context.Context, _ clipDetailReq) (validResponse, error) {
			called = true
			return validResponse{}, nil
		},
	}
	r := setupClipDetail[clipDetailReq, validResponse](uc, nil, bindClipDetail)
	w := httptest.NewRecorder()
	// No :id segment — gin won't match the route at all (404 from
	// gin's default no-route), so bindClipDetail never runs.
	req := httptest.NewRequest(http.MethodGet, "/clips/youtube", nil)
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("status: expected not-200, got %d", w.Code)
	}
	if called {
		t.Fatalf("use case must not run when route doesn't match")
	}
}

// ── BindPagination + BindPaginationWithLimits direct tests ───────────────

func TestBindPagination_HappyDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	p, err := BindPagination(c)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.Limit != DefaultPageLimit {
		t.Fatalf("Limit: want %d, got %d", DefaultPageLimit, p.Limit)
	}
	if p.Offset != 0 {
		t.Fatalf("Offset: want 0, got %d", p.Offset)
	}
}

func TestBindPaginationWithLimits_Happy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x?limit=100&offset=20", nil)
	p, err := BindPaginationWithLimits(c, 25, 200)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.Limit != 100 {
		t.Fatalf("Limit: want 100, got %d", p.Limit)
	}
	if p.Offset != 20 {
		t.Fatalf("Offset: want 20, got %d", p.Offset)
	}
}

func TestBindPaginationWithLimits_RejectsBeyondMax(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x?limit=500", nil)
	_, err := BindPaginationWithLimits(c, 25, 200)
	if err == nil {
		t.Fatalf("err: want non-nil when limit=500 exceeds maxLimit=200")
	}
	if !strings.Contains(err.Error(), "above maximum 200") {
		t.Fatalf("err: want 'above maximum 200', got %v", err)
	}
}
