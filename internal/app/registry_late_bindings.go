// Package app prepares runtime handlers and provider descriptors after all
// immutable services have been constructed. Despite the legacy function name,
// this file performs no registry mutation and no required dependency injection.
package app

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/api/mediasearch"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/api/outbox"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptassets"
	"go.uber.org/zap"
)

// applyLateBindings is retained as a compatibility name for the orchestrator.
// Its behavior is now a pure preparation phase:
//   - build internal route handlers from canonical services;
//   - collect provider adapters;
//   - build descriptor-owned HTTP/provider slots;
//   - return an immutable PreparedCapabilities value.
//
// Registration, validation and freeze happen later and exclusively inside
// registerCapabilities. No object is completed through setters here.
func applyLateBindings(_ *module.Registry, log *zap.Logger, root *wiring.ComposeRoot, regWiring *RegistryWiring, crossStep registryCrossStepState) (PreparedCapabilities, error) {
	prepared := PreparedCapabilities{}

	if root.Outbox != nil && root.Outbox.EventsRepo != nil {
		outboxPort := newOutboxMonitorAdapter(root.Outbox.EventsRepo)
		regWiring.OutboxHandler = outboxapi.NewHandler(outboxPort, log)
		log.Info("QDRANT-002: outbox handler prepared for internal route mounting")
	}

	if root.Process != nil && root.Process.VectorSvc != nil && root.AI != nil && root.AI.OllamaClient != nil {
		// Consume the canonical composition-root singleton directly. This no
		// longer depends on Assets having copied the aggregator into its own
		// wiring object first, eliminating the former temporal coupling.
		var searchAgg mediasearchapi.AggregatorSearcher
		if crossStep.SearchAggregator != nil {
			searchAgg = crossStep.SearchAggregator
		}
		regWiring.MediasearchHandler = mediasearchapi.NewHandler(mediasearchapi.WireParams{
			Aggregator: searchAgg,
			Log:        log,
		})
		log.Info("QDRANT-004: mediasearch handler prepared from canonical search aggregator")
	}

	if root.Search == nil || root.Search.ProviderRegistry == nil {
		return prepared, nil
	}

	if regWiring.ArtlistSvc != nil && regWiring.ArtlistSvc.Service != nil {
		prepared.Providers = append(prepared.Providers, TrackedProviderEntry{
			Id:     "artlist",
			Kind:   ProviderKindSearch,
			Search: artlistadapter.NewAdapter(regWiring.ArtlistSvc.Service),
		})
	}
	if regWiring.YouTubeClip != nil && regWiring.YouTubeClip.Service != nil {
		prepared.Providers = append(prepared.Providers, TrackedProviderEntry{
			Id:     "youtube",
			Kind:   ProviderKindSearch,
			Search: youtubeadapter.NewAdapter(regWiring.YouTubeClip.Service),
		})
	}
	if regWiring.StockPipeline != nil && regWiring.StockPipeline.Service != nil {
		prepared.Providers = append(prepared.Providers, TrackedProviderEntry{
			Id:    "stock",
			Kind:  ProviderKindFetch,
			Fetch: stockadapter.NewAdapter(regWiring.StockPipeline.Service),
		})
	}

	descriptor, err := scriptassets.Build(scriptassets.Dependencies{Logger: log})
	if err != nil {
		return PreparedCapabilities{}, err
	}
	prepared.HTTPModules = append(prepared.HTTPModules, TrackedHTTPModule{
		Module: descriptor,
		Point:  "register.ScriptAssets",
	})
	if providerDescriptor, ok := descriptor.(module.DescriptorProviders); ok {
		prepared.ProviderDescriptors = append(prepared.ProviderDescriptors, providerDescriptor)
	} else {
		return PreparedCapabilities{}, fmt.Errorf("script-assets descriptor does not implement api.DescriptorProviders")
	}

	return prepared, nil
}
