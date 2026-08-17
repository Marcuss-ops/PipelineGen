// Package nonops hosts the 5 NonOps HTTP methods extracted from the
// clips/ package per PR-CLIPS-NONOPS-EXTRACT (P0, deadline 2026-08-01).
//
// The former bulk-tag routes (BulkAddTags/BulkRemoveTags) were migrated
// to the canonical /api/assets/operator/bulk surface, the former
// single-clip reindex route was migrated to
// /api/assets/operator/assets/:id/reindex, and the former batch-reindex
// route (POST /api/media/clips/enrich/batch) was retired in favor of the
// canonical media.reindex job (enqueueable via POST /api/jobs); all were
// removed here.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - Handler interface (5 methods) lives ONLY here.
//   - Each method lives in its capability-specific sister file
//     (handler_reprocess / handler_download / handler_jobs).
//
// godlike/07 minimum-blast-radius: use case constructors
// (NewReprocessUseCase, NewEnrichUseCase) stay in the parent
// clips.NewHandler per thinker verdict Q7 — the sub-handler consumes
// pre-built use case instances, not raw repositories. The sub-package
// dep footprint is bound to what the 5 methods INHERENTLY invoke.
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

// Handler is the canonical contract for the 5 NonOps methods that
// the orchestrator clips.Handler delegates to. Implemented by
// *NonOpsHandler. Pattern 0 (godlike/06): single interface per
// concrete struct, kept as a 5-method surface because all 5 share
// the same constructor and the same lifespan. Splitting into
// sub-interfaces would be over-engineering.
//
// Compile-time pin at the bottom of this file locks the concrete to
// the interface — future drift in any of the 5 method signatures
// surfaces as a build failure, not a runtime panic.
type Handler interface {
	// Reprocess route (write+idem).
	ReprocessClip(c *gin.Context)

	// Enrich routes.
	EnrichMedia(c *gin.Context)
	EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string)

	// Job handlers (used by ClipsDescriptor.RegisterJobHandlers).
	RegisterJobHandlers() error
	HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *kerneljob.Job, tools *appjobs.JobTools) (map[string]any, error)
}

// Deps is the constructor bag for NonOpsHandler. The 5 fields below
// are exactly what the 5 methods touch — no more, no less. Per
// thinker verdict Q7: use case instances are pre-built by the parent
// (clips.NewHandler) and passed in already-constructed. The
// sub-handler does NOT take repository / service / config
// dependencies for use case construction.
type Deps struct {
	// ReprocessUC powers ReprocessClip.
	ReprocessUC *appclips.ReprocessUseCase
	// EnrichUC powers EnrichMedia + EnrichAndIndexClip.
	EnrichUC *appclips.EnrichUseCase
	// JobsSvc powers EnrichMedia (enqueue) + RegisterJobHandlers
	// (job handler registration).
	JobsSvc kerneljob.Service
	// BulkUploadWorker powers HandleBulkUploadYouTubeClipsJob.
	BulkUploadWorker *appclips.BulkUploadWorker
	// Log is the structured logger. nil-tolerated via zap.NewNop().
	Log *zap.Logger
}

// NonOpsHandler owns the 5 NonOps methods extracted from clips/Handler.
// Receiver-on-pattern-B: constructed in nonops.NewNonOpsHandler from a
// Deps shape extracted from the orchestrator Deps.
type NonOpsHandler struct {
	reprocessUC      *appclips.ReprocessUseCase
	enrichUC         *appclips.EnrichUseCase
	jobsSvc          kerneljob.Service
	bulkUploadWorker *appclips.BulkUploadWorker
	log              *zap.Logger
}

// NewNonOpsHandler constructs a NonOpsHandler from the supplied Deps.
// Nil fields are tolerated for test fixtures (each method does its own
// nil-check); production wiring supplies all 5 via the orchestrator
// Deps shape.
func NewNonOpsHandler(d Deps) *NonOpsHandler {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &NonOpsHandler{
		reprocessUC:      d.ReprocessUC,
		enrichUC:         d.EnrichUC,
		jobsSvc:          d.JobsSvc,
		bulkUploadWorker: d.BulkUploadWorker,
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

// Compile-time pin: *NonOpsHandler must implement Handler. Future
// drift in any of the 5 method signatures surfaces as a build
// failure, not a runtime panic (Pattern 0 + godlike/06 SSOT).
var _ Handler = (*NonOpsHandler)(nil)
