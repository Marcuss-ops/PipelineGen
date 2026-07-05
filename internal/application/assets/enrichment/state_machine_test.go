// Package enrichment — state_machine_test.go is the canonical TDD
// coverage for the typed 4-state EnrichStateMachine wrapper
// (PR-ENRICHMENT-STATE-MACHINE, July 2026, godlike/06 SSOT).
//
// 6 contract tests pin the canonical validEdges closed-set + the
// godlike/07 typed-error contract (errors.Is + errors.As probe):
//  1. TestStateMachine_MarkPending_StampsPending — happy-path: the
//     canonical ingest stamp writes PENDING through the repo port.
//  2. TestStateMachine_Transition_PendingToEnriching — legal edge.
//  3. TestStateMachine_Transition_EnrichingToEnriched — legal
//     terminal-success edge.
//  4. TestStateMachine_Transition_EnrichingToFailed — legal
//     terminal-failure edge.
//  5. TestStateMachine_Transition_IllegalEnrichingToPending — illegal
//     edge surfaces godlike/07 typed envelope; errors.Is + errors.As.
//  6. TestStateMachine_Transition_EmptyAssetID — pre-flight guard
//     rejects empty IDs BEFORE the SQL roundtrip (godlike/07
//     typed-error contract).
package enrichment

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// mockEnrichRepo is an in-memory EnrichRepositoryPort for the
// state-machine TDD tests. Captures the last SetEnrichState call so
// tests can assert on the (id, state) pair without spinning up a
// SQLite fixture. The read surface (GetEnrichState) is implemented
// as a getter to keep the mock hermetic — current tests focus on the
// write path; future tests that exercise the from-state pre-flight
// (transition_validation_via_GetEnrichState) will consult
// getResp.
type mockEnrichRepo struct {
	mu      sync.Mutex
	lastID  string
	lastSt  asset.EnrichState
	setErr  error
	getResp asset.EnrichState
	getErr  error
}

func (m *mockEnrichRepo) SetEnrichState(_ context.Context, id string, st asset.EnrichState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	m.lastID = id
	m.lastSt = st
	return nil
}

func (m *mockEnrichRepo) GetEnrichState(_ context.Context, _ string) (asset.EnrichState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.getResp, nil
}

func TestStateMachine_MarkPending_StampsPending(t *testing.T) {
	repo := &mockEnrichRepo{}
	m, err := NewEnrichStateMachine(repo)
	if err != nil {
		t.Fatalf("NewEnrichStateMachine: %v", err)
	}
	if err := m.MarkPending(context.Background(), "asset-123"); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	if repo.lastID != "asset-123" {
		t.Errorf("lastID: got %q, want %q", repo.lastID, "asset-123")
	}
	if repo.lastSt != asset.EnrichStatePending {
		t.Errorf("lastSt: got %q, want %q", repo.lastSt, asset.EnrichStatePending)
	}
}

func TestStateMachine_Transition_PendingToEnriching(t *testing.T) {
	repo := &mockEnrichRepo{}
	m, err := NewEnrichStateMachine(repo)
	if err != nil {
		t.Fatalf("NewEnrichStateMachine: %v", err)
	}
	if err := m.Transition(context.Background(), "asset-456", asset.EnrichStatePending, asset.EnrichStateEnriching); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if repo.lastSt != asset.EnrichStateEnriching {
		t.Errorf("lastSt: got %q, want %q", repo.lastSt, asset.EnrichStateEnriching)
	}
}

func TestStateMachine_Transition_EnrichingToEnriched(t *testing.T) {
	repo := &mockEnrichRepo{}
	m, err := NewEnrichStateMachine(repo)
	if err != nil {
		t.Fatalf("NewEnrichStateMachine: %v", err)
	}
	if err := m.MarkEnriched(context.Background(), "asset-789"); err != nil {
		t.Fatalf("MarkEnriched: %v", err)
	}
	if repo.lastSt != asset.EnrichStateEnriched {
		t.Errorf("lastSt: got %q, want %q", repo.lastSt, asset.EnrichStateEnriched)
	}
}

