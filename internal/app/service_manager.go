package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// initServices initializes the full service graph by delegating to three
// domain-specific composers in dependency order:
//
//  1. composeCoreInfra    — LLM, Drive, storage, media processor, vector infra
//  2. composeMediaDomain  — YouTube, voiceover, images, books
//  3. composeIntegration  — sync, jobs, realtime, script flow, deletion, lessons
//
// Each composer returns a focused struct; initServices stitches them together
// into the single *services struct expected by the rest of the app.
func initServices(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, registryWiring *RegistryWiring) (*services, error) {
	// 1. Core Infrastructure (shared dependencies)
	core, err := composeCoreInfra(ctx, cfg, dbs, log)
	if err != nil {
		return nil, err
	}

	// 2. Media Domain Services (depend on core infra)
	mediaDomain, err := composeMediaDomain(ctx, cfg, dbs, log, core)
	if err != nil {
		return nil, err
	}

	// 3. Cross-domain Integration (builds the final services struct, late-
	// binds outbox dispatcher onto stockpipeline.Service via registryWiring
	// when the registry was assembled upstream). nil registryWiring is
	// tolerated — partial deployments, test harnesses, and the legacy
	// entry points stay green by skipping the late-binding step.
	return composeIntegration(ctx, cfg, dbs, log, core, mediaDomain, registryWiring)
}
