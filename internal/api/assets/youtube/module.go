// Package youtube — module.go: the canonical Build entrypoint for the
// YouTube HTTP capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// Mirrors the artlist precedent (Blocco C1-Step 3, June 2026) and the
// channels/scriptassets/generation precedents. The Descriptor wraps a
// pre-built `*youtube.Service` (already constructed by the composition
// root from `BuildDomainBundle` + `youtube.NewService(youtube.ServiceDeps)`)
// and exposes the route-only `api.Module` for the api.Registry. The
// HTTP Handler is constructed inside Build and captured by the Module's
// RegisterRoutes closure; callers (composition root, tests, internal
// services) never touch a raw `*YouTubeClipHandler` outside this package.
//
// Mandatory fields return an error when nil; optional fields fall through
// to the handler's existing nil-tolerance (each route short-circuits to
// 503 or to the appropriate sentinel response — never panic, never NPE).
// Logger nil → zap.NewNop() (composition-root-friendly default).
package youtube

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
//
// Mandatory fields (Build returns error on nil):
//   - Service         *youtube.Service            (used by GetVideoInfo, Extract, Diagnostics, SearchAdvanced, Stats)
//   - Jobs            jobservice.Service          (used by Extract enqueue path)
//   - ToolChecker     appassets.ToolChecker       (used by Diagnostics; nil-safe inside NewYouTubeClipHandler)
//   - EnabledFunc     func() bool                 (composition-root wires cfg.Features.YouTubeEnabled; keeps the
//     api package free of platform/config imports)
//
// Optional (forward through nil; handler keeps its nil-tolerance):
//   - ClipStorePort   ytports.ClipStorePort       (downstream uses — kept from the pre-Step-4 wiring; nil-tolerant
//     because newClipStoreAdapter(nil) returns a nil interface)
//   - Idempotency     gin.HandlerFunc             (wraps POST /clips/process; nil ⇒ no-op default handler)
//   - SearchAggregator *providers.SearchAggregator (routes SearchAdvanced + Stats; nil ⇒ 503 from those two routes)
//   - ModuleOpts      []api.RouteModuleOption     (typically empty for clips; bundle-specific decorators ONLY)
//   - Logger          *zap.Logger                 (nil ⇒ zap.NewNop())
type Dependencies struct {
	// Service is the canonical `*youtube.Service` already wired by the
	// composition root from `BuildDomainBundle` →
	// `youtube.NewService(youtube.ServiceDeps)`. MANDATORY.
	Service *youtube.Service

	// Jobs is the canonical jobs service used by the /extract enqueue
	// path. MANDATORY — Build returns an error when nil.
	Jobs jobservice.Service

	// ClipStorePort is the youtubeports.ClipStorePort the handler holds
	// for downstream uses (reprocess / download paths that don't go
	// through the search aggregator). OPTIONAL — `newClipStoreAdapter`
	// returns a nil interface when the underlying `*assets.ClipsRepository`
	// is nil; routes that depend on the clip-store short-circuit gracefully.
	ClipStorePort ytports.ClipStorePort

	// ToolChecker is the external-tool probe used by GET /diagnostics
	// (yt-dlp, ffmpeg, node, cookies). MANDATORY in production
	// (Diagnostics is part of the public surface and must not 500 if
	// asked); composition-root-provided, but Build surfaces a nil as a
	// fail-closed error so the operator sees the missing dep at startup
	// rather than at first diagnostics request.
	ToolChecker appassets.ToolChecker

	// Idempotency is the reusable Gin idempotency middleware wrapped
	// around POST /clips/process (the only Write route). OPTIONAL —
	// nil falls through to a no-op handler in NewYouTubeClipHandler.
	Idempotency gin.HandlerFunc

	// SearchAggregator routes SearchAdvanced + Stats via the canonical
	// providers.SearchAggregator fan-out (S3d, June 2026). OPTIONAL —
	// nil causes SearchAdvanced + Stats to return 503 at request time
	// (the composition root is required to provide one in any
	// post-Freeze configuration).
	SearchAggregator *providers.SearchAggregator

	// EnabledFunc is the closure that decides whether the module's
	// routes are mounted. The composition root wires the canonical
	// `cfg.Features.YouTubeEnabled` so this package stays free of
	// `internal/platform/config` imports. MANDATORY.
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// applied to the RouteModule at Build time. OPTIONAL — nil
	// produces a plain RouteModule with no extra middleware beyond
	// the Idempotency installation.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger; nil → zap.NewNop().
	Logger *zap.Logger
}

