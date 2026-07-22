// Package channels — module.go: the single canonical Build entrypoint
// for the channels capability. P1.6 (June 2026): moved from
// internal/application/channels/ to internal/api/channels/ to honour
// the api → application layering.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The result is complete: missing mandatory dependencies return an
// error during composition; they must not create partially
// initialized services. Once Build returns, the descriptor is ready
// to be registered into the api.Registry.
//
// Used by the composition root at internal/app/registry.go::WireRegistry.
// One-shot CLI callers
// (cmd/admin/backfill_monitored_sources_to_category_channels.go) bypass the
// registry and use NewService (service.go) directly — Build is the
// registry path. The ChannelsDescriptor returned here exposes the
// underlying Service so non-HTTP callers have a typed seam without
// needing to re-construct the use case layer.
package channels

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appchannels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Each field is
// optional at the type level (nil-tolerant) — but Repository and
// Logger have well-defined fallbacks documented on the Build
// function.
type Dependencies struct {
	// Repository is the persistence port the capability consumes.
	// Build wraps the concrete *channels.ChannelsRepository in a
	// RepositoryAdapter before this point (the composition root's
	// job — internal/application/* never imports
	// internal/infrastructure/*). Passing nil here is an explicit
	// error: the capability cannot operate without persistence.
	Repository appchannels.Repository

	// Logger is the canonical structured logger. nil is replaced
	// with zap.NewNop() so wiring sites do not need to nil-check.
	Logger *zap.Logger
}

// ChannelsDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module
// field (named, not embedded — no method promotion surprises from
// api.Module). Service is exposed for non-HTTP callers (cmd/admin)
// so they can drive the capability without re-constructing the use
// case layer.
type ChannelsDescriptor struct {
	Module api.Module
	// Service exposes the canonical use-case orchestrator to typed,
	// non-HTTP callers registered by the composition root.
	Service *appchannels.Service
}

// ── Module satisfaction (api.Descriptor interface) ───────────
// Descriptor embeds Module; the explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we
// forward them by hand. Round-3 reviewer fix (option b): keep the
// named field for callers reading the struct, while preserving
// interface satisfaction via explicit delegation.

func (c *ChannelsDescriptor) Name() string {
	return c.Module.Name()
}
func (c *ChannelsDescriptor) Enabled() bool {
	return c.Module.Enabled()
}
func (c *ChannelsDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	c.Module.RegisterRoutes(rg)
}

// Build constructs the channels capability: Repository → Service →
// HTTP Handler → api.Module. The returned Descriptor carries the
// Service slot so non-HTTP callers have direct access to the
// use-case layer.
//
// Returns an error when Repository is nil: the capability cannot
// operate without persistence. Logger is replaced with zap.NewNop()
// when nil so wiring sites do not need to nil-check.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Repository == nil {
		return nil, fmt.Errorf("channels.Build: Repository is required")
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	svc := appchannels.NewService(deps.Repository, log)
	handler := NewHandler(svc, log)

	return &ChannelsDescriptor{
		Module: api.NewRouteModule(
			"channels",
			func() bool { return true },
			"/channels",
			handler,
			log,
		),
		Service: svc,
	}, nil
}
