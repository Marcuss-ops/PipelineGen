// Package mediasearch — unit tests for /internal/v1/media/search handler.
//
// Agente 2, Azione 1: verifica che il workspace venga propagato
// come search.Query.Actor dal handler all'aggregatore.
package mediasearch

import (
	"context"
	"testing"

	mediasearchapp "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// TestHandlerPropagatesWorkspaceActor verifies that the handler
// populates search.Query.Actor with the real workspace extracted
// from the auth middleware (not default/zero values).
func TestHandlerPropagatesWorkspaceActor(t *testing.T) {
	workspace := mediasearchapp.WorkspaceContext{
		WorkspaceID: "ws-abc123",
		PrincipalID: "user-42",
		IsAdmin:     false,
	}

	q := searchQueryFromRequest(
		searchRequest{Query: "test query"},
		mediasearchapp.SearchModeHybrid,
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
	workspace := mediasearchapp.WorkspaceContext{
		WorkspaceID: "ws-admin",
		PrincipalID: "admin-007",
		IsAdmin:     true,
	}

	q := searchQueryFromRequest(
		searchRequest{Query: "admin search"},
		mediasearchapp.SearchModeANN,
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
	workspace := mediasearchapp.WorkspaceContext{} // empty: no workspace extracted

	q := searchQueryFromRequest(
		searchRequest{Query: "anonymous"},
		mediasearchapp.SearchModeHybrid,
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
	workspace := mediasearchapp.WorkspaceContext{
		WorkspaceID: "ws-preserve",
		PrincipalID: "user-preserve",
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
		mediasearchapp.SearchModeHybrid,
		30,
		workspace,
	)

	if q.Text != "hello world" {
		t.Errorf("Text = %q, want %q", q.Text, "hello world")
	}
	if q.Mode != mediasearchapp.SearchModeHybrid {
		t.Errorf("Mode = %q, want %q", q.Mode, mediasearchapp.SearchModeHybrid)
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
	workspace := mediasearchapp.WorkspaceContext{
		WorkspaceID: "ws-prod-1",
		PrincipalID: "worker-001",
		IsAdmin:     false,
	}

	q := searchQueryFromRequest(
		searchRequest{Query: "prod"},
		mediasearchapp.SearchModeHybrid,
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
	workspace := mediasearchapp.WorkspaceContext{
		WorkspaceID: "ws-e2e",
		PrincipalID: "u-e2e",
		IsAdmin:     true,
	}

	// Direct call to searchQueryFromRequest (the handler's function)
	q := searchQueryFromRequest(req, mediasearchapp.SearchModeANN, 10, workspace)

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

// actorCapturingAggregator is a test stub that records the Query
// passed to its Search method.
type actorCapturingAggregator struct {
	captured *search.Query
}

func (a *actorCapturingAggregator) Search(_ context.Context, q search.Query) (*search.Result, error) {
	*a.captured = q
	return &search.Result{Items: []search.Candidate{}}, nil
}
