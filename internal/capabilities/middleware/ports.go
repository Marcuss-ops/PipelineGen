// Package middleware (ports) — typed application-layer ports that the
// API middleware package depends on.
//
// PG-006 (June 2026): the previous 4 files under
// internal/platform/httpserver/middleware/admin_token.go + middleware_middleware.go +
// middleware_feature_flags.go + middleware_ratelimit.go reached
// through 2 concrete internal/platform/* types
// (*config.Config + internal/platform/logging) plus the
// `logger.Error/Warn/Info` package-level aliases. Per AGENTS.md
// Pattern 0 + PG-006 ticket scope, every infrastructure-shaped
// dependency now flows through a typed port declared here. Concrete
// adapters live in internal/app/middleware_security_adapter.go with
// explicit compile-time `var _ <Port> = (*<Adapter>)(nil)` assertions.
//
// PG-006.1 (June 2026): the canonical concrete adapter for
// AuthSecurityPort is internal/platform/httpserver/middleware.TokenSecurityAdapter
// (re-located from pkg/middleware on June 2026 — pkg/ is leaf-only
// by AGENTS.md Pattern 4 and HTTP-middleware concrete adapters cannot
// legitimately live there). The struct is reachable from internal/api,
// cmd/admin, and internal/app without crossing layering boundaries.
// The cfg-wrapping trio that previously lived as inline auth
// adapters in api/server.go + cmd/admin/gen_api_docs.go +
// internal/app/middleware_security_adapter.go was deleted; callers
// now snapshot cfg.Security fields into
// &internal/platform/httpserver/middleware.TokenSecurityAdapter{...} literals. The
// compile-time assertion lives on the implementor side at
// internal/platform/httpserver/middleware/adapters_assertions.go (round-2
// relocation; placed there to keep ports.go cycle-free).
//
// Rule: define only methods the middleware actually calls — do NOT
// widen any port to expose the whole underlying concrete. New
// consumer sites land as additional methods, one PR at a time.
package middleware

import "context"

// AuthSecurityPort is the canonical narrow surface of *config.Config's
// Security substruct used by the auth/worker-auth/admin-token
// middlewares. The 3 methods below are exactly the ones the 3
// middlewares read on the cfg.Security path — pattern 0 minimal.
type AuthSecurityPort interface {
	// EnableAuth reports whether production deployments enforce the
	// auth checks. When false, every Auth() / RequireAdminToken() /
	// WorkerAuth() middleware short-circuits to pass-through (admin
	// context) without consulting the token values.
	EnableAuth() bool
	// AdminToken is the admin-facing secret string. Constant-time
	// compared (compareTokens) against the X-Velox-Admin-Token /
	// Authorization: Bearer header — see admin_token.go / Auth() for
	// the threat model. May be empty: callers treat that as
	// misconfigured and refuse to serve.
	AdminToken() string
	// WorkerToken is the worker-facing secret string (mirrors AdminToken
	// on the /internal/v1/* surface). WorkerAuth refuses anything that
	// isn't byte-exactly equal to this value (defense in depth against
	// accidental admin/worker token interchange).
	WorkerToken() string
}

// Compile-time assertion lives at internal/platform/httpserver/middleware/adapters_assertions.go (round-2 relocation to keep ports.go cycle-free).

// RateLimitPort is the canonical narrow surface of *config.Config's
// Security substruct used by the rate-limit middleware. The 2 methods
// below are exactly the ones RateLimit reads.
type RateLimitPort interface {
	// RateLimitEnabled reports whether the per-IP token-bucket limiter
	// is wired. When false, RateLimit returns a no-op handler.
	RateLimitEnabled() bool
	// RateLimitRequests is the per-window fill quota.
	RateLimitRequests() int
}

// FeatureFlagsPort is the canonical narrow surface of *config.Config's
// Features substruct used by the per-feature gate middlewares. The N
// methods below mirror the bool flags currently read on
// `cfg.Features.<X>Enabled`. Each per-middleware factory
// (ArtlistEnabled, ScriptClipsEnabled, ...) keeps a
// one-method-port in the calling site — but the canonical reader is
// the same `FeatureFlagsPort` so a single adapter can satisfy the
// whole family.
type FeatureFlagsPort interface {
	ArtlistEnabled() bool
	ScriptClipsEnabled() bool
	// ScriptImagesEnabled recorded here so the future ScriptImagesEnabled
	// gate middleware lands via a new one-method port without altering
	// this surface.
}

