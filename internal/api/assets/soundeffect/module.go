// Package soundeffect — module.go: the single canonical Build entrypoint
// for the Soundeffect HTTP capability.
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
// This file is part of Blocco C1-Step 8 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/module_media.go::WireAssets` and threads the returned
// Descriptor into `assetsapi.Dependencies.SoundEffect` (route module
// that mounts /media/sound_effect).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7).
//
// UNIQUE TO SOUNDEFFECT (PG-003, June 2026): the package was the
// FIRST capability in the assets tree to migrate to typed
// sfxports.* ports (ClipRepositoryPort / DriveUploaderPort /
// SemanticMetadataWriterPort / DestinationResolverPort /
// DispatcherPort / PublisherPort) — the concrete
// *assets.ClipsRepository + *drive.Uploader + *semantic.MetadataWriter
// + *drive.Resolver reach-throughs are now hidden behind adapter
// structs in `internal/app/adapters_infra.go`. The Build contract
// consumes these typed-port interfaces as flat Dependencies
// fields — no concrete infrastructure type crosses the package
// boundary. This keeps the api/ layer thin (per AGENTS.md
// Pattern 0 — port abstraction layer).
//
// FASE 7 (June 2026) added the PublisherPort to the sfxports
// surface (the canonical delivery.Publisher injection for the
// Drive upload path). The Build contract consumes the Publisher
// port as an optional Dependencies field (the handler nil-checks
// it at request time and falls back to the legacy driveUploader
// path when nil — preserved for local-only test fixtures).
//
// The Build contract therefore has the most fields of any
// capability in the assets tree today (11 deps), but the
// Descriptor surface stays minimal: only `Module` + forwarder
// methods (matches the stock / voiceover precedent — no
// `Handler` / `Service` field, because soundeffect has no
// non-HTTP consumer in the codebase; /generate is the entire
// public surface, reachable only via HTTP).
package soundeffect

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Soundeffect's
// handler is the most-port-injected in the assets tree today
// (11 typed-port deps via PG-003 + FASE 7). The Build contract
// preserves the production-wiring shape so the composition
// root's adapter construction in
// `internal/app/module_media.go::WireAssets` (sfxClips /
// sfxMeta / sfxResolver / sfxDriveUp / sfxDispatcher /
// sfxPublisher + processRunnerAdapter + log) flows through
// unchanged.
//
// Mandatory fields return an error when nil; optional fields
// fall through to the handler's existing nil-tolerance
// (the Generate handler nil-checks metaWriter + driveUploader
// + publisher at request time and the dispatcher is fail-closed
// with 503 when nil — but Build is stricter: it refuses to
// construct the Descriptor when the dispatcher is nil so the
// operator sees the wiring defect at startup, not at first
// request).
//
// Logger nil → zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// ClipsRepo is the canonical sfxports.ClipRepositoryPort
	// (PG-003, June 2026). In production, the
	// sfxClipsRepoAdapter wraps *assets.ClipsRepository.
	// MANDATORY — Build returns an error when nil. The
	// handler stores the field for forward-portability even
	// though the current Generate path does not read it
	// directly (the dispatcher adapter owns the UPSERT
	// path). A nil port would surface later as a
	// confusing nil-receiver dispatch — fail at startup
	// instead.
	ClipsRepo sfxports.ClipRepositoryPort

	// F2.10: DriveUploader field RETIRED (override brutal).
	// The legacy `*drive.Uploader` plumbing via
	// sfxports.DriveUploaderPort + sfxDriveUploaderAdapter
	// is gone; the sfx Generate write path routes through
	// delivery.Publisher.Publish exclusively (Publisher field
	// below). Composition root drops the sfxDriveUploader
	// variable + the matching Build-deps field.

	// MetaWriter is the canonical
	// sfxports.SemanticMetadataWriterPort (PG-003, June 2026).
	// In production, the sfxSemanticWriterAdapter wraps
	// *semantic.MetadataWriter. OPTIONAL — the handler
	// nil-checks at request time and falls back to inline
	// tag/searchText defaults when nil.
	MetaWriter sfxports.SemanticMetadataWriterPort

	// Resolver is the canonical sfxports.DestinationResolverPort
	// (PG-003, June 2026). In production, the sfxResolverAdapter
	// wraps *drive.Resolver. MANDATORY — the handler calls
	// h.resolver.Resolve(destReq) unconditionally in Generate.
	Resolver sfxports.DestinationResolverPort

	// Dispatcher is the canonical sfxports.DispatcherPort
	// (PR 6, June 2026, codex/qdrant-api-writers-fail-closed).
	// In production, the newSfxDispatcherAdapter wraps
	// *outbox.Dispatcher. MANDATORY — Build is fail-closed
	// on nil (stricter than the handler's runtime 503
	// contract; the operator sees the missing dispatcher
	// at startup, not at first request). The runtime
	// 503 still fires for late-binding test fixtures.
	Dispatcher sfxports.DispatcherPort

	// Publisher is the canonical sfxports.PublisherPort
	// (FASE 7, June 2026, books FASE 6 → sfx FASE 7
	// migration to the canonical delivery.Publisher
	// surface; F2.10: now the sole Drive-write canal after
	// the brutal-override retirement of the legacy
	// driveUploader fallback). In production, the
	// delivery.Publisher concrete satisfies sfxports.PublisherPort
	// structurally (no sfx-specific adapter wrapper needed —
	// see module_media.go composition site). OPTIONAL —
	// the handler nil-checks at request time (publisher=nil
	// skips the Drive write path silently).
	Publisher sfxports.PublisherPort

	// SoundEffectsRootFolder is the Drive folder ID under
	// which synthesized sound effects were uploaded via
	// the legacy driveUploader path. F2.10: RETIRED —
	// empty string is now the only valid value; the sfx
	// Build ignores it; the publisher path resolves via
	// delivery.DestinationSoundEffect routing instead.
	// Kept in the struct for backwards-compat with any
	// downstream wiring that still sets it (no-op read).
	SoundEffectsRootFolder string

	// ProcessRunner is the canonical appassets.ProcessRunner
	// port (composition-root adapter wraps
	// infraassets.NewProcessRunnerAdapter). MANDATORY — the
	// handler calls h.processRunner.Run unconditionally
	// for the python synth + ffmpeg steps.
	ProcessRunner appassets.ProcessRunner

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The soundeffect capability
	// has no feature flag in production (always on) — the
	// composition root wires `func() bool { return true }`
	// (or any availability-check closure the platform team
	// prefers). MANDATORY — Build returns an error when nil
	// (so this package stays free of platform/config imports).
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a
	// plain RouteModule.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil →
	// zap.NewNop() (composition-root-friendly default).
	Logger *zap.Logger
}

