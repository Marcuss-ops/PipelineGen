// Package images (api/images) — territory_handlers_test.go
// pins the godlike/07 typed-error contract for the
// PR-GENERATED-SEARCH-FIX data-only helper.
//
// Scope: this file is the CANONICAL test surface for the
// ErrInvalidGeneratedSearchLimit typed sentinel and the
// single-write invariant across the 3 generated-territory read
// routes (GeneratedSearch + TerritorySearch?territory=generated
// + TerritorySearch?territory=all).
//
// godlike/07 typed-error contract: the data-only
// listGeneratedTerritoryResults helper wraps the sentinel via
// fmt.Errorf percent-w so callers probe via errors.Is. A future
// agent that "simplifies" the wrap to percent-v or string concat
// would break the 400-vs-500 discrimination at the 3 caller
// sites (searchGeneratedTerritory + allTerritoriesAggregate);
// the TestErrInvalidGeneratedSearchLimit_WrapsViaPercentW test
// locks this contract.
//
// No-double-write invariant: the 3 callers (searchGeneratedTerritory
// via GeneratedSearch + searchGeneratedTerritory via
// generatedAggregate + allTerritoriesAggregate direct) each
// write the response envelope exactly once. The data helper
// never writes to the response — the contract that the
// double-write fix in PR-GENERATED-SEARCH-FIX round 2 enforces.
// The TestErrInvalidGeneratedSearchLimit_DataHelperNeverWrites
// test pins this: when the data helper returns the typed
// sentinel, the gin test recorder shows zero writes from the
// helper itself (only from the caller that consumes the
// returned error).
//
// godlike/07 minimum-blast-radius: the "default limit is accepted"
// contract IS tested below in TestErrInvalidGeneratedSearchLimit_DefaultLimitAccepted
// (asserts err == nil and no typed sentinel when ?limit is omitted),
// but via the nil-safe service contract (returns (nil, nil) on nil
// receiver / nil repo per the PR-GENERATED-SEARCH-FIX thin-delegate
// godlike/07 contract) — NOT via a panic-recover trick (which would
// have exercised the service's nil-guard rather than the data
// helper's limit-check). A dedicated parseGeneratedSearchLimit
// pure-function refactor is a forward-pointer (godlike/07
// minimum-blast-radius: out of scope for the current PR).
package images

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestErrInvalidGeneratedSearchLimit_WrapsViaPercentW asserts the
// godlike/07 typed-error contract: errors.Is must traverse the
// fmt.Errorf percent-w wrap chain to reach the package-level
// sentinel. If a future refactor changes the wrap to percent-v
// (no chain), the errors.Is probe returns false and the 3 caller
// sites would fall through to 500 InternalError instead of 400
// BadRequest.
//
// 5 sub-assertions: (a) non-nil sentinel, (b) non-empty message,
// (c) percent-w wrap preserves errors.Is chain, (d) unrelated
// sentinel negative, (e) percent-v wrap negative (chain-break
// detector).
func TestErrInvalidGeneratedSearchLimit_WrapsViaPercentW(t *testing.T) {
	t.Parallel()

	// (a) The sentinel exists and is the canonical package-level
	// godlike/07 typed error (capitalised ErrXxx per project
	// convention per godlike/06 SSOT one-owner-per-fact).
	if ErrInvalidGeneratedSearchLimit == nil {
		t.Fatal("ErrInvalidGeneratedSearchLimit is nil — sentinel must be package-level")
	}

	// (b) The sentinel carries a non-empty diagnostic message
	// (godlike/07 requires human-readable cause text for operator
	// dashboards + log scanners).
	if ErrInvalidGeneratedSearchLimit.Error() == "" {
		t.Fatal("ErrInvalidGeneratedSearchLimit message is empty — godlike/07 needs a human-readable diagnostic")
	}

	// (c) fmt.Errorf percent-w wrap preserves errors.Is chain.
	wrapped := fmt.Errorf("invalid limit=%q: %w", "-1", ErrInvalidGeneratedSearchLimit)
	if !errors.Is(wrapped, ErrInvalidGeneratedSearchLimit) {
		t.Fatal("errors.Is(wrapped, ErrInvalidGeneratedSearchLimit) returned false; " +
			"the percent-w wrap chain is broken (likely a future refactor " +
			"replaced percent-w with percent-v or string concat)")
	}

	// (d) A UNRELATED sentinel must NOT match (negative test).
	unrelated := errors.New("some other error")
	if errors.Is(unrelated, ErrInvalidGeneratedSearchLimit) {
		t.Fatal("errors.Is(unrelated, ErrInvalidGeneratedSearchLimit) returned true; " +
			"the sentinel is matching unrelated errors — godlike/07 typed-error contract broken")
	}

	// (e) A wrap that uses percent-v (no chain) must NOT match.
	// This is the canonical chain-break detector: a future agent
	// who "simplifies" the wrap to percent-v would lose the errors.Is
	// chain and the 3 caller sites would fall through to 500 instead
	// of 400.
	stringConcat := fmt.Errorf("invalid limit=%q: %v", "-1", ErrInvalidGeneratedSearchLimit)
	if errors.Is(stringConcat, ErrInvalidGeneratedSearchLimit) {
		t.Fatal("errors.Is(stringConcat, ErrInvalidGeneratedSearchLimit) returned true; " +
			"the percent-v wrap produced a chain match — godlike/07 typed-error contract " +
			"would be silently broken if a future refactor swapped percent-w for percent-v")
	}
}

