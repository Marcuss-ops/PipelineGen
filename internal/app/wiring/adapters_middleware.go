// Package app — adapters_middleware.go: middleware rate-limit + feature-flag
// adapters and the doctor-config snapshot factory.
//
// Extracted from adapters_infra.go per AGENTS.md Pattern 5
// (PR-ADAPTERS-SPLIT, July 2026).
package wiring

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	mw "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// ── DoctorConfig snapshot factory ────────────────────────────────────────────

// doctorConfigFrom reads the diagnostic-relevant fields off *config.Config
// and packs them into a value-typed snapshot. Eager path resolution
// (AssetsPath(), ImagesPath(), TempDir() etc.) means the handler holds
// plain strings, not method receivers — easier to test, easier to fake.
// Returns the zero-value DoctorConfig if cfg is nil so callers don't need
// to nil-check before passing it into NewModule.
func doctorConfigFrom(cfg *config.Config) systemapi.DoctorConfig {
	if cfg == nil {
		return systemapi.DoctorConfig{}
	}
	return systemapi.DoctorConfig{
		DataDir:                   cfg.Storage.DataDir,
		AssetsPath:                cfg.Storage.AssetsPath(),
		ImagesPath:                cfg.Storage.ImagesPath(),
		TempPath:                  cfg.Storage.TempPath(),
		AnimationsPath:            cfg.Storage.AnimationsPath(),
		YoutubeClipsPath:          cfg.Storage.YoutubeClipsPath(),
		PythonScriptsDir:          cfg.Paths.PythonScriptsDir,
		GoogleAccountingEnabled:   cfg.GoogleAccounting.Enabled,
		GoogleAccountingServerURL: cfg.GoogleAccounting.ServerURL,
	}
}

// ── Middleware rate-limit adapter (delegating to canonical) ──────────────────
//
// CLEANUP (September 2026): the *middlewareRateLimitAdapter inline struct was
// removed. The canonical concrete is internal/platform/httpserver/middleware.
// RateLimitAdapter (snapshot-immutable, nil-receiver-safe). The local helpers
// below snapshot cfg.Security.* into that canonical literal so the wiring layer
// keeps no duplicate struct — one canonical owner per fact (godlike/06 SSOT).

func newMiddlewareRateLimitAdapter(cfg *config.Config) middleware.RateLimitPort {
	if cfg == nil {
		return nil
	}
	return &mw.RateLimitAdapter{
		Enabled:  cfg.Security.RateLimitEnabled,
		Requests: cfg.Security.RateLimitRequests,
	}
}

// ── Middleware feature-flags adapter (delegating to canonical) ───────────────
//
// CLEANUP (September 2026): same delegation pattern for the feature-flags
// surface. The canonical is middleware.FeatureFlagsAdapter.

func newMiddlewareFeatureFlagsAdapter(cfg *config.Config) middleware.FeatureFlagsPort {
	if cfg == nil {
		return nil
	}
	return &mw.FeatureFlagsAdapter{
		Artlist:     cfg.Features.ArtlistEnabled,
		ScriptClips: cfg.Features.ScriptClipsEnabled,
	}
}
