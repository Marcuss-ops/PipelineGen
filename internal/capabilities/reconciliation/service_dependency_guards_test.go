package reconciliation

import "testing"

// Constructor fail-closed guards for core and repair dependencies.

func TestNewServiceFromDeps_PanicsOnNilCore(t *testing.T) {
	cases := []struct {
		name    string
		mutator func(*ServiceDeps)
	}{
		{
			name:    "empty Schema.Version",
			mutator: func(d *ServiceDeps) { d.Schema.Version = "" },
		},
		{
			name:    "nil Qdrant",
			mutator: func(d *ServiceDeps) { d.Qdrant = nil },
		},
		{
			name:    "nil SQLite",
			mutator: func(d *ServiceDeps) { d.SQLite = nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := ServiceDeps{
				Schema: defaultSchema(),
				Qdrant: &stubQdrant{},
				SQLite: &stubSQLite{},
			}
			tc.mutator(&deps)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for %s, got none", tc.name)
				}
			}()
			_ = NewServiceFromDeps(deps)
		})
	}
}

// ── PR 10 fail-closed scroll gates + panic guard + contentHash propagation ──
//
// PR 10 (June 2026) hardens the reconciler scroll loop: each gate fires
// a non-nil error from Service.scrollAll → Reconcile returns the report
// + a wrapped fatal error in BOTH DryRun and Apply modes. Partial data
// is intentionally discarded so a downstream operator never sees a
// misleading "all clear" through zero-actionable pairs.

// pagingQdrant is a multi-page stub with error injection. Used by the
// fail-closed gate tests below. When nextOffset is non-empty AND errAt=0
// the stub keeps yielding a single offset forever (maxPages cap test).
// When errAt > 0 the stub returns err at the (errAt-1)th 0-indexed call.

func TestReconcile_NewServiceFromDeps_PanicsOnNilOutboxOrPayload(t *testing.T) {
	// PR 10: nil Outbox / nil Payload / BOTH nil panic. The silent
	// noop fallback that masked production half-wiring is gone.
	cases := []struct {
		name    string
		mutator func(*ServiceDeps)
	}{
		{
			name:    "nil Outbox",
			mutator: func(d *ServiceDeps) { d.Outbox = nil },
		},
		{
			name:    "nil Payload",
			mutator: func(d *ServiceDeps) { d.Payload = nil },
		},
		{
			name:    "nil Outbox AND nil Payload",
			mutator: func(d *ServiceDeps) { d.Outbox = nil; d.Payload = nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := ServiceDeps{
				Schema:  defaultSchema(),
				Qdrant:  &stubQdrant{},
				SQLite:  &stubSQLite{},
				Outbox:  &stubOutbox{},
				Payload: &stubPayload{},
			}
			tc.mutator(&deps)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for %s, got none", tc.name)
				}
			}()
			_ = NewServiceFromDeps(deps)
		})
	}
}
