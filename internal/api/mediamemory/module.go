// Package mediamemory (api) — module.go is the canonical wrapper
// that adapts the mediamemory.Handler into the api.Module contract
// (see api/module_descriptor.go::Descriptor).
//
// godlike/06 SSOT (Capability Standard contract per
// api/module_descriptor.go): every capability exposes exactly one
// build function (Build(deps) -> api.Descriptor). The Descriptor
// embeds Module so legacy route-only implementations satisfy the
// interface for free (no late-binding adapters, no setters).
//
// godlike/06 SSOT (composition root is the only constructor): this
// file does NOT call NewHandler internally; it accepts the
// pre-built Handler from the composition root (forward-prevention:
// avoid any init-time wiring phase).
//
// godlike/07 NO-FAKE-AVAILABILITY: unavailable capabilities are
// not registered. The composition root passes only wired handlers.
package mediamemory

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Module is the canonical api.Module wrapper around mediamemory.Handler.
//
// It implements api.Module (Name / Enabled / RegisterRoutes).
// It does NOT implement DescriptorLifecycle / DescriptorJobs /
// DescriptorProviders — those optional slots are NOT consumed by
// mediamemory (no background goroutines, no worker handlers, no
// asset-provider catalog entries registered by this capability).
//
// godlike/06 SSOT invariant: mediamemory NEVER registers itself as
// a catalog provider. Catalog providers are owned by
// internal/application/assets/providers (composition root wires
// `artlist`, `youtube`, `images` etc.); mediamemory only CONSUMES
// their search fan-out.
type Module struct {
	name    string
	enabled func() bool
	handler *Handler
	log     *zap.Logger
}

// NewModule constructs the api.Module wrapper. The handler argument
// must be non-nil for a registered capability; nil handlers are
// skipped by RegisterRoutes.
func NewModule(name string, enabled func() bool, handler *Handler, log *zap.Logger) *Module {
	return &Module{
		name:    name,
		enabled: enabled,
		handler: handler,
		log:     log,
	}
}

// Name returns the canonical module name. Composition root uses
// this as the lookup key when wiring the api.Registry.
func (m *Module) Name() string {
	return m.name
}

// Enabled reports whether the module is active. If the closure is
// nil, the wrapper falls back to "registered iff handler is non-nil".
func (m *Module) Enabled() bool {
	if m.enabled == nil {
		return m.handler != nil
	}
	return m.enabled()
}

// RegisterRoutes mounts the mediamemory surface under
// /api/media-memory (godlike/06 SSOT: the canonical route prefix is
// OWNED by handler.go::RegisterRoutes; this wrapper MUST NOT
// re-prefix "/media-memory" again or the routes become
// /api/media-memory/media-memory/... — broken at the gateway).
//
// godlike/06 SSOT (composition-root contract): the composition
// root mounts this Module under /api (e.g. via api.Registry) so the
// full path is /api/media-memory/... The wrapper hands the
// already-prefixed group to the Handler verbatim.
//
// godlike/07 NO-FAKE-AVAILABILITY: when the handler is nil,
// RegisterRoutes skips the capability rather than mounting a
// placeholder route.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	if m.handler == nil {
		if m.log != nil {
			m.log.Warn("mediamemory: handler is nil, skipping route registration (Phase 1.1 skeleton)",
				zap.String("module", m.name))
		}
		return
	}
	// NO additional Group("/media-memory") here — handler.go owns
	// the canonical route prefix (godlike/06 SSOT).
	m.handler.RegisterRoutes(rg)
}

// Build is the canonical Capability Standard entrypoint. The
// composition root calls Build(deps) once during wire-up; the
// returned Descriptor is registered into api.Registry.
//
// Build wraps the already-wired handler supplied by the composition
// root; an unavailable handler is not exposed as a route.
func Build(handler *Handler, log *zap.Logger) api.Descriptor {
	m := NewModule("mediamemory", nil, handler, log)
	return api.AsDescriptor(m)
}

// Compile-time assertion: Module satisfies api.Module.
var _ api.Module = (*Module)(nil)