// SoundeffectDescriptor is the concrete capability Descriptor
// returned by Build. It satisfies api.Descriptor via the explicit
// Module field (named, not embedded — no method-promotion
// surprises from api.Module) and forwarder methods.
//
// UNIQUE TO SOUNDEFFECT: the Descriptor does NOT expose the
// handler (matches the artlist / stock / voiceover precedent of
// dropping the explicit Handler field). There is no non-HTTP
// consumer of the soundeffect handler in the codebase —
// /generate is the entire public surface, reachable only via
// HTTP. The handler stays the internal worker captured by the
// Module closure; no caller reads a raw *Handler from outside
// the package.
type SoundeffectDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// assetsapi.Dependencies.SoundEffect.
	Module api.Module
}

// ── Module satisfaction (api.Descriptor) ────────────────────────────
// Descriptor does NOT embed Module. The explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we
// forward them by hand. (Matches the Artlist / YouTube / Clips /
// Stock / Voiceover precedent.)

// Name returns the module name ("sound-effect"). Preserved
// verbatim from the pre-Step-8 wiring so the public route
// prefix `/api/media/sound_effect/*` stays unchanged (zero-
// change-contract).
func (d *SoundeffectDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *SoundeffectDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is
// reachable only via the Module's internal closure.
func (d *SoundeffectDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Soundeffect HTTP capability from the typed
// narrow dependencies. Returns a fail-closed error when any
// mandatory dep is nil. Logger nil → zap.NewNop(). ModuleOpts
// nil → no decorators.
//
// The returned Descriptor carries the Module (routes). The HTTP
// Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller (composition root, tests,
// internal services) reads the raw Handler anywhere outside
// this function.
func Build(deps Dependencies) (api.Descriptor, error) {
	// ── Mandatory-shape validation ────────────────────────────────
	if deps.ClipsRepo == nil {
		return nil, fmt.Errorf("soundeffect.Build: ClipsRepo is required (composition root must pre-construct the sfxClipsRepoAdapter wrapping *assets.ClipsRepository)")
	}
	if deps.Resolver == nil {
		return nil, fmt.Errorf("soundeffect.Build: Resolver is required (Generate calls h.resolver.Resolve unconditionally — a nil port would NPE at first request)")
	}
	if deps.Dispatcher == nil {
		return nil, fmt.Errorf("soundeffect.Build: Dispatcher is required (PR 6 fail-closed invariant — Build is stricter than the handler's runtime 503 contract; the operator sees the missing dispatcher at startup, not at first request)")
	}
	if deps.ProcessRunner == nil {
		return nil, fmt.Errorf("soundeffect.Build: ProcessRunner is required (Generate calls h.processRunner.Run unconditionally for python synth + ffmpeg)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("soundeffect.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler has no fail-
	// closed checks (preserves the pre-Step-8 behavior for
	// direct callers that bypass Build); Build's checks above
	// are the new defensive layer.
	//
	// F2.10 (June 2026): the `deps.DriveUploader` arg was dropped
	// from NewHandler (override brutal). Publisher is now the
	// sole Drive-write canal — there is no legacy fallback
	// branch in the handler's Generate path.
	handler := NewHandler(
		deps.ClipsRepo,
		deps.MetaWriter, // nil-tolerant at request time
		deps.Resolver,
		deps.Dispatcher,
		deps.Publisher, // F2.10: nil-tolerant at request time (skips Drive write silently)
		deps.SoundEffectsRootFolder,
		deps.ProcessRunner,
		log,
	)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	mod := api.NewRouteModule(
		"sound-effect",
		deps.EnabledFunc,
		"/sound_effect",
		handler,
		log,
		deps.ModuleOpts..., // typically []ModuleOption{api.WithMiddleware(...)}
	)

	return &SoundeffectDescriptor{
		Module: mod,
	}, nil
}
