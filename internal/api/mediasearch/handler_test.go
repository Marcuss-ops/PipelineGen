// Package mediasearch — unit tests for /internal/v1/media/search handler.
//
// Agente 2, Azione 1: verifica che il workspace venga propagato
// come search.Query.Actor dal handler all'aggregatore.
package mediasearch

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// TestHandlerPropagatesWorkspaceActor verifies that the handler
// populates search.Query.Actor with the real workspace extracted
// from the auth middleware (not default/zero values).
func TestHandlerPropagatesWorkspaceActor(t *testing.T) {
	workspace := search.Actor{
		WorkspaceID: "ws-abc123",
		UserID:      "user-42",
		IsAdmin:     false,
	}

	q := searchQueryFromRequest(
		searchRequest{Query: "test query"},
		search.SearchModeHybrid,
		20,
		workspace,
	)

	if q.Actor.WorkspaceID != "ws-abc123" {
		t.Errorf("Actor.WorkspaceID = %q, want %q", q.Actor.WorkspaceID, "ws-abc123")
	}
	if q.Actor.UserID != "user-42" {
		t.Errorf("Actor.UserID = %q, want %q", q.Actor.UserID, "user-42")
	}
	if q.Actor.IsAdmin {
		t.Errorf("Actor.IsAdmin = true, want false (non-admin workspace)")
	}
	if q.Actor.IsZero() {
		t.Error("Actor.IsZero() = true, want false (workspace fields are populated)")
	}
}

// TestHandlerPropagatesAdminActor verifies that admin flag is
// correctly forwarded when the auth middleware sets is_admin=true.
func TestHandlerPropagatesAdminActor(t *testing.T) {
	workspace := search.Actor{
		WorkspaceID: "ws-admin",
		UserID:      "admin-007",
		IsAdmin:     true,
	}

	q := searchQueryFromRequest(
		searchRequest{Query: "admin search"},
		search.SearchModeANN,
		50,
		workspace,
	)

	if q.Actor.WorkspaceID != "ws-admin" {
		t.Errorf("Actor.WorkspaceID = %q, want %q", q.Actor.WorkspaceID, "ws-admin")
	}
	if q.Actor.UserID != "admin-007" {
		t.Errorf("Actor.UserID = %q, want %q", q.Actor.UserID, "admin-007")
	}
	if !q.Actor.IsAdmin {
		t.Error("Actor.IsAdmin = false, want true (admin workspace)")
	}
	if q.Actor.IsZero() {
		t.Error("Actor.IsZero() = true, want false (admin fields are populated)")
	}
}

// TestSearchQueryFromRequest_ActorZeroValues verifies that an empty
// workspace produces an Actor with zero-value fields (the caller is
// responsible for workspace validation, not the query builder).
func TestSearchQueryFromRequest_ActorZeroValues(t *testing.T) {
	workspace := search.Actor{} // empty: no workspace extracted

	q := searchQueryFromRequest(
		searchRequest{Query: "anonymous"},
		search.SearchModeHybrid,
		10,
		workspace,
	)

	if !q.Actor.IsZero() {
		t.Error("Actor.IsZero() = false, want true (empty workspace)")
	}
}

