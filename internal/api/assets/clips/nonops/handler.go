// Package nonops hosts the 9 NonOps HTTP methods extracted from the
// clips/ package per PR-CLIPS-NONOPS-EXTRACT (P0, deadline 2026-08-01).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - Handler interface (9 methods + RegisterRoutes) lives ONLY here.
//   - applyBulkTagsDefaults helper lives ONLY in handler_bulk_tags.go.
//   - Each method lives in its capability-specific sister file
//     (handler_reprocess / handler_index / handler_download / handler_jobs).
//
// godlike/07 minimum-blast-radius: use case constructors
// (NewBulkTagsUseCase, NewReprocessUseCase, NewEnrichUseCase) stay in
// the parent clips.NewHandler per thinker verdict Q7 — the sub-handler
// consumes pre-built use case instances, not raw repositories. The
// sub-package dep footprint is bound to what the 9 methods
// INHERENTLY invoke (use cases + a RepoForSource callback for the
// ReindexClip source-resolution seam).
package nonops

import (
	"context"
	"fmt"
	"strings"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler is the canonical contract for the 9 NonOps methods that
// the orchestrator clips.Handler delegates to. Implemented by
// *NonOpsHandler. Pattern 0 (godlike/06): single interface per
// concrete struct, kept as a 9-method surface because all 9 share
// the same constructor and the same lifespan. Splitting into
// sub-interfaces would be over-engineering.
//
// Compile-time pin at the bottom of this file locks the concrete to
// the interface — future drift in any of the 9 method signatures
// surfaces as a build failure, not a runtime panic.
type Handler interface {
	// BulkTag routes (write+idem).
	BulkAddTags(c *gin.Context)
	BulkRemoveTags(c *gin.Context)

	// Reprocess route (write+idem).
	ReprocessClip(c *gin.Context)

	// Reindex routes (write+idem).
	ReindexClip(c *gin.Context)
	BatchReindex(c *gin.Context)

	// Enrich routes.
	EnrichMedia(c *gin.Context)
	EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string)

	// Job handlers (used by ClipsDescriptor.RegisterJobHandlers).
	RegisterJobHandlers() error
	HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *kerneljob.Job, tools *appjobs.JobTools) (map[string]any, error)

	// Route installation (called by the orchestrator Handler.RegisterRoutes).
	RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc)
}

// Deps is the constructor bag for NonOpsHandler. The 8 fields below
// are exactly what the 9 methods touch — no more, no less. Per
// thinker verdict Q7: use case instances are pre-built by the parent
// (clips.NewHandler) and passed in already-constructed. The
// sub-handler does NOT take repository / service / config
// dependencies for use case construction.
type Deps struct {
	// BulkTagsUC powers BulkAddTags + BulkRemoveTags.
	BulkTagsUC *appclips.BulkTagsUseCase
	// ReprocessUC powers ReprocessClip.
	ReprocessUC *appclips.ReprocessUseCase
	// EnrichUC powers EnrichMedia + EnrichAndIndexClip + ReindexClip (enrichNeeded branch).
	EnrichUC *appclips.EnrichUseCase
	// ClipIndexer powers ReindexClip (clipIndexer.IsEnabled() gate) + BatchReindex.
	ClipIndexer appclips.ClipIndexerPort
	// JobsSvc powers EnrichMedia + ReindexClip (enqueue) + BatchReindex (enqueue) +
	// RegisterJobHandlers (job handler registration).
	JobsSvc kerneljob.Service
	// BulkUploadWorker powers HandleBulkUploadYouTubeClipsJob.
	BulkUploadWorker *appclips.BulkUploadWorker
	// RepoForSource is the callback that resolves a clip source to its
	// canonical repository. Wired by the parent as `h.repoForSource`
	// (a Go method-value bound to the orchestrator *Handler so the
	// lookup chains into the Search sub-handler without coupling
	// nonops to it directly). Required by ReindexClip.
	RepoForSource func(string) appclips.ClipRepositoryPort
	// Log is the structured logger. nil-tolerated via zap.NewNop().
	Log *zap.Logger
}

// NonOpsHandler owns the 9 NonOps methods extracted from clips/Handler.
// Receiver-on-pattern-B: constructed in nonops.NewNonOpsHandler from a
// Deps shape extracted from the orchestrator Deps.
type NonOpsHandler struct {
	bulkTagsUC       *appclips.BulkTagsUseCase
	reprocessUC      *appclips.ReprocessUseCase
	enrichUC         *appclips.EnrichUseCase
	clipIndexer      appclips.ClipIndexerPort
	jobsSvc          kerneljob.Service
	bulkUploadWorker *appclips.BulkUploadWorker
	repoForSource    func(string) appclips.ClipRepositoryPort
	log              *zap.Logger
}

