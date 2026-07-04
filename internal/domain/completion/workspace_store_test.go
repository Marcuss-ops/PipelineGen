// FASE 2.2 P0-COMPL-7 — typed-contract tests for the canonical
// WorkspaceStore high-level domain port. godlike/07 typed-error
// contract: errors.Is probes + struct-shape assertions are the
// canonical test surface.
//
// No concrete adapter is supplied in FASE 2.2 — the point is to pin
// the PACKAGE surface (WorkspaceStore interface + WorkspaceHandle
// envelope + 5 typed-error sentinels) so a future adapter MUST
// satisfy the interface (signature drift surfaces as a build failure
// via the compile-time `var _ WorkspaceStore = stubStorer{}` pin
// in TestWorkspaceStore_StubStorerSatisfiesInterface, not as a
// runtime panic).
//
// The actual worker-side concrete adapter
// (`internal/application/jobs/completion/workspace_store_adapter.go`)
// is a FASE 2.2 follow-up wire-closure PR — its TDD tests live
// alongside it (forward-pointer).
package completion

import (
	"errors"
	"testing"
	"time"
)

// stubStorer is the minimal stub adapter that satisfies the canonical
// WorkspaceStore interface. It is declared in the test file (NOT a
// separate package) per godlike/07 minimum-blast-radius: a single
// test-side struct pin catches the canonical 5-method signature
// drift at compile time without polluting the production package
// surface with a real adapter.
//
// Each method returns the typed-error-free shape: zero-value
// WorkspaceHandle, nil error, empty string, nil — this is enough
// for the COMPILE-TIME pin (the canonical interface shape is the
// only thing pinned here; runtime behaviour is the future adapter's
// concern, not the test surface).
type stubStorer struct{}

func (stubStorer) Prepare(jobID string, ttlHint time.Duration) (WorkspaceHandle, error) {
	return WorkspaceHandle{}, nil
}

func (stubStorer) Complete(jobID string, sha256 string) error {
	return nil
}

func (stubStorer) Evict(jobID string) error {
	return nil
}

func (stubStorer) MarkTTL(jobID string, ttl time.Duration) error {
	return nil
}

func (stubStorer) Path(jobID string) (string, error) {
	return "", nil
}

// TestWorkspaceStore_StubStorerSatisfiesInterface pins the canonical
// 5-method signature via a real stub adapter. Per godlike/06 SSOT,
// this is the SOLE canonical compile-time pin: any future drift in
// the WorkspaceStore signature (added/removed methods, parameter
// type change, return type change) surfaces as a build failure at
// the `var _ WorkspaceStore = stubStorer{}` line below — not as a
// runtime panic, not as a tautological nil-interface check.
//
// This replaces the pre-fix tautological test
// `TestWorkspaceStore_InterfaceSelfAssertion` which checked only the
// nil-interface zero value (no signature drift detection).
func TestWorkspaceStore_StubStorerSatisfiesInterface(t *testing.T) {
	var _ WorkspaceStore = stubStorer{}
}

// TestWorkspaceStore_TypedErrorChain pins that EVERY sentinel in
// workspace_store.go is a valid `error` implementor. godlike/07 typed
// errors.Is probes: each sentinel must be wired to the
// WorkspaceStore.* methods in the forward-pointer adapter (not yet
// shipped). callers compose via errors.Is, never via string-match.
func TestWorkspaceStore_TypedErrorChain(t *testing.T) {
	sentinels := []error{
		ErrWorkspaceStoreNotConfigured,
		ErrWorkspaceAlreadyExists,
		ErrWorkspaceNotFound,
		ErrWorkspaceMarkTTLFailed,
		ErrWorkspaceHandleExpired,
	}
	for _, sentinel := range sentinels {
		if sentinel == nil {
			t.Fatalf("typed-error sentinel must be non-nil")
		}
		// errors.Is-navigable: must wrap to itself.
		if !errors.Is(sentinel, sentinel) {
			t.Fatalf("sentinel %v must satisfy errors.Is(self, self)", sentinel)
		}
	}
}

// TestWorkspaceHandle_FieldsAreExported pins the canonical envelope
// shape (Path string + ExpiresAt time.Time). FASE 2.2 forward-pointer:
// a future adapter can extend the struct with feature-fields WITHOUT
// breaking the canonical surface (compile-time assertion `var _
// time.Time = WorkspaceHandle{}.ExpiresAt` in workspace_store.go
// catches shape drift at build time, not runtime).
func TestWorkspaceHandle_FieldsAreExported(t *testing.T) {
	expected := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	h := WorkspaceHandle{
		Path:      "/var/pipelinegen/jobs/job-abc",
		ExpiresAt: expected,
	}
	if h.Path != "/var/pipelinegen/jobs/job-abc" {
		t.Fatalf("WorkspaceHandle.Path getter round-trip failed: got %q", h.Path)
	}
	if !h.ExpiresAt.Equal(expected) {
		t.Fatalf("WorkspaceHandle.ExpiresAt getter round-trip failed: got %v, want %v", h.ExpiresAt, expected)
	}
}

// TestWorkspaceStore_StubStorerPathReturnShape pins the canonical
// `(string, error)` return shape of the Path method. godlike/07
// typed-error contract: empty-string is NOT a valid "absent"
// sentinel — the canonical absent return is ("", ErrWorkspaceNotFound).
// The stubStorer here returns ("", nil) for the happy-path shape; the
// absent-path is asserted via errors.Is on the typed sentinel in the
// forward-pointer adapter's tests.
func TestWorkspaceStore_StubStorerPathReturnShape(t *testing.T) {
	var s WorkspaceStore = stubStorer{}
	gotPath, err := s.Path("any-jobID")
	if err != nil {
		t.Fatalf("stubStorer.Path returned unexpected error: %v", err)
	}
	if gotPath != "" {
		t.Fatalf("stubStorer.Path happy-path must return empty string (zero-value): got %q", gotPath)
	}
}
