// Package clips composes the clip HTTP handlers. Application use cases are
// constructed by internal/app and injected here through handler-specific deps.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/gin-gonic/gin"
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

// Handler is a transport-only aggregate retained for route compatibility and
// thin delegators. It owns no repository, delivery or processing dependency.
type Handler struct {
	Idempotency gin.HandlerFunc

	search        *SearchHandler
	ingest        *IngestHandler
	ops           *OpsHandler
	nonops        *nonops.NonOpsHandler
	bulkTransport *BulkUploadTransport
	actions       *ActionHandler
}

func NewHandler(d Deps, idempotencyMiddleware gin.HandlerFunc) *Handler {
	idem := idempotencyMiddleware
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}
	if d.Operations.ClipOpsService == nil && d.ClipOpsService != nil {
		d.Operations.ClipOpsService = d.ClipOpsService
	}
	if d.Operations.Log == nil && d.Log != nil {
		d.Operations.Log = d.Log
	}
	return &Handler{
		Idempotency:   idem,
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
func NewHandlerStrict(d Deps, idempotencyMiddleware gin.HandlerFunc) (*Handler, error) {
	if err := nonops.ValidateNonOpsDeps(d.NonOps); err != nil {
		return nil, err
	}
	if d.Ingest.EnrichUC == nil {
		return nil, appclips.ErrEnrichDispatcherRequired
	}
	return NewHandler(d, idempotencyMiddleware), nil
}

func (h *Handler) repoForSource(source string) appclips.ClipRepositoryPort {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()
	if h.ingest != nil {
		h.ingest.RegisterRoutes(r, idem)
	}
	if h.search != nil {
		h.search.RegisterRoutes(r, idem)
	}
	if h.ops != nil {
		h.ops.RegisterRoutes(r, idem)
	}
	if h.nonops != nil {
		h.nonops.RegisterRoutes(r, idem)
	}
	if h.bulkTransport != nil {
		h.bulkTransport.RegisterRoutes(r, idem)
	}
	if h.actions != nil {
		r.POST("/:source/clips/:id/download", idem, h.actions.DownloadClip)
		r.POST("/:source/clips/:id/duplicates", idem, h.actions.FindDuplicates)
		r.POST("/:source/clips/:id/reupload", idem, h.actions.ReuploadClip)
	}
}

func (h *Handler) catalogRegistrar(idem gin.HandlerFunc) *catalogRegistrar {
	return &catalogRegistrar{search: h.search, ops: h.ops, h: h, idem: idem}
}

func (h *Handler) ingestRegistrar(idem gin.HandlerFunc) *ingestRegistrar {
	return &ingestRegistrar{ingest: h.ingest, idem: idem}
}

func (h *Handler) processingRegistrar(idem gin.HandlerFunc) *processingRegistrar {
	return &processingRegistrar{nonops: h.nonops, idem: idem}
}

func (h *Handler) publicationRegistrar(idem gin.HandlerFunc) *publicationRegistrar {
	return &publicationRegistrar{h: h, idem: idem}
}

func (h *Handler) indexingRegistrar(idem gin.HandlerFunc) *indexingRegistrar {
	return &indexingRegistrar{nonops: h.nonops, idem: idem}
}

func (h *Handler) operationsRegistrar(idem gin.HandlerFunc) *operationsRegistrar {
	return &operationsRegistrar{ops: h.ops, nonops: h.nonops, idem: idem}
}

func (h *Handler) bulkRegistrar(idem gin.HandlerFunc) *bulkRegistrar {
	return &bulkRegistrar{bulk: h.bulkTransport, idem: idem}
}