// YouTubeDescriptor is the concrete capability Descriptor returned by
// Build. It satisfies api.Descriptor via the explicit Module field
// (named, not embedded — no method-promotion surprises from api.Module)
// and forwarder methods. The pre-built `Service` is exposed so non-HTTP
// callers (current: late-bindings service-path for provider registry;
// future: internal services, admin tools, tests) can drive the
// capability without re-constructing the use-case layer.
//
// This matches the ArtlistDescriptor shape — Descriptor is the same
// compound "Module + (Service|JobHandlers|Providers)" contract used by
// Channels / ScriptAssets / Generation. Today the YouTube Descriptor
// exposes ONLY Module + Service (no Jobs slot, no Providers slot)
// because the YouTube capability owns no worker-handler side-effects
// and no provider-catalog side-effects of its own; the Service's
// RegisterHandler is invoked by the composition root OUTSIDE the
// Descriptor (matches the artlist pattern: `ytSvc.RegisterHandler(jobs)`
// happens at the end of `WireYouTubeClip`).
type YouTubeDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule instance)
	// the composition root registers for HTTP traffic.
	Module api.Module

	// Service is exposed for non-HTTP callers and for the
	// composition root's late-bind `ytSvc.RegisterHandler(jobs)` step.
	Service *youtube.Service
}

// ── Module satisfaction (api.Descriptor) ────────────────────────────
// Descriptor does NOT embed Module. The explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we forward
// them by hand. (Matches the Artlist / Channels / Generation precedent.)

// Name returns the module name ("clips" — preserved from the
// pre-Step-4 wiring so the api.Registry entry stays the same name for
// back-compat with any operator tools / dashboards that reference it).
func (d *YouTubeDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *YouTubeDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *YouTubeDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the YouTube HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop().
//
// The returned Descriptor carries the Module (routes) + Service
// (non-HTTP use cases + late-bind jobs.Service.RegisterHandler).
// The HTTP Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller reads the raw Handler anywhere
// outside this function (matches the Blocco C1-Step 3 artlist pattern).
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Service == nil {
		return nil, fmt.Errorf("youtube.Build: Service is required (composition root must pre-construct *youtube.Service from BuildDomainBundle + youtube.NewService)")
	}
	if deps.Jobs == nil {
		return nil, fmt.Errorf("youtube.Build: Jobs is required (the /extract enqueue path is unreachable without jobservice.Service)")
	}
	if deps.ToolChecker == nil {
		return nil, fmt.Errorf("youtube.Build: ToolChecker is required (GET /diagnostics depends on the external-tool probe — missing dep must fail closed at composition time, not at first request)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("youtube.Build: EnabledFunc is required (composition root must wire cfg.Features.YouTubeEnabled as a closure so this package stays free of platform/config imports)")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewYouTubeClipHandler(
		deps.Service,
		log,
		deps.Jobs,
		deps.ClipStorePort, // nil-tolerant — downstream routes that need it short-circuit
		deps.ToolChecker,
		deps.Idempotency,      // nil ⇒ no-op default inside NewYouTubeClipHandler
		deps.SearchAggregator, // nil ⇒ SearchAdvanced + Stats return 503
	)

	module := api.NewRouteModule(
		"clips",
		deps.EnabledFunc,
		"/clips",
		handler,
		log,
		deps.ModuleOpts..., // typically empty for the clips module
	)

	log.Info("created Clips module via youtube.Build (Blocco C1-Step 4)")

	return &YouTubeDescriptor{
		Module:  module,
		Service: deps.Service,
	}, nil
}