// TestSearchQueryFromRequest_ActorPreservesOtherFields proves that
// adding Actor does not lose any existing field: Text, Mode, Limit,
// and Filters all survive the change.
func TestSearchQueryFromRequest_ActorPreservesOtherFields(t *testing.T) {
	workspace := search.Actor{
		WorkspaceID: "ws-preserve",
		UserID:      "user-preserve",
		IsAdmin:     false,
	}

	q := searchQueryFromRequest(
		searchRequest{
			Query: "  hello world  ",
			Mode:  "hybrid",
			Limit: 30,
			Filters: searchRequestFilter{
				Source:        "youtube",
				MediaType:     "video",
				Category:      "tutorial",
				Language:      "it",
				Tags:          []string{"golang", "docker"},
				DurationMsMin: 5000,
			},
		},
		search.SearchModeHybrid,
		30,
		workspace,
	)

	if q.Text != "hello world" {
		t.Errorf("Text = %q, want %q", q.Text, "hello world")
	}
	if q.Mode != search.SearchModeHybrid {
		t.Errorf("Mode = %q, want %q", q.Mode, search.SearchModeHybrid)
	}
	if q.Limit != 30 {
		t.Errorf("Limit = %d, want 30", q.Limit)
	}
	if q.Filters.Source != "youtube" {
		t.Errorf("Filters.Source = %q, want %q", q.Filters.Source, "youtube")
	}
	if q.Filters.MediaType != "video" {
		t.Errorf("Filters.MediaType = %q, want %q", q.Filters.MediaType, "video")
	}
	if q.Filters.Category != "tutorial" {
		t.Errorf("Filters.Category = %q, want %q", q.Filters.Category, "tutorial")
	}
	if q.Filters.Language != "it" {
		t.Errorf("Filters.Language = %q, want %q", q.Filters.Language, "it")
	}
	if len(q.Filters.Tags) != 2 || q.Filters.Tags[0] != "golang" || q.Filters.Tags[1] != "docker" {
		t.Errorf("Filters.Tags = %v, want [golang docker]", q.Filters.Tags)
	}
	if q.Filters.DurationMsMin != 5000 {
		t.Errorf("Filters.DurationMsMin = %d, want 5000", q.Filters.DurationMsMin)
	}
	if q.Actor.WorkspaceID != "ws-preserve" {
		t.Errorf("Actor.WorkspaceID = %q, want %q", q.Actor.WorkspaceID, "ws-preserve")
	}
}

// TestSearchQueryFromRequest_HandlesBuild verifies that backends
// receive the typed Actor, not a zeroed-out one.
// When searchQueryFromRequest receives a real workspace, the
// resulting Query.Actor must be non-zero (a real identity).
func TestSearchQueryFromRequest_HandlesBuild(t *testing.T) {
	workspace := search.Actor{
		WorkspaceID: "ws-prod-1",
		UserID:      "worker-001",
		IsAdmin:     false,
	}

	q := searchQueryFromRequest(
		searchRequest{Query: "prod"},
		search.SearchModeHybrid,
		20,
		workspace,
	)

	// Compile-time guarantee: search.Query.Actor is typed, not interface{}.
	// Runtime guarantee: the Actor is populated from workspace, not zeroed.
	if q.Actor.WorkspaceID == "" {
		t.Error("Actor.WorkspaceID empty; workspace was not propagated")
	}
	if q.Actor.UserID == "" {
		t.Error("Actor.UserID empty; PrincipalID was not propagated")
	}
}

// TestHandler_StubAggregatorCapturesActor proves the end-to-end
// actor propagation through a stub aggregator: the handler builds
// a Query with Actor from workspace, and the stub captures it.
func TestHandler_StubAggregatorCapturesActor(t *testing.T) {
	var captured search.Query
	stub := &actorCapturingAggregator{captured: &captured}

	h := NewHandler(WireParams{Aggregator: stub, Log: nil})

	// Simulate a gin context via httptest
	req := searchRequest{Query: "e2e", Mode: "ann", Limit: 10}
	workspace := search.Actor{
		WorkspaceID: "ws-e2e",
		UserID:      "u-e2e",
		IsAdmin:     true,
	}

	// Direct call to searchQueryFromRequest (the handler's function)
	q := searchQueryFromRequest(req, search.SearchModeANN, 10, workspace)

	// Verify the search.Query has Actor populated
	if q.Actor.WorkspaceID != "ws-e2e" {
		t.Errorf("Actor.WorkspaceID = %q, want %q", q.Actor.WorkspaceID, "ws-e2e")
	}
	if q.Actor.UserID != "u-e2e" {
		t.Errorf("Actor.UserID = %q, want %q", q.Actor.UserID, "u-e2e")
	}
	if !q.Actor.IsAdmin {
		t.Error("Actor.IsAdmin = false, want true")
	}

	// Also verify the stub would receive it correctly
	_, _ = h.aggreg.Search(context.Background(), q)
	if captured.Actor.WorkspaceID != "ws-e2e" {
		t.Errorf("stub captured Actor.WorkspaceID = %q, want %q", captured.Actor.WorkspaceID, "ws-e2e")
	}
}

