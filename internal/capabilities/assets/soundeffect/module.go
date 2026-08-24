// Package soundeffect exposes the canonical Build entrypoint for the
// Soundeffect HTTP capability.
package soundeffect

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CoreDeps groups the capability's use-case and processing dependencies.
type CoreDeps struct {
	ClipsRepo     ClipRepositoryPort
	MetaWriter    SemanticMetadataWriterPort
	ProcessRunner appassets.ProcessRunner
}

// DeliveryDeps groups destination and publication dependencies.
type DeliveryDeps struct {
	Resolver               DestinationResolverPort
	Publisher              PublisherPort
	SoundEffectsRootFolder string
}

// TransportDeps groups route and dispatch wiring.
type TransportDeps struct {
	Dispatcher  DispatcherPort
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
	Delivery      DeliveryDeps
	Transport     TransportDeps
	Observability ObservabilityDeps
}

// SoundeffectDescriptor is the concrete capability descriptor returned by Build.
type SoundeffectDescriptor struct {
	Module api.Module
}

func (d *SoundeffectDescriptor) Name() string {
	return d.Module.Name()
}

func (d *SoundeffectDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

func (d *SoundeffectDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Soundeffect HTTP capability and fails closed when a
// mandatory dependency is missing.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Core.ClipsRepo == nil {
		return nil, fmt.Errorf("soundeffect.Build: ClipsRepo is required (composition root must pre-construct the sfxClipsRepoAdapter wrapping *assets.ClipsRepository)")
	}
	if deps.Delivery.Resolver == nil {
		return nil, fmt.Errorf("soundeffect.Build: Resolver is required (Generate calls h.resolver.Resolve unconditionally — a nil port would NPE at first request)")
	}
	if deps.Transport.Dispatcher == nil {
		return nil, fmt.Errorf("soundeffect.Build: Dispatcher is required (PR 6 fail-closed invariant — Build is stricter than the handler's runtime 503 contract; the operator sees the missing dispatcher at startup, not at first request)")
	}
	if deps.Core.ProcessRunner == nil {
		return nil, fmt.Errorf("soundeffect.Build: ProcessRunner is required (Generate calls h.processRunner.Run unconditionally for python synth + ffmpeg)")
	}
	if deps.Transport.EnabledFunc == nil {
		return nil, fmt.Errorf("soundeffect.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	log := deps.Observability.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewHandler(HandlerDeps{
		ClipsRepo:              deps.Core.ClipsRepo,
		MetaWriter:             deps.Core.MetaWriter,
		Resolver:               deps.Delivery.Resolver,
		Dispatcher:             deps.Transport.Dispatcher,
		Publisher:              deps.Delivery.Publisher,
		SoundEffectsRootFolder: deps.Delivery.SoundEffectsRootFolder,
		ProcessRunner:          deps.Core.ProcessRunner,
		Log:                    log,
	})

	mod := api.NewRouteModule(
		"sound-effect",
		deps.Transport.EnabledFunc,
		"/sound_effect",
		handler,
		log,
		deps.Transport.ModuleOpts...,
	)

	return &SoundeffectDescriptor{Module: mod}, nil
}
