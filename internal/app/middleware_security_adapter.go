// Package app — adapters for the middleware typed ports (PG-006, June 2026).
//
// The previous 4 middleware files under internal/api/middleware/**
// reached through concrete internal/infrastructure/types: *config.Config
// + the `logger.Error/Warn/Info` package-level functions from
// internal/infrastructure/logging. Per AGENTS.md Pattern 0 the
// composition root (this file) is the SINGLE allowed caller of those
// concrete types; the api/ layer is now strictly
// application+domain+pkg-only.
//
// Each adapter carries a `var _ middleware.<Port> = (*<Adapter>)(nil)`
// assertion so future port drift is caught at compile time.
// Nil-tolerant constructors preserve the `if h.xy != nil` discipline
// callers have relied on since the Wave 14 grandfathered era.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// middlewareSecurityAdapter wraps *config.Config to satisfy
// middleware.AuthSecurityPort. Each method exposes exactly the field
// the middleware reads (Pattern 0: minimal — never return the whole
// *Config).
type middlewareSecurityAdapter struct {
	cfg *config.Config
}

// Compile-time assertion: middlewareSecurityAdapter satisfies middleware.AuthSecurityPort.
var _ middleware.AuthSecurityPort = (*middlewareSecurityAdapter)(nil)

func newMiddlewareSecurityAdapter(cfg *config.Config) middleware.AuthSecurityPort {
	if cfg == nil {
		return nil
	}
	return &middlewareSecurityAdapter{cfg: cfg}
}

func (a *middlewareSecurityAdapter) EnableAuth() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Security.EnableAuth
}

func (a *middlewareSecurityAdapter) AdminToken() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Security.AdminToken
}

func (a *middlewareSecurityAdapter) WorkerToken() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Security.WorkerToken
}

// middlewareRateLimitAdapter wraps *config.Config to satisfy
// middleware.RateLimitPort. Same one-method-per-call-site discipline.
type middlewareRateLimitAdapter struct {
	cfg *config.Config
}

// Compile-time assertion.
var _ middleware.RateLimitPort = (*middlewareRateLimitAdapter)(nil)

func newMiddlewareRateLimitAdapter(cfg *config.Config) middleware.RateLimitPort {
	if cfg == nil {
		return nil
	}
	return &middlewareRateLimitAdapter{cfg: cfg}
}

func (a *middlewareRateLimitAdapter) RateLimitEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Security.RateLimitEnabled
}

func (a *middlewareRateLimitAdapter) RateLimitRequests() int {
	if a.cfg == nil {
		return 0
	}
	return a.cfg.Security.RateLimitRequests
}

// middlewareFeatureFlagsAdapter wraps *config.Config to satisfy
// middleware.FeatureFlagsPort. The per-feature bools read on
// `cfg.Features.<X>Enabled` get one delegation method each so the
// future ScriptImagesEnabled flag lands cleanly without changing
// the port surface.
type middlewareFeatureFlagsAdapter struct {
	cfg *config.Config
}

// Compile-time assertion.
var _ middleware.FeatureFlagsPort = (*middlewareFeatureFlagsAdapter)(nil)

func newMiddlewareFeatureFlagsAdapter(cfg *config.Config) middleware.FeatureFlagsPort {
	if cfg == nil {
		return nil
	}
	return &middlewareFeatureFlagsAdapter{cfg: cfg}
}

func (a *middlewareFeatureFlagsAdapter) ArtlistEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ArtlistEnabled
}

func (a *middlewareFeatureFlagsAdapter) ScriptDocsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptDocsEnabled
}

func (a *middlewareFeatureFlagsAdapter) ScriptClipsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptClipsEnabled
}
