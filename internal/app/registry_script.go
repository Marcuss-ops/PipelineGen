// Package app — Script orchestration wrapper (PR4 split).
//
// PR4 mechanical split (June 2026): registerScripts is the
// orchestrator-side helper that bundles two script-related
// registrations:
//
//  1. wireScriptFlow (defined in wire_script.go, called here) — sets
//     up the ScriptFlow use case orchestration + handler registration.
//     This wraps the ScriptFlow use case + script-asset adapters +
//     route registration. The wiring side-effect is identical to
//     pre-PR4 (no Module assignments to wiring internally).
//  2. registerScriptHistory (relocated to registry_public_modules.go;
//     called here) — adds the script-history route module at
//     /scripts/* paths, which is shared by all script entrypoints.
//
// The PR4 spec called registerScripts as a single orchestrator call;
// this file owns that wrapper so the slim registry.go's 7-step
// orchestrator reads cleanly.
package app

import (
	"context"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// registerScripts orchestrates the /api/script/* routing surface.
// Calls wireScriptFlow (in wire_script.go) for the canonical use-case
// delegation and registerScriptHistory (in registry_public_modules.go)
// for the script-history route module.
func registerScripts(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if err := wireScriptFlow(ctx, cfg, log, root, registry); err != nil {
		return err
	}
	return registerScriptHistory(registry, log, cfg, root)
}
