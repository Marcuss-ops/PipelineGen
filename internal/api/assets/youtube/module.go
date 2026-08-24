// Package youtube exposes the canonical Build entrypoint for the YouTube HTTP
// capability.
package youtube

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockplan"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CoreDeps groups the primary service and execution dependencies.
type CoreDeps struct {
	Service       *youtube.Service
	Jobs          jobs.Service
	ClipStorePort ytports.ClipStorePort
	ToolChecker   appassets.ToolChecker
	StockService  *stockplan.StockService
}

// SearchDeps groups the optional search surfaces used by advanced search and
// stats routes.
type SearchDeps struct {
	Service *search.Aggregator
	FanOut  search.SearchFanOut
}

// TransportDeps groups HTTP middleware and route-module configuration.
type TransportDeps struct {
	Idempotency gin.HandlerFunc
	EnabledFunc func() bool
	ModuleOpts  []api.RouteModuleOption
}

// ObservabilityDeps groups optional diagnostics dependencies.
type ObservabilityDeps struct {
	Logger *zap.Logger
}

// Dependencies is the typed narrow input to Build. Each capability area is
// represented by a small bundle so the API module stays below the eight-field
// dependency-bag limit.
type Dependencies struct {
	Core          CoreDeps
	Search        SearchDeps
	Transport     TransportDeps
	Observability ObservabilityDeps
}

// YouTubeDescriptor is the concrete capability descriptor returned by Build.
type YouTubeDescriptor struct {
	Module  api.Module
	Service *youtube.Service
}

func (d *YouTubeDescriptor) Name() string {
	return d.Module.Name()
}

func (d *YouTubeDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

func (d *YouTubeDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the YouTube HTTP capability and fails closed when a mandatory
// dependency is missing.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Core.Service == nil {
		return nil, fmt.Errorf("youtube.Build: Service is required (composition root must pre-construct *youtube.Service from BuildDomainBundle + youtube.NewService)")
	}
	if deps.Core.Jobs == nil {
		return nil, fmt.Errorf("youtube.Build: Jobs is required (the /extract enqueue path is unreachable without jobs.Service)")
	}
	if deps.Core.ToolChecker == nil {
		return nil, fmt.Errorf("youtube.Build: ToolChecker is required (GET /diagnostics depends on the external-tool probe — missing dep must fail closed at composition time, not at first request)")
	}
	if deps.Transport.EnabledFunc == nil {
		return nil, fmt.Errorf("youtube.Build: EnabledFunc is required (composition root must wire cfg.Features.YouTubeEnabled as a closure so this package stays free of platform/config imports)")
	}

	log := deps.Observability.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewYouTubeClipHandler(
		deps.Core.Service,
		log,
		deps.Core.Jobs,
		deps.Core.ClipStorePort,
		deps.Core.ToolChecker,
		deps.Transport.Idempotency,
		deps.Search.Service,
		deps.Search.FanOut,
	)
	handler.stockService = deps.Core.StockService

	module := api.NewRouteModule(
		"clips",
		deps.Transport.EnabledFunc,
		"/clips",
		handler,
		log,
		deps.Transport.ModuleOpts...,
	)

	log.Info("created Clips module via youtube.Build (Blocco C1-Step 4)")

	return &YouTubeDescriptor{
		Module:  module,
		Service: deps.Core.Service,
	}, nil
}