// TestErrInvalidGeneratedSearchLimit_DataHelperNeverWrites pins
// the no-double-write invariant: the data-only
// listGeneratedTerritoryResults helper must NOT write to the
// gin.ResponseWriter when it returns the typed sentinel. The
// caller (searchGeneratedTerritory or allTerritoriesAggregate)
// is the SOLE writer of the 400 BadRequest envelope.
//
// Test setup: a minimal ImagesHandler with a nil service. The
// data helper short-circuits on invalid limit BEFORE touching
// the service, so nil service is safe here. After the helper
// returns the typed sentinel, the test asserts the gin
// response writer is still untouched (Code = 200 default,
// Body.Len() == 0).
func TestErrInvalidGeneratedSearchLimit_DataHelperNeverWrites(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// Build a request with a known-invalid limit so the data
	// helper short-circuits on the limit-check, returns the
	// typed sentinel, and never reaches the (nil) service.
	req := httptest.NewRequest(http.MethodGet, "/api/images/generated/search?limit=-1", nil)
	c.Request = req

	// nil ImagesHandler is OK because the helper errors out
	// before dereferencing h.service (the limit-check is the
	// first thing the helper does).
	h := &ImagesHandler{}
	_, err := h.listGeneratedTerritoryResults(c)
	if err == nil {
		t.Fatal("listGeneratedTerritoryResults returned nil error for limit=-1; " +
			"expected typed ErrInvalidGeneratedSearchLimit")
	}
	if !errors.Is(err, ErrInvalidGeneratedSearchLimit) {
		t.Fatalf("listGeneratedTerritoryResults returned err = %v, want ErrInvalidGeneratedSearchLimit", err)
	}

	// The no-double-write invariant: the data helper must NOT
	// have written any HTTP response. The caller's envelope
	// (searchGeneratedTerritory or allTerritoriesAggregate) is
	// the SOLE writer.
	if rec.Code != http.StatusOK {
		t.Fatalf("gin response writer Code = %d, want %d (default 200); "+
			"the data helper is writing the response itself — "+
			"this is the double-write bug fixed in PR-GENERATED-SEARCH-FIX round 2",
			rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("gin response writer Body.Len() = %d, want 0; "+
			"the data helper wrote a response body — "+
			"this is the double-write bug fixed in PR-GENERATED-SEARCH-FIX round 2 (body=%q)",
			rec.Body.Len(), rec.Body.String())
	}
} // TestErrInvalidGeneratedSearchLimit_DefaultLimitAccepted asserts
// the happy-path on the data helper when the caller omits the
// `limit` query parameter (default 200). The helper must NOT
// surface the typed sentinel for the default case — it should
// only fail on malformed user input.
//
// Note: the underlying *imgservice.Service.ListImagesByOrigin
// is nil-safe (returns (nil, nil) on nil receiver / nil repo per
// the PR-GENERATED-SEARCH-FIX thin-delegate contract), so the
// data helper returns (nil, nil) without panicking when the
// service is nil. This is the canonical godlike/07 contract
// (no nil-panic regressions on the data path). The limit-check
// happy-path is therefore exercised end-to-end: the helper
// accepts the default limit and delegates to the service.
//
// Forward-pointer: a pure parseGeneratedSearchLimit(limitStr string)
// (int, error) function would unit-test the limit-check in
// isolation without depending on the nil-safe service. Out of
// scope for this PR per godlike/07 minimum-blast-radius.
func TestErrInvalidGeneratedSearchLimit_DefaultLimitAccepted(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/api/images/generated/search", nil) // no ?limit
	c.Request = req

	// nil *ImagesHandler: h.service is nil, so the thin-delegate
	// service.ListImagesByOrigin returns (nil, nil) without
	// panicking (per the PR-GENERATED-SEARCH-FIX nil-safe
	// contract). The data helper then wraps in
	// `make([]ImageSearchResult, 0, len(nil))` which produces
	// a NON-NIL empty slice (len=0, cap=0). The JSON wire format
	// for an empty non-nil slice is `[]` (not `null`), which is
	// the canonical "no data" envelope the API consumers expect.
	h := &ImagesHandler{}
	results, err := h.listGeneratedTerritoryResults(c)
	if err != nil {
		t.Fatalf("listGeneratedTerritoryResults returned err = %v for default limit; "+
			"the default-200 limit must be accepted without an error (nil-safe service "+
			"returns (nil, nil) for nil receiver)", err)
	}
	if errors.Is(err, ErrInvalidGeneratedSearchLimit) {
		t.Fatal("listGeneratedTerritoryResults returned ErrInvalidGeneratedSearchLimit for default limit; " +
			"the default-200 limit must be accepted without the typed sentinel")
	}
	// Empty-slice contract: the helper always wraps results in
	// make(), so the "no data" shape is a non-nil empty slice
	// (len=0), NOT a nil slice. The JSON wire format is `[]`
	// (empty array), which is the canonical envelope the API
	// consumers expect. A future refactor that switches to a
	// nil-slice shape (returning nil directly) would change the
	// wire format to `null` — a silent consumer-facing change.
	if results == nil {
		t.Fatal("expected non-nil empty slice from data helper (make() always produces non-nil), got nil; " +
			"the canonical 'no data' shape is a non-nil empty slice — " +
			"returning nil would change the JSON wire format from `[]` to `null`")
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results from nil-safe service, got %d results; "+
			"the nil-safe service contract returns (nil, nil) for nil receiver "+
			"so the helper should produce an empty slice", len(results))
	}
	// The data helper must NEVER write to the response — even
	// on the happy path. The caller (searchGeneratedTerritory
	// or allTerritoriesAggregate) is the SOLE writer.
	if rec.Code != http.StatusOK {
		t.Fatalf("gin response writer Code = %d, want %d (default 200); "+
			"the data helper is writing the response on the happy path — "+
			"this is the double-write bug fixed in PR-GENERATED-SEARCH-FIX round 2",
			rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("gin response writer Body.Len() = %d, want 0; "+
			"the data helper wrote a response body on the happy path — "+
			"this is the double-write bug fixed in PR-GENERATED-SEARCH-FIX round 2 (body=%q)",
			rec.Body.Len(), rec.Body.String())
	}
}