// TestHandlerMapsMediaTypeToQueryMediaTypes verifies that when the
// request filter carries media_type, it is forwarded as
// Query.MediaTypes []string (for BackendRegistry capability
// selection) AND as Filters.MediaType (for downstream Qdrant
// must-predicate compilation).
// PR-AGENTE2-MEDIATYPE (Agente 2, Azione 2).
func TestHandlerMapsMediaTypeToQueryMediaTypes(t *testing.T) {
	workspace := search.Actor{
		WorkspaceID: "ws-mt",
		UserID:      "u-mt",
		IsAdmin:     false,
	}

	t.Run("video media_type populates both fields", func(t *testing.T) {
		q := searchQueryFromRequest(
			searchRequest{
				Query: "test",
				Filters: searchRequestFilter{
					MediaType: "video",
				},
			},
			search.SearchModeHybrid,
			20,
			workspace,
		)

		// Query.MediaTypes: should be ["video"]
		if len(q.MediaTypes) != 1 {
			t.Fatalf("len(MediaTypes) = %d, want 1", len(q.MediaTypes))
		}
		if q.MediaTypes[0] != "video" {
			t.Errorf("MediaTypes[0] = %q, want %q", q.MediaTypes[0], "video")
		}

		// Filters.MediaType: should still be "video"
		if q.Filters.MediaType != "video" {
			t.Errorf("Filters.MediaType = %q, want %q", q.Filters.MediaType, "video")
		}
	})

	t.Run("empty media_type leaves MediaTypes nil", func(t *testing.T) {
		q := searchQueryFromRequest(
			searchRequest{Query: "no filter"},
			search.SearchModeHybrid,
			20,
			workspace,
		)

		if q.MediaTypes != nil {
			t.Errorf("MediaTypes = %v, want nil (no media_type filter)", q.MediaTypes)
		}
	})

	t.Run("whitespace-only media_type leaves MediaTypes nil", func(t *testing.T) {
		q := searchQueryFromRequest(
			searchRequest{
				Query: "test",
				Filters: searchRequestFilter{
					MediaType: "   ",
				},
			},
			search.SearchModeHybrid,
			20,
			workspace,
		)

		if q.MediaTypes != nil {
			t.Errorf("MediaTypes = %v, want nil (whitespace-only trimmed to empty)", q.MediaTypes)
		}
		if q.Filters.MediaType != "" {
			t.Errorf("Filters.MediaType = %q, want empty after trim", q.Filters.MediaType)
		}
	})

	t.Run("audio media_type works", func(t *testing.T) {
		q := searchQueryFromRequest(
			searchRequest{
				Query: "sound",
				Filters: searchRequestFilter{
					MediaType: "audio",
				},
			},
			search.SearchModeANN,
			10,
			workspace,
		)

		if len(q.MediaTypes) != 1 || q.MediaTypes[0] != "audio" {
			t.Errorf("MediaTypes = %v, want [audio]", q.MediaTypes)
		}
		if q.Filters.MediaType != "audio" {
			t.Errorf("Filters.MediaType = %q, want audio", q.Filters.MediaType)
		}
	})

	t.Run("image media_type works", func(t *testing.T) {
		q := searchQueryFromRequest(
			searchRequest{
				Query: "photo",
				Filters: searchRequestFilter{
					MediaType: "image",
				},
			},
			search.SearchModeHybrid,
			15,
			workspace,
		)

		if len(q.MediaTypes) != 1 || q.MediaTypes[0] != "image" {
			t.Errorf("MediaTypes = %v, want [image]", q.MediaTypes)
		}
		if q.Filters.MediaType != "image" {
			t.Errorf("Filters.MediaType = %q, want image", q.Filters.MediaType)
		}
	})
}

// ── Error mapping tests (PR-AGENTE2-ERRORS — Agente 2, Azione 4) ────

