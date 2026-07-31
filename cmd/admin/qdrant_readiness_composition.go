package main

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	app "github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// appInitCompositionForReadiness is the production bridge: it calls
// the canonical app.InitComposition + app.WireRegistry constructors
// (the same constructors cmd/server/main.go uses) and translates the
// *app.ComposeRoot + *app.RegistryWiring into the readiness-side
// *compositionRoot struct.
//
// Returns nil + non-nil error when InitComposition OR WireRegistry
// fails. The readiness runQdrantReadiness caller handles nil root by
// failing every production-shaped check.
func appInitCompositionForReadiness(ctx context.Context, cfg *config.Config, log *zap.Logger) (*compositionRoot, func(), error) {
	// Step 1: app.InitComposition returns the production *ComposeRoot
	// tree (DriveBundle, RepoBundle, ProcessBundle, OutboxBundle,
	// etc.). It also constructs the canonical *outboxevents.Pool and
	// migrates the SQLite DB. Signature: (cfg, log) -> (root, jobs, cleanup, err).
	prodRoot, _, cleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return nil, func() {}, fmt.Errorf("app.InitComposition: %w", err)
	}
	if prodRoot == nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("app.InitComposition returned nil root without error")
	}

	// Step 2: app.WireRegistry constructs the gin routes / middleware
	// layer, including the production outbox + mediasearch handlers
	// that the readiness-gate's real_routes_present check pulls
	// through api.Router.SetOutboxHandler / SetMediasearchHandler.
	// Signature: (ctx, cfg, log, root) -> (*RegistryWiring, error).
	registryWiring, err := app.WireRegistry(prodRoot.Ctx, cfg, log, prodRoot)
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("app.WireRegistry: %w", err)
	}
	if registryWiring == nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("app.WireRegistry returned nil wiring without error")
	}

	// Step 3: translate the bundle tree + registry wiring into the
	// readiness-side structural view. Every field below is a
	// PRODUCTION CONCRETE pointer / interface; nil-checks at the
	// read sites are the production-shape invariant.
	return &compositionRoot{
		Dispatcher:         prodRoot.Outbox.Dispatcher,
		EventsPool:         prodRoot.Outbox.EventsPool,
		OutboxHandler:      registryWiring.OutboxHandler,
		MediasearchHandler: registryWiring.MediasearchHandler,
		ClipsRepo:          prodRoot.Repos.ClipsRepo,
		QdrantClient:       prodRoot.Process.QdrantClient,
		SemanticSearch:     registryWiring.SearchFanOut,
	}, cleanup, nil
}
