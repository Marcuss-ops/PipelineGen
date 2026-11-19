// Package app — adapters_middleware.go: middleware rate-limit + feature-flag
// adapters and the doctor-config snapshot factory.
//
// Extracted from adapters_infra.go per AGENTS.md Pattern 5
// (PR-ADAPTERS-SPLIT, July 2026).
package wiring

import (
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
		// The doctor must probe the SAME executables the runtime resolves
		// (see DoctorConfig.YtdlpPath). Resolved here so the handler keeps
		// holding plain strings and stays a thin transport.
		YtdlpPath:  cfg.External.ResolvedYtdlpPath(),
		FfmpegPath: cfg.External.FfmpegPath,
	}
}
