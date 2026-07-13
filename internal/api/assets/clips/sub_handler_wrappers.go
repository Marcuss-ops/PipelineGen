// Package clips — sub-handler wrappers for the Wave 4 sub-descriptor
// split (catalog, ingest, processing, publication, indexing, operations,
// bulk).
//
// Each wrapper implements submodule.RouteRegistrar and exposes only the
// routes that belong to its sub-descriptor. The wrappers keep the
// existing sub-handler implementations in place while allowing the
// parent clips module to present each cluster as a standalone
// api.Descriptor.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/submodule"
	"github.com/gin-gonic/gin"
)

// Ensure the wrappers satisfy the generic sub-descriptor contract.
var _ submodule.RouteRegistrar = (*catalogRegistrar)(nil)
var _ submodule.RouteRegistrar = (*ingestRegistrar)(nil)
var _ submodule.RouteRegistrar = (*processingRegistrar)(nil)
var _ submodule.RouteRegistrar = (*publicationRegistrar)(nil)
var _ submodule.RouteRegistrar = (*indexingRegistrar)(nil)
var _ submodule.RouteRegistrar = (*operationsRegistrar)(nil)
var _ submodule.RouteRegistrar = (*bulkRegistrar)(nil)

// catalogRegistrar owns read/search routes.
type catalogRegistrar struct {
	search *SearchHandler
	ops    *OpsHandler
	h      *Handler
	idem   gin.HandlerFunc
}

func (w *catalogRegistrar) RegisterRoutes(r *gin.RouterGroup) {
	w.search.RegisterRoutes(r, w.idem)

	// Folder query routes (read-only)
	r.GET("/:source/folders", w.ops.ListFolders)
	r.GET("/:source/folders/:id", w.ops.FolderStatus)
	r.GET("/:source/folders/:id/children", w.ops.GetFolderChildren)
	r.GET("/:source/tree", w.ops.GetTree)
	r.GET("/:source/breadcrumb", w.ops.GetBreadcrumb)

	// Duplicate lookup (write, idem-protected)
	r.POST("/:source/clips/:id/duplicates", w.idem, w.h.FindDuplicates)
}

// ingestRegistrar owns clip ingestion routes.
type ingestRegistrar struct {
	ingest *IngestHandler
	idem   gin.HandlerFunc
}

func (w *ingestRegistrar) RegisterRoutes(r *gin.RouterGroup) {
	w.ingest.RegisterRoutes(r, w.idem)
}

// processingRegistrar owns media processing routes.
type processingRegistrar struct {
	nonops *nonops.NonOpsHandler
	idem   gin.HandlerFunc
}

func (w *processingRegistrar) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/:source/clips/:id/reprocess", w.idem, w.nonops.ReprocessClip)
	r.POST("/enrich", w.idem, w.nonops.EnrichMedia)
}

// publicationRegistrar owns publication/retrieval routes.
type publicationRegistrar struct {
	h    *Handler
	idem gin.HandlerFunc
}

func (w *publicationRegistrar) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/:source/clips/:id/download", w.idem, w.h.DownloadClip)
	r.POST("/:source/clips/:id/reupload", w.idem, w.h.ReuploadClip)
}

// indexingRegistrar owns clip indexing routes.
type indexingRegistrar struct {
	nonops *nonops.NonOpsHandler
	idem   gin.HandlerFunc
}

func (w *indexingRegistrar) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/:source/clips/:id/reindex", w.idem, w.nonops.ReindexClip)
	r.POST("/enrich/batch", w.idem, w.nonops.BatchReindex)
}

// operationsRegistrar owns operational routes.
type operationsRegistrar struct {
	ops    *OpsHandler
	nonops *nonops.NonOpsHandler
	idem   gin.HandlerFunc
}

func (w *operationsRegistrar) RegisterRoutes(r *gin.RouterGroup) {
	// Integrity routes
	r.POST("/:source/clips/:id/verify", w.idem, w.ops.VerifyClip)
	r.POST("/:source/clips/:id/fix-hash", w.idem, w.ops.HandleFixHash)

	// Maintenance routes
	r.DELETE("/:source/clips/:id", w.idem, w.ops.TrashClip)
	r.POST("/:source/reconcile", w.idem, w.ops.Reconcile)
	r.POST("/:source/cleanup", w.idem, w.ops.Cleanup)
	r.POST("/:source/folders/:id/manifest", w.idem, w.ops.RegenerateManifest)
	r.DELETE("/:source/folders/:id", w.idem, w.ops.TrashFolder)

	// Bulk tag routes
	r.POST("/:source/bulk/tags/add", w.idem, w.nonops.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", w.idem, w.nonops.BulkRemoveTags)
}

// bulkRegistrar owns the bulk upload route.
type bulkRegistrar struct {
	bulk *BulkUploadTransport
	idem gin.HandlerFunc
}

func (w *bulkRegistrar) RegisterRoutes(r *gin.RouterGroup) {
	w.bulk.RegisterRoutes(r, w.idem)
}
