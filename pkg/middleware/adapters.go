// Package middleware (pkg/middleware) — leaf-only canonical concrete
// adapters that satisfy the application-layer SecurityAdapter ports.
//
// PG-006.1 (June 2026): the typed SecurityAdapter struct was previously
// duplicated three times across the codebase:
//
//   - serverSecurityAdapter       (internal/api/server.go)
//   - genDocsSecurityAdapter      (cmd/admin/gen_api_docs.go)
//   - middlewareSecurityAdapter  (internal/app/middleware_security_adapter.go)
//
// Each of the three wrapped *config.Config the same way and exposed the
// same 3 methods (EnableAuth/AdminToken/WorkerToken) for consumption
// by the auth/worker-auth/admin-token middlewares. PG-006.1
// consolidates the canonical concrete struct here so internal/api and
// cmd/admin can import it without crossing internal/app layering
// boundaries; the composition root at internal/app/ wires the
// structured value from cfg.Security fields at construction time.
//
// Pattern 0 (PG-006, June 2026): a concrete adapter that satisfies
// application-layer auth ports lives in pkg/middleware, not in
// internal/app/. The compile-time assertion that asserts
// *pkg/middleware.TokenSecurityAdapter satisfies AuthSecurityPort
// lives in internal/application/middleware/ports.go next to the port.
//
// Note: pkg/ is leaf-only (AGENTS.md Pattern 4). *config.Config is
// typed in internal/platform/config and is therefore NOT exposed
// here. Call-sites construct TokenSecurityAdapter directly from
// cfg.Security field reads. This snapshot-immutability pattern means
// the adapter does NOT live-update on cfg reload (intentional — auth
// state is server-mutable only via explicit re-wire); the trade-off
// matches test fixture ergonomics (`&TokenSecurityAdapter{Admin: "x"}`).
package middleware

// TokenSecurityAdapter is the canonical SecurityAdapter concrete
// implementation. It exposes the EnableAuth/AdminToken/WorkerToken
// method set that internal/application/middleware.AuthSecurityPort
// (and downstream auth middlewares RequireAdminToken / Auth /
// WorkerAuth) consume.
//
// Construct it from cfg.Security at composition time via a snapshot
// literal — the Enable field carries cfg.Security.EnableAuth verbatim
// (the operator-facing on/off switch), and Admin/Worker carry the
// secret strings. Or supply all three fields directly in tests, CLI
// utilities, and documentation generators that do not carry a full
// *config.Config.
//
// EnableAuth is the cfg.Security.EnableAuth passthrough (after
// nil-receiver-safe-guard). It is NOT derived from Admin emptiness;
// the "Enable=true with Admin empty" combination is the deliberate
// fail-closed misconfig state: RequireAdminToken() reads
// EnableAuth()==true, then admin_token.go's `expected == ""` check
// returns 500 (refuse request) per its error model (see
// admin_token.go's misconfig section). Operators who want auth OFF
// must set Enable=false (regardless of Admin contents) — that means
// Enable=false + Admin="secret" is also pass-through (NOT enforced),
// inline with the previous serverSecurityAdapter + middlewareSecurityAdapter
// + genDocsSecurityAdapter semantics that were the pre-PG-006.1 trio.
//
// Empty Admin with Enable=true remains a fail-closed misconfig (500).
// Both empty Admin + empty Enable is a no-op pass-through.
//
// Every method on TokenSecurityAdapter is nil-receiver-safe. Legacy
// sites that round-trip typed-nil values reach method dispatch
// rather than the nil-interface short-circuit guard in admin_token.go,
// and unsafe implementations would panic under those paths. The
// safe guards here unify the divergent contracts of the three
// pre-PG-006.1 inline adapters (one had a nil-cfg panic, two did
// not — see PG-006.1 review notes).
type TokenSecurityAdapter struct {
	// Enable is the cfg.Security.EnableAuth passthrough. When false,
	// the auth middleware short-circuits to pass-through without
	// consulting Admin/Worker values. This is the operator-facing
	// on/off switch and is the canonical source of truth for
	// EnableAuth().
	Enable bool
	// Admin is the admin-facing secret. Empty Admin with Enable=true
	// is a fail-closed misconfig (500) per admin_token.go's behavior;
	// callers SHOULD pair an Enable=true with a non-empty Admin in
	// production. Empty Admin with Enable=false is a clean no-op
	// pass-through.
	Admin string
	// Worker is the worker-facing secret. Empty Worker with Enable-style
	// worker auth results in rejecting every worker-side request via
	// the constant-time compare path in WorkerAuth.
	Worker string
}

// EnableAuth returns the Enable field value, nil-receiver-safe.
// PG-006.1 review (round-2): the prior round-1 implementation
// derived EnableAuth from Admin != "" which collapsed the
// fail-closed misconfig state (Enable=true + Admin="") into the
// pass-through state. This was a behavior regression from the three
// pre-PG-006.1 inline adapters which all read cfg.Security.EnableAuth
// directly. The rule is now canonicalized here.
func (t *TokenSecurityAdapter) EnableAuth() bool {
	return t != nil && t.Enable
}

// AdminToken returns the admin-facing secret. Nil-receiver-safe.
func (t *TokenSecurityAdapter) AdminToken() string {
	if t == nil {
		return ""
	}
	return t.Admin
}

// WorkerToken returns the worker-facing secret. Nil-receiver-safe.
func (t *TokenSecurityAdapter) WorkerToken() string {
	if t == nil {
		return ""
	}
	return t.Worker
}
