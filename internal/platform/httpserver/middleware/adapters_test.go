package httpserver

import "testing"

// ── TokenSecurityAdapter contract ──────────────────────────────────
//
// PG-006.1 (June 2026) tests. These pin:
//   - nil-receiver safety (method dispatch path under typed-nil)
//   - Enable field is the canonical EnableAuth() source of truth
//     (was a round-1 regression: deriving EnableAuth from Admin emptiness
//     collapsed the fail-closed misconfig state)
//   - Enable=false + non-empty Admin → EnableAuth()=false (regression latch)
//   - Enable=true + empty Admin → EnableAuth()=true (fail-closed misconfig state)
//   - Worker field is independent of EnableAuth()

func TestTokenSecurityAdapter_NilReceiverSafe(t *testing.T) {
	t.Parallel()

	var nilAdapter *TokenSecurityAdapter

	if nilAdapter.EnableAuth() {
		t.Errorf("nil receiver EnableAuth() should be false; got true")
	}
	if got := nilAdapter.AdminToken(); got != "" {
		t.Errorf("nil receiver AdminToken() should be empty; got %q", got)
	}
	if got := nilAdapter.WorkerToken(); got != "" {
		t.Errorf("nil receiver WorkerToken() should be empty; got %q", got)
	}
}

func TestTokenSecurityAdapter_AllZeroIsPassThrough(t *testing.T) {
	t.Parallel()

	a := &TokenSecurityAdapter{}
	if a.EnableAuth() {
		t.Errorf("zero-value EnableAuth() should be false; got true")
	}
	if got := a.AdminToken(); got != "" {
		t.Errorf("zero-value AdminToken() should be empty; got %q", got)
	}
	if got := a.WorkerToken(); got != "" {
		t.Errorf("zero-value WorkerToken() should be empty; got %q", got)
	}
}

func TestTokenSecurityAdapter_EnableFalseOverridesPresentAdmin(t *testing.T) {
	t.Parallel()

	// REGRESSION LATCH (PG-006.1 round-2). Round-1 collapsed
	// Enable=false + Admin="secret" to EnableAuth=false correctly but
	// ALSO collapsed Enable=true + Admin="" to EnableAuth=false (when
	// it should be true → fail-closed 500). This test pins the
	// post-round-2 fix: Enable field alone gates EnableAuth();
	// Admin/Worker content does NOT.
	a := &TokenSecurityAdapter{Enable: false, Admin: "secret-admin"}
	if a.EnableAuth() {
		t.Errorf("Enable=false with non-empty Admin: EnableAuth() should be false; got true")
	}
	if got := a.AdminToken(); got != "secret-admin" {
		t.Errorf("Enable=false AdminToken() should still return the secret verbatim; got %q", got)
	}
}

func TestTokenSecurityAdapter_EnableTrueEmptyAdminIsFailClosedMisconfig(t *testing.T) {
	t.Parallel()

	// Locks the canonical misconfig state: operator set Enable=true
	// (auth on) but Admin="". admin_token.go's RequireAdminToken
	// gates a 500 on this combination via the `expected == ""` check.
	// EnableAuth() MUST return true here so the middleware enters
	// that branch rather than silently pass-through.
	a := &TokenSecurityAdapter{Enable: true}
	if !a.EnableAuth() {
		t.Errorf("Enable=true + empty Admin EnableAuth() should be true (fail-closed misconfig state); got false")
	}
	if got := a.AdminToken(); got != "" {
		t.Errorf("Enable=true + empty Admin AdminToken() should be empty; got %q", got)
	}
}

func TestTokenSecurityAdapter_EnableTruePresentAdminEnforcesAuth(t *testing.T) {
	t.Parallel()

	const adminSecret = "secret-admin"
	a := &TokenSecurityAdapter{Enable: true, Admin: adminSecret}

	if !a.EnableAuth() {
		t.Errorf("Enable=true + Admin set EnableAuth() should be true; got false")
	}
	if got := a.AdminToken(); got != adminSecret {
		t.Errorf("AdminToken() = %q; want %q", got, adminSecret)
	}
	if got := a.WorkerToken(); got != "" {
		t.Errorf("WorkerToken() should be empty (Worker unset); got %q", got)
	}
}

func TestTokenSecurityAdapter_WorkerOnlyDoesNotEnableAdminAuth(t *testing.T) {
	t.Parallel()

	// Worker set, Enable=false (or Admin empty) → EnableAuth remains
	// false. Worker is independent of the admin auth path.
	const workerSecret = "secret-worker"
	a := &TokenSecurityAdapter{Worker: workerSecret}

	if a.EnableAuth() {
		t.Errorf("Worker-only EnableAuth() should be false; got true")
	}
	if got := a.WorkerToken(); got != workerSecret {
		t.Errorf("WorkerToken() = %q; want %q", got, workerSecret)
	}
	if got := a.AdminToken(); got != "" {
		t.Errorf("Worker-only AdminToken() should be empty; got %q", got)
	}
}

func TestTokenSecurityAdapter_AllThreeFieldsReadsExactValues(t *testing.T) {
	t.Parallel()

	const adminSecret = "admin-x"
	const workerSecret = "worker-y"
	a := &TokenSecurityAdapter{Enable: true, Admin: adminSecret, Worker: workerSecret}

	if !a.EnableAuth() {
		t.Errorf("Enable=true EnableAuth() should be true; got false")
	}
	if got := a.AdminToken(); got != adminSecret {
		t.Errorf("AdminToken() = %q; want %q", got, adminSecret)
	}
	if got := a.WorkerToken(); got != workerSecret {
		t.Errorf("WorkerToken() = %q; want %q", got, workerSecret)
	}
}
