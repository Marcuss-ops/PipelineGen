package httpserver

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type runtimeTestModule struct{ name string }

func (m *runtimeTestModule) Name() string                    { return m.name }
func (m *runtimeTestModule) Enabled() bool                   { return true }
func (m *runtimeTestModule) RegisterRoutes(*gin.RouterGroup) {}

type runtimeTestJobs struct{ handlers []JobHandlerDescriptor }

func (j runtimeTestJobs) JobHandlers() []JobHandlerDescriptor { return j.handlers }

func TestValidateRuntimeModuleRejectsDuplicateRoutesAndJobs(t *testing.T) {
	mod := &runtimeTestModule{name: "clips"}
	err := ValidateRuntimeModule(RuntimeModule{
		Name: "assets",
		Routes: []RuntimeRouteDescriptor{
			{Capability: "assets", Module: mod},
			{Capability: "assets", Module: mod},
		},
	})
	require.ErrorContains(t, err, "duplicate route module")

	err = ValidateRuntimeModule(RuntimeModule{
		Name:        "assets",
		Routes:      []RuntimeRouteDescriptor{{Module: mod}},
		JobHandlers: []JobHandlerDescriptor{{Type: "clips.upload", Handler: func() {}}, {Type: "clips.upload", Handler: func() {}}},
	})
	require.ErrorContains(t, err, "duplicate job type")
}

func TestRegistryRuntimeModuleSameInstanceIsIdempotent(t *testing.T) {
	reg := NewRegistry()
	mod := &runtimeTestModule{name: "clips"}
	descriptor, err := RuntimeModuleFor("assets", "test.assets", mod)
	require.NoError(t, err)
	require.NoError(t, reg.RegisterRuntimeModule(descriptor))
	require.NoError(t, reg.RegisterRuntimeModule(descriptor))
	require.Len(t, reg.RuntimeModules(), 1)
	require.Len(t, reg.GetEnabled(), 1)
}

func TestRegisterRuntimeModuleRejectsDuplicateBeforeMutation(t *testing.T) {
	reg := NewRegistry()
	first := &runtimeTestModule{name: "first"}
	second := &runtimeTestModule{name: "first"}
	firstDescriptor, err := RuntimeModuleFor("assets", "first", first)
	require.NoError(t, err)
	require.NoError(t, reg.RegisterRuntimeModule(firstDescriptor))

	err = reg.RegisterRuntimeModule(RuntimeModule{
		Name:   "other",
		Routes: []RuntimeRouteDescriptor{{Module: second}},
	})
	require.ErrorContains(t, err, `module "first" already registered`)
	require.Len(t, reg.GetEnabled(), 1)
	require.Len(t, reg.RuntimeModules(), 1)
}

func TestRegisterRuntimeJobHandlersUsesDescriptorPort(t *testing.T) {
	registrar := &recordingJobRegistrar{}
	descriptor := runtimeTestJobs{handlers: []JobHandlerDescriptor{{Type: "clips.upload", Handler: func() {}}}}
	require.NoError(t, RegisterRuntimeJobHandlers(registrar, descriptor))
	require.Equal(t, []string{"clips.upload"}, registrar.types)
}

type recordingJobRegistrar struct{ types []string }

func (r *recordingJobRegistrar) RegisterHandler(jobType string, _ any) error {
	r.types = append(r.types, jobType)
	return nil
}
