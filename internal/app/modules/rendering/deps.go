package rendering

// deps.go owns the typed input contracts of the rendering module.
//
// NewHandlerSet took five positional arguments: two overlay handles, a GPU
// gate, a media prober and a version string. The last one is the only plain
// string in a list of typed ports, which is why an accidental reorder used to
// surface as a wrong version rather than a compile error (BON-03 step B).

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

// HandlerSetDeps is the input contract of NewHandlerSet. All four ports are
// mandatory; Version may be empty and falls back to "renderinggen-dev".
type HandlerSetDeps struct {
	Cache    *overlays.Cache
	Renderer overlays.Renderer
	Gate     *overlays.GPUGate
	Prober   capmodel.MediaProber
	Version  string
}
