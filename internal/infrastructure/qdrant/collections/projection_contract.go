package collections

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// ProjectionPopulateFunc fills a prepared physical collection. The manager
// owns collection creation and lifecycle state; the callback owns the
// canonical index writer/rebuild operation. It runs before validation and
// alias activation.
type ProjectionPopulateFunc func(context.Context, string) error

// ProjectionManager is the single owner of the Qdrant projection lifecycle.
// Implementations must never expose an alias mutation that bypasses these
// state transitions.
type ProjectionManager interface {
	Build(context.Context, string, string, int64) error
	BuildProjection(context.Context, string, string, int64) error
	BuildProjectionWith(context.Context, string, string, int64, ProjectionPopulateFunc) error
	Validate(context.Context, string, int64, int) (*schema.SwitchReport, error)
	ValidateProjection(context.Context, string, int64, int) (*schema.SwitchReport, error)
	Activate(context.Context, string, int64) error
	ActivateProjection(context.Context, string, int64) error
	Rollback(context.Context, string, string) error
	RollbackProjection(context.Context, string, string) error
	Rebuild(context.Context, string, string, int64, ProjectionPopulateFunc) error
	RebuildProjection(context.Context, string, string, int64, ProjectionPopulateFunc) error
	GetStatus(string) (mediaregistry.ProjectionStatus, error)
	Projection(string) (mediaregistry.Projection, bool)
}

var _ ProjectionManager = (*CollectionManager)(nil)