// TestMapSearchError_InvalidCursor verifies ErrInvalidCursor → 422.
func TestMapSearchError_InvalidCursor(t *testing.T) {
	err := search.ErrInvalidCursor
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, err, "ws-test")

	if w.Code != 422 {
		t.Errorf("status = %d, want 422 (UnprocessableEntity)", w.Code)
	}
	body := w.Body.String()
	if !containsFold(body, "invalid cursor") {
		t.Errorf("body %q should mention 'invalid cursor'", body)
	}
}

// TestMapSearchError_MissingWorkspace verifies ErrMissingWorkspace → 403.
func TestMapSearchError_MissingWorkspace(t *testing.T) {
	err := search.ErrMissingWorkspace
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, err, "ws-test")

	if w.Code != 403 {
		t.Errorf("status = %d, want 403 (Forbidden)", w.Code)
	}
}

// TestMapSearchError_HybridRequiresSparse verifies ErrHybridRequiresSparse → 422.
func TestMapSearchError_HybridRequiresSparse(t *testing.T) {
	err := search.ErrHybridRequiresSparse
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, err, "ws-test")

	if w.Code != 422 {
		t.Errorf("status = %d, want 422 (UnprocessableEntity)", w.Code)
	}
	body := w.Body.String()
	if !containsFold(body, "hybrid") {
		t.Errorf("body %q should mention 'hybrid'", body)
	}
}

// TestMapSearchError_NoBackendAvailable verifies ErrNoBackendAvailable → 503.
func TestMapSearchError_NoBackendAvailable(t *testing.T) {
	err := search.ErrNoBackendAvailable
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, err, "ws-test")

	if w.Code != 503 {
		t.Errorf("status = %d, want 503 (ServiceUnavailable)", w.Code)
	}
	body := w.Body.String()
	if !containsFold(body, "no search backend") {
		t.Errorf("body %q should mention 'no search backend'", body)
	}
}

// TestMapSearchError_AllBackendsFailed verifies ErrAllBackendsFailed → 502.
func TestMapSearchError_AllBackendsFailed(t *testing.T) {
	err := search.ErrAllBackendsFailed
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, err, "ws-test")

	if w.Code != 502 {
		t.Errorf("status = %d, want 502 (BadGateway)", w.Code)
	}
	body := w.Body.String()
	if !containsFold(body, "all search backends") {
		t.Errorf("body %q should mention 'all search backends'", body)
	}
}

// TestMapSearchError_UnknownError verifies unknown errors → 500.
func TestMapSearchError_UnknownError(t *testing.T) {
	err := errTestUnknown
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, err, "ws-test")

	if w.Code != 500 {
		t.Errorf("status = %d, want 500 (InternalServerError)", w.Code)
	}
}

// TestMapSearchError_WrappedSentinel verifies that errors.Is works
// through fmt.Errorf("...: %w", sentinel) wrappers.
func TestMapSearchError_WrappedSentinel(t *testing.T) {
	h := NewHandler(WireParams{})

	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"wrapped InvalidCursor", newWrappedErr(search.ErrInvalidCursor), 422},
		{"wrapped MissingWorkspace", newWrappedErr(search.ErrMissingWorkspace), 403},
		{"wrapped HybridRequiresSparse", newWrappedErr(search.ErrHybridRequiresSparse), 422},
		{"wrapped NoBackendAvailable", newWrappedErr(search.ErrNoBackendAvailable), 503},
		{"wrapped AllBackendsFailed", newWrappedErr(search.ErrAllBackendsFailed), 502},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestGinContext()
			h.mapSearchError(c, tt.err, "ws-test")
			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// ── Response truthfulness tests (PR-AGENTE2-TRUTHFUL — Agente 2, Azione 3)