// NewNonOpsHandler constructs a NonOpsHandler from the supplied Deps.
// Nil fields are tolerated for test fixtures (each method does its own
// nil-check); production wiring supplies all 8 via the orchestrator
// Deps shape.
func NewNonOpsHandler(d Deps) *NonOpsHandler {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &NonOpsHandler{
		bulkTagsUC:       d.BulkTagsUC,
		reprocessUC:      d.ReprocessUC,
		enrichUC:         d.EnrichUC,
		clipIndexer:      d.ClipIndexer,
		jobsSvc:          d.JobsSvc,
		bulkUploadWorker: d.BulkUploadWorker,
		repoForSource:    d.RepoForSource,
		log:              d.Log,
	}
}

// ValidateNonOpsDeps is the canonical fail-closed gate for the nonops
// sub-handler's required deps. Extracted from NewNonOpsHandlerStrict so
// the strict constructor AND external callers (clips.NewHandlerStrict via
// the composition root) share a single source-of-truth.
//
// godlike/07 fail-closed contract: JobsSvc and BulkUploadWorker MUST
// be non-nil at composition time. The canonical 3-method registration
// chain (ClipsDescriptor.RegisterJobHandlers -> Handler.RegisterJobHandlers
// -> NonOpsHandler.RegisterJobHandlers -> jobs.Service.RegisterHandler)
// cannot complete if either is missing — the dispatcher's handler map
// would stay empty -> "no handler registered" at first enqueue.
// Validation at construction lets the operator crash loudly at boot
// rather than silently accumulating an unrecoverable chain gap.
//
// Returns a non-nil error listing ALL missing required deps (not just
// the first one), so operators can fix the wiring in one boot iteration.
func ValidateNonOpsDeps(d Deps) error {
	var missing []string
	if d.JobsSvc == nil {
		missing = append(missing, "JobsSvc")
	}
	if d.BulkUploadWorker == nil {
		missing = append(missing, "BulkUploadWorker")
	}
	if len(missing) > 0 {
		return fmt.Errorf("nonops.ValidateNonOpsDeps: required dependencies missing at composition time (godlike/07 fail-closed contract): %s — pass them at the composition root via nonops.NewNonOpsHandlerStrict or clips.NewHandlerStrict", strings.Join(missing, ", "))
	}
	return nil
}

// NewNonOpsHandlerStrict constructs a NonOpsHandler with the required
// composition-time deps validated upfront via ValidateNonOpsDeps.
//
// godlike/07 fail-closed: JobsSvc + BulkUploadWorker are REQUIRED.
// Nil either -> an error is returned at construction time so the
// operator crashes loudly at boot rather than "no handler registered"
// at first enqueue. The legacy nil-tolerant NewNonOpsHandler remains
// for test fixtures that opt out of the fail-closed contract (legacy
// back-compat path; do NOT call from production composition roots).
//
// Canonical production wiring: the composition root
// (clips.Build -> NewHandlerStrict -> NewNonOpsHandlerStrict) calls
// this constructor so the canonical 3-method registration chain is
// fail-closed at boot per godlike/07.
func NewNonOpsHandlerStrict(d Deps) (*NonOpsHandler, error) {
	if err := ValidateNonOpsDeps(d); err != nil {
		return nil, err
	}
	return NewNonOpsHandler(d), nil
}

// RegisterRoutes installs the 6 NonOps HTTP routes on the supplied
// gin router group. All routes are writes (idem-protected per PR8).
//
// Route table:
//
//	POST /:source/bulk/tags/add         -> BulkAddTags      (write+idem)
//	POST /:source/bulk/tags/remove      -> BulkRemoveTags   (write+idem)
//	POST /:source/clips/:id/reprocess   -> ReprocessClip    (write+idem)
//	POST /:source/clips/:id/reindex     -> ReindexClip      (write+idem)
//	POST /enrich                        -> EnrichMedia      (write+idem)
//	POST /enrich/batch                  -> BatchReindex     (write+idem)
func (h *NonOpsHandler) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	r.POST("/:source/bulk/tags/add", idem, h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", idem, h.BulkRemoveTags)
	r.POST("/:source/clips/:id/reprocess", idem, h.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", idem, h.ReindexClip)
	r.POST("/enrich", idem, h.EnrichMedia)
	r.POST("/enrich/batch", idem, h.BatchReindex)
}

// Compile-time pin: *NonOpsHandler must implement Handler. Future
// drift in any of the 9 method signatures surfaces as a build
// failure, not a runtime panic (Pattern 0 + godlike/06 SSOT).
var _ Handler = (*NonOpsHandler)(nil)
