// Package clips composes the clip HTTP handlers. Application use cases are
// constructed by internal/app and injected here through handler-specific deps.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"go.uber.org/zap"
)

// Deps mirrors the real HTTP capability boundaries. Each grouped field is the
// exact constructor contract of one independently testable handler.
type Deps struct {
	Search     SearchDeps
	Ingest     IngestDeps
	Operations OpsDeps
	NonOps     nonops.Deps
	Bulk       BulkTransportDeps
	Actions    ActionDeps

	// ClipOpsService and Log are a narrow compatibility seam for historical
	// same-package fixtures. Production composition never sets them; remove
	// after those fixtures construct Operations explicitly.
	ClipOpsService *appclips.ClipOpsService
	Log            *zap.Logger
}

// Handler is a transport-only aggregate retained for thin delegators used by
// focused handler tests. Route registration belongs to the canonical module
// descriptors, not this aggregate.
type Handler struct {
	search        *SearchHandler
	ingest        *IngestHandler
	ops           *OpsHandler
	nonops        *nonops.NonOpsHandler
	bulkTransport *BulkUploadTransport
	actions       *ActionHandler
}

func NewHandler(d Deps) *Handler {
	if d.Operations.ClipOpsService == nil && d.ClipOpsService != nil {
		d.Operations.ClipOpsService = d.ClipOpsService
	}
	if d.Operations.Log == nil && d.Log != nil {
		d.Operations.Log = d.Log
	}
	return &Handler{
		search:        NewSearchHandler(d.Search),
		ingest:        NewIngestHandler(d.Ingest),
		ops:           NewOpsHandler(d.Operations),
		nonops:        nonops.NewNonOpsHandler(d.NonOps),
		bulkTransport: NewBulkUploadTransport(d.Bulk),
		actions:       NewActionHandler(d.Actions),
	}
}

// NewHandlerStrict fails closed when the mandatory mutation and job paths are
// incomplete. The checks are performed before any route is exposed.
func NewHandlerStrict(d Deps) (*Handler, error) {
	if err := nonops.ValidateNonOpsDeps(d.NonOps); err != nil {
		return nil, err
	}
	if d.Ingest.EnrichUC == nil {
		return nil, appclips.ErrEnrichDispatcherRequired
	}
	return NewHandler(d), nil
}

func (h *Handler) repoForSource(source string) appclips.ClipRepositoryPort {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
}