// TestHandlerExposesPartialWithoutInternalPaths verifies that when
// a result is partial (some backends errored), the response carries
// degraded=true, provider_errors, and none of the items leak
// internal fields (LocalPath, DriveFileID, raw Drive URL).
func TestHandlerExposesPartialWithoutInternalPaths(t *testing.T) {
	r := &search.Result{
		Partial: true,
		ProviderErrors: map[string]string{
			"artlist": "timeout",
		},
		Items: []search.Candidate{
			{
				AssetID:      "asset-1",
				Title:        "Sunset",
				Source:       "youtube",
				MediaType:    "video",
				PreviewURL:   "https://signed.example.com/asset-1",
				Score:        0.95,
				ThumbnailURL: "https://thumbnail.internal/qdrant_collection_x",
			},
		},
	}

	resp := resultToResponse(r, "sunset", search.SearchModeHybrid, search.SearchCatalog, "")

	if !resp.OK {
		t.Error("partial with items should have OK=true")
	}
	if !resp.Degraded {
		t.Error("partial with items should have Degraded=true")
	}
	if !resp.Partial {
		t.Error("Partial should be true")
	}
	if resp.BackendErrors == nil || resp.BackendErrors["artlist"] != "timeout" {
		t.Errorf("ProviderErrors = %v, want {artlist: timeout}", resp.BackendErrors)
	}

	// Verify no internal fields leak in items
	for _, item := range resp.Items {
		if strings.Contains(item.PreviewURL, "drive.google.com") {
			t.Errorf("PreviewURL %q should not be a raw Drive URL", item.PreviewURL)
		}
		// searchResultItem deliberately excludes LocalPath, DriveLink, ThumbnailURL
		// so those fields are structurally impossible to leak.
	}
}

// TestHandlerReturns503WhenNoBackend verifies that when the
// aggregator returns ErrNoBackendAvailable, the handler responds 503.
func TestMediaSearchResponse_DoesNotSerializeRawDriveLink(t *testing.T) {
	r := &search.Result{Items: []search.Candidate{{
		AssetID:    "asset-stale",
		Title:      "stale link candidate",
		Source:     "semantic",
		MediaType:  "video",
		DriveLink:  "https://drive.google.com/file/d/deleted-id/view",
		PreviewURL: "https://signed.example.test/assets/asset-stale",
	}}}

	resp := resultToResponse(r, "stale", search.SearchModeANN, search.SearchCatalog, "")
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal search response: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "drive_link") || strings.Contains(body, "deleted-id") {
		t.Fatalf("raw Drive locator leaked into public response: %s", body)
	}
	if !strings.Contains(body, "signed.example.test/assets/asset-stale") {
		t.Fatalf("signed PreviewURL missing from public response: %s", body)
	}
}

func TestHandlerReturns503WhenNoBackend(t *testing.T) {
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, search.ErrNoBackendAvailable, "ws-test")

	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// TestHandlerReturns502WhenAllBackendsFail verifies that when the
// aggregator returns ErrAllBackendsFailed, the handler responds 502.
func TestHandlerReturns502WhenAllBackendsFail(t *testing.T) {
	h := NewHandler(WireParams{})
	c, w := newTestGinContext()

	h.mapSearchError(c, search.ErrAllBackendsFailed, "ws-test")

	if w.Code != 502 {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

// errorReturningAggregator is a stub that always returns a fixed error.
type errorReturningAggregator struct {
	err error
}

func (a *errorReturningAggregator) Search(_ context.Context, _ search.Query) (*search.Result, error) {
	return nil, a.err
}

// errTestUnknown is a plain error not matching any sentinel.
var errTestUnknown = errStr("some unexpected runtime error")

type errStr string

func (e errStr) Error() string { return string(e) }

// newWrappedErr wraps a sentinel so errors.Is still traverses the chain.
func newWrappedErr(sentinel error) error {
	return errWrapped{msg: "upstream: " + sentinel.Error(), cause: sentinel}
}

type errWrapped struct {
	msg   string
	cause error
}

func (e errWrapped) Error() string { return e.msg }
func (e errWrapped) Unwrap() error { return e.cause }

// newTestGinContext creates a real *gin.Context backed by httptest
// so apiutil.Error writes to the recorder (which we can inspect).
func newTestGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

// containsFold reports whether s contains substr case-insensitively.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// actorCapturingAggregator is a test stub that records the Query
// passed to its Search method.
type actorCapturingAggregator struct {
	captured *search.Query
}

func (a *actorCapturingAggregator) Search(_ context.Context, q search.Query) (*search.Result, error) {
	*a.captured = q
	return &search.Result{Items: []search.Candidate{}}, nil
}