func TestStateMachine_Transition_EnrichingToFailed(t *testing.T) {
	repo := &mockEnrichRepo{}
	m, err := NewEnrichStateMachine(repo)
	if err != nil {
		t.Fatalf("NewEnrichStateMachine: %v", err)
	}
	if err := m.MarkFailed(context.Background(), "asset-fail"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if repo.lastSt != asset.EnrichStateFailed {
		t.Errorf("lastSt: got %q, want %q", repo.lastSt, asset.EnrichStateFailed)
	}
}

func TestStateMachine_Transition_IllegalEnrichingToPending(t *testing.T) {
	repo := &mockEnrichRepo{}
	m, err := NewEnrichStateMachine(repo)
	if err != nil {
		t.Fatalf("NewEnrichStateMachine: %v", err)
	}
	// ENRICHING → PENDING is NOT a legal edge (the canonical validEdges
	// set in state_machine.go only allows ENRICHING → {ENRICHED, FAILED}
	// and the typed state machine rejects the inverse as well). The
	// godlike/07 typed-error envelope must surface.
	err = m.Transition(context.Background(), "asset-bad", asset.EnrichStateEnriching, asset.EnrichStatePending)
	if err == nil {
		t.Fatal("Transition ENRICHING→PENDING: expected illegal-transition error, got nil")
	}
	if !errors.Is(err, ErrIllegalEnrichTransition) {
		t.Errorf("errors.Is(ErrIllegalEnrichTransition): got false for err=%v", err)
	}
	var ite *IllegalEnrichTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("errors.As(*IllegalEnrichTransitionError): got false for err=%v", err)
	}
	if ite.From != asset.EnrichStateEnriching || ite.To != asset.EnrichStatePending {
		t.Errorf("envelope mismatch: From=%q To=%q, want ENRICHING→PENDING", ite.From, ite.To)
	}
	// godlike/07 typed-error contract: the rejected edge MUST NOT
	// have invoked the repo port (otherwise the SQL column would
	// have flipped to the wrong value, surfacing a silent-success
	// bug masked by the typed-error envelope).
	if repo.lastSt != "" {
		t.Errorf("repo.lastSt should be empty (transition rejected): got %q", repo.lastSt)
	}
}

func TestStateMachine_Transition_EmptyAssetID(t *testing.T) {
	repo := &mockEnrichRepo{}
	m, err := NewEnrichStateMachine(repo)
	if err != nil {
		t.Fatalf("NewEnrichStateMachine: %v", err)
	}
	// godlike/07 pre-flight guard: empty IDs are rejected BEFORE
	// the SQL roundtrip so the typed-error contract surfaces a
	// stable sentinel rather than the lower-level fmt.Errorf
	// format-string from the SQL primitive.
	if err := m.MarkPending(context.Background(), ""); !errors.Is(err, ErrEnrichAssetIDRequired) {
		t.Errorf("MarkPending(\"\"): got err=%v, want ErrEnrichAssetIDRequired", err)
	}
	if err := m.Transition(context.Background(), "", asset.EnrichStatePending, asset.EnrichStateEnriching); !errors.Is(err, ErrEnrichAssetIDRequired) {
		t.Errorf("Transition(\"\"): got err=%v, want ErrEnrichAssetIDRequired", err)
	}
}

// TestStateMachine_Transition_RowMissingRemapsToTypedSentinel pins the
// godlike/07 typed-error contract on the row-missing remap. The
// Transition() method must surface ErrEnrichStateMissing (errors.Is
// probe-friendly) when the SQL primitive's row-missing fmt.Errorf
// propagates through. Without this test the stringly-typed remap
// (strings.Contains on the SQL error message) could silently break
// in a future refactor of the SQL primitive's error format.
func TestStateMachine_Transition_RowMissingRemapsToTypedSentinel(t *testing.T) {
	repo := &mockEnrichRepo{
		setErr: fmt.Errorf("clips.SetEnrichState(test-id, PENDING): asset row missing in media_assets"),
	}
	m, err := NewEnrichStateMachine(repo)
	if err != nil {
		t.Fatalf("NewEnrichStateMachine: %v", err)
	}
	err = m.Transition(context.Background(), "test-id", asset.EnrichStatePending, asset.EnrichStateEnriching)
	if err == nil {
		t.Fatal("Transition row-missing: expected ErrEnrichStateMissing, got nil")
	}
	if !errors.Is(err, ErrEnrichStateMissing) {
		t.Errorf("errors.Is(ErrEnrichStateMissing): got false for err=%v", err)
	}
}
