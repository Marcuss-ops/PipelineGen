// Package app prepares runtime handlers after immutable services are constructed.
// It never mutates the provider registry and performs no provider registration.
package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/api/mediasearch"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/api/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"go.uber.org/zap"
)

// applyLateBindings is retained as an orchestration name for a pure handler
// preparation phase. Provider adapters and descriptor-owned providers have
// already been registered and frozen before this function is called.
func applyLateBindings(_ *module.Registry, log *zap.Logger, root *wiring.ComposeRoot, regWiring *RegistryWiring, crossStep registryCrossStepState) (PreparedCapabilities, error) {
	prepared := PreparedCapabilities{}
	if root.Outbox != nil && root.Outbox.EventsRepo != nil {
		regWiring.OutboxHandler = outboxapi.NewHandler(newOutboxMonitorAdapter(root.Outbox.EventsRepo), log)
	}
	if root.Process != nil && root.Process.VectorSvc != nil && root.AI != nil && root.AI.OllamaClient != nil {
		var searchAgg mediasearchapi.AggregatorSearcher
		if crossStep.SearchAggregator != nil {
			searchAgg = crossStep.SearchAggregator
		}
		regWiring.MediasearchHandler = mediasearchapi.NewHandler(mediasearchapi.WireParams{
			Aggregator: searchAgg, SemanticReady: WireMediasearchReadiness(root, searchAgg), Log: log,
		})
	}
	if crossStep.ScriptAssetsModule != nil {
		prepared.HTTPModules = append(prepared.HTTPModules, TrackedHTTPModule{Module: crossStep.ScriptAssetsModule, Point: "register.ScriptAssets"})
	}
	return prepared, nil
}
