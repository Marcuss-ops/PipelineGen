// Package artlist — module.go: the single canonical Build entrypoint for
// the Artlist HTTP capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The returned Descriptor is complete: missing mandatory dependencies
// return an error during composition; the capability does not create
// partially-initialized services. Once Build returns, the descriptor is
// ready to be registered into the api.Registry by the composition root.
//
// This file is part of Blocco C1-Step 3 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + future
// `internal/app/capability_registry.go` hoist site). The composition
// root consumes this Build via
// `internal/app/module_sources.go::wireArtlistModule` and registers
// the returned Module.
//
// Pattern parity with `scriptassets/module.go` (DescriptorProviders slot),
// `generation/module.go` (DescriptorJobs slot), and `channels/module.go`
// (route-only Descriptor). The Handler stays internal to the Module —
// each Descriptor's `RegisterRoutes(rg)` closure invokes it; callers
// never touch a raw `*ArtlistHandler` from outside the package.
package assets

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	artlistapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Mandatory fields
// return an error when nil; optional fields fall through to the
// handler's existing nil-tolerance (each route short-circuits to 503
// or to the appropriate sentinel response — never panic, never NPE).
//
// Logger nil → zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// Service is the canonical `*artlist.Service` already wired by
	// the composition root from the `ArtlistBundle` (clip catalog,
	// dispatcher, lifecycle, semantic enricher, …). MANDATORY —
	// Build returns an error when nil.
	Service *artlistapp.Service

	// CatalogSync handles /sync-catalogs reconciliation. OPTIONAL —
	// nil is forwarded and the handler returns 503 at request time.
	CatalogSync *catalogsync.Service

	// ClipResolver is the clipresolver port used by /recommend.
	// OPTIONAL — nil is forwarded and the handler returns 503.
	ClipResolver ClipResolverPort

	// CfgPort is the typed narrow port that exposes only the artlist
	// root folder the handler reads during request normalization
	// (RunTagRequest → normalized RootFolderID). MANDATORY — Build
	// returns an error when nil.
	CfgPort artlistapp.ArtlistConfigPort

	// EnabledFunc is the closure that decides whether the module's
	// routes are mounted. The composition root wires the canonical
	// `cfg.Features.ArtlistEnabled` (so this package never imports
	// `internal/platform/config`). MANDATORY — Build returns an
	// error when nil.
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a plain
	// RouteModule.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil → zap.NewNop().
	Logger *zap.Logger
}

// ArtlistDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module
// field (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods. The pre-built `Service` is
// exposed so non-HTTP callers (future internal services, admin
// tools, tests) can drive the capability without re-constructing
// the use-case layer (matches the Channels precedent).
type ArtlistDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule instance)
	// the composition root registers for HTTP traffic.
	Module api.Module

	// Service is exposed for non-HTTP callers (tests, admin tools,
	// future internal services).
	Service *artlistapp.Service
}

// ── Module satisfaction (api.Descriptor) ────────────────────────────
// Descriptor does NOT embed Module. The explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we
// forward them by hand. (Matches the Channels / Generation /
// ScriptAssets precedent.)

// Name returns the module name ("artlist").
func (d *ArtlistDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *ArtlistDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *ArtlistDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Artlist HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop().
//
// The returned Descriptor carries the Module (routes) + Service
// (non-HTTP use cases). The HTTP Handler is constructed here and
// captured by the Module's RegisterRoutes closure — no caller
// (composition root, tests, internal services) reads the raw Handler
// anywhere outside this function.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Service == nil {
		return nil, fmt.Errorf("artlist.Build: Service is required (composition root must pre-construct *artlist.Service from ArtlistBundle)")
	}
	if deps.CfgPort == nil {
		return nil, fmt.Errorf("artlist.Build: CfgPort is required (RunTagRequest normalization reads the artlist root folder)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("artlist.Build: EnabledFunc is required (composition root must wire cfg.Features.ArtlistEnabled as a closure so this package stays free of platform/config imports)")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewArtlistHandler(
		deps.Service,
		deps.CatalogSync,  // nil-tolerant — handler returns 503 on /sync-catalogs
		deps.ClipResolver, // nil-tolerant — handler returns 503 on /recommend
		log,
		deps.CfgPort,
	)

	module := api.NewRouteModule(
		"artlist",
		deps.EnabledFunc,
		"/artlist",
		handler,
		log,
		deps.ModuleOpts..., // typically []ModuleOption{api.WithMiddleware(...)}
	)

	return &ArtlistDescriptor{
		Module:  module,
		Service: deps.Service,
	}, nil
}
