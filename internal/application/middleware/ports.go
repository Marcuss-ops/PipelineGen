// Package middleware (ports) — typed application-layer ports that the
// API middleware package depends on.
//
// PG-006 (June 2026): the previous 4 files under
// internal/api/middleware/admin_token.go + middleware_middleware.go +
// middleware_feature_flags.go + middleware_ratelimit.go reached
// through 2 concrete internal/infrastructure/* types
// (*config.Config + internal/infrastructure/logging) plus the
// `logger.Error/Warn/Info` package-level aliases. Per AGENTS.md
// Pattern 0 + PG-006 ticket scope, every infrastructure-shaped
// dependency now flows through a typed port declared here. Concrete
// adapters live in internal/app/middleware_security_adapter.go with
// explicit compile-time `var _ <Port> = (*<Adapter>)(nil)` assertions.
//
// PG-006.1 (June 2026): the canonical concrete adapter for
// AuthSecurityPort is internal/api/middleware.TokenSecurityAdapter
// (re-located from pkg/middleware on June 2026 — pkg/ is leaf-only
// by AGENTS.md Pattern 4 and HTTP-middleware concrete adapters cannot
// legitimately live there). The struct is reachable from internal/api,
// cmd/admin, and internal/app without crossing layering boundaries.
// The cfg-wrapping trio that previously lived as inline auth
// adapters in api/server.go + cmd/admin/gen_api_docs.go +
// internal/app/middleware_security_adapter.go was deleted; callers
// now snapshot cfg.Security fields into
// &internal/api/middleware.TokenSecurityAdapter{...} literals. The
// compile-time assertion lives on the implementor side at
// internal/api/middleware/adapters_assertions.go (round-2
// relocation; placed there to keep ports.go cycle-free).
//
// Rule: define only methods the middleware actually calls — do NOT
// widen any port to expose the whole underlying concrete. New
// consumer sites land as additional methods, one PR at a time.
package middleware

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

// Compile-time assertion lives at internal/api/middleware/adapters_assertions.go (round-2 relocation to keep ports.go cycle-free).

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