// ── M2M (machine-to-machine) client auth port ───────────────────────
//
// The jobClientAuthMiddleware (see internal/platform/httpserver/
// middleware/middleware_m2m.go) gates POST /api/v1/jobs and
// GET /api/v1/jobs/:id on a per-client secret model distinct from the
// single shared admin/worker tokens of AuthSecurityPort. The remote
// computer (PipelineGen / Agent / second PC) submits a Bearer
// VELOX_M2M_SECRET; the Master resolves it to a registered client row
// (client_id, scopes, quota, rate limit) and authorizes the specific
// scope each route requires (jobs.submit / jobs.read).
//
// The plaintext secret is NEVER stored: only its SHA-256 hex digest
// is persisted (m2m_clients.secret_hash). The port's
// HashClientSecret is the single canonical hash function so the
// admin key-creation endpoint and the runtime auth path agree on
// the digest shape. Hashing is deliberately SHA-256 (not Argon2) in
// the first pass: the secret is a high-entropy random string, so a
// plain hash is sufficient and matches the kernel/digest SSOT the
// rest of the project already uses. A future hardening pass may
// swap the algorithm behind this single method.
//
// Rule: define only methods the middleware actually calls — do NOT
// widen any port to expose the whole underlying concrete. New
// consumer sites land as additional methods, one PR at a time.

// M2MSecurityPort is the canonical narrow surface the
// jobClientAuthMiddleware reads. The 3 methods are exactly what the
// middleware needs to decide whether a request is authorized on the
// M2M job surface: an on/off switch, the hash primitive, and the
// client lookup.
type M2MSecurityPort interface {
	// EnableM2M reports whether production deployments enforce the M2M
	// client-secret checks on the /api/v1/jobs surface. When false,
	// jobClientAuthMiddleware short-circuits to pass-through (admin
	// context) so dev/test/E2E fixtures that have not provisioned an
	// m2m_clients row still work. Mirrors EnableAuth on
	// AuthSecurityPort for the same dev-loop reason.
	EnableM2M() bool
	// HashClientSecret returns the canonical digest of a plaintext
	// client secret. It is the single SSOT so the key-creation admin
	// endpoint (stores secret_hash) and the runtime auth path (hashes
	// the Bearer before LookupClient) agree on the digest shape.
	// Callers MUST NOT roll their own sha256.Sum256 for secrets.
	HashClientSecret(plaintext string) string
	// LookupClient resolves a client_id by its secret digest. Returns
	// the registered M2MClient (with scopes/enabled/quota) or
	// (nil, nil) when no row matches. A non-nil error means the store
	// was unreachable; the middleware treats that as 500 (fail-closed).
	LookupClient(ctx context.Context, secretHash string) (*M2MClient, error)
}

// M2MClient is the in-memory projection of a registered m2m_clients row
// that the middleware needs to authorize a request. It is the canonical
// transport shape across the port boundary — the concrete SQLite store
// maps its internal row struct into this value.
//
// Scopes is the parsed set of granted scope strings (e.g.
// {"jobs.submit", "jobs.read"}). Enabled is false when an operator has
// disabled the client without deleting it. ClientID is the stable
// identifier used for idempotency-key uniqueness and quota accounting.
type M2MClient struct {
	ClientID string
	Scopes   []string
	Enabled  bool
}

// HasScope reports whether the client was granted the given scope.
// Mirrors the set-membership check the middleware's requireScope helper
// performs. Exported so the concrete store and tests share the same
// semantics; the middleware reads scopes directly via the slice for
// the constant-time-irrelevant membership test (scope grants are not
// timing-sensitive: they are configured, not probed).
func (c *M2MClient) HasScope(scope string) bool {
	if c == nil {
		return false
	}
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Compile-time assertion lives at internal/platform/httpserver/middleware/adapters_assertions.go (round-2 relocation to keep ports.go cycle-free).
