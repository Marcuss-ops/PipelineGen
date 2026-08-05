// Package workerruntime — config.go (P1-3, June 2026).
//
// LoadConfig is the canonical entry for the worker's configuration.
// Behaviour pinned from the pre-P1-3 cmd/worker/main.go:
//
//   - Uses config.GetFromPath (NOT config.Get) so a typo in
//     config.yaml does NOT silently fall back to defaults (that
//     was the audit-bug closed in P0-5).
//   - Config.GetFromPath returning nil cfg is treated as a hard
//     error rather than a default-loaded config.
//   - cfg.Validate() failure is treated as a hard error so the
//     worker exits non-zero BEFORE any HTTP / disk / DB side-effect.
//
// Errors wrap the underlying cause with oper@or-readable context
// so the worker log line carries the cfg path + validation reason.
package workerruntime

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// LoadConfig reads YAML configuration from cfgPath, validates it,
// and returns the canonical *config.Config. On any failure (read
// error, nil cfg from a non-existent path, validation error) the
// returned error is non-nil and the caller surfaces it via
// log.Fatal so the worker exits non-zero before touching the
// network or filesystem.
//
// Per P0-5 fail-fast rule: pre-P1-3 main.go's `os.Exit(2)` is
// preserved here via Run()'s log-Fatal path — no silent fall-back
// to defaults.
func LoadConfig(cfgPath string) (*config.Config, error) {
	resolved, err := config.GetResolvedFromPath(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config from %q: %w", cfgPath, err)
	}
	if resolved == nil {
		return nil, fmt.Errorf("nil resolved config from %q", cfgPath)
	}
	return resolved.View(), nil
}
