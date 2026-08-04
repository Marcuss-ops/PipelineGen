// Package clips — Ingest sub-handler (Step 5 Split 2, June 2026).
//
// OVERRIDE ADR 0009 (clips.Handler capability-split) — user override
// recorded in commit messages; this commit extracts the 3 ingest
// routes (CreateClip + UpdateClip + UploadVideoClip) into a dedicated
// *IngestHandler receiver. IngestDeps carries only the 6 deps these
// methods consume (cluster × deps matrix §4):
//
//   - Dispatcher     (CreateClip + UpdateClip + UploadVideoClip — atomic UPSERT + outbox)
//   - AssetTreeSvc   (CreateClip + UpdateClip + UploadVideoClip — tree upsert)
//   - JobsSvc        (CreateClip + UploadVideoClip — media.enrich enqueue)
//   - ClipsRepo      (UpdateClip — repoForSource gate)
//   - EnrichUC       (CreateClip + UploadVideoClip — null-check pre media.enrich gate)
//   - UploadUC       (UploadVideoClip — P1.5 CUTOVER: uploadUC.Execute handles artifact,
//     Drive, ffprobe, metadata, and indexing internally)
//   - Log            (all methods)
//
// 6 fields removed July 2026 (dead code — ArtifactSvc, DriveAdmin,
// ProcessRunner, Cfg, ClipIndexer, MetaWriter were assigned but
// never read after UploadVideoClip migrated to uploadUC.Execute).
//
// Pattern B (per-cluster RegisterRoutes with idem fn as parameter):
// the canonical ingest sub-descriptor calls
// ih.RegisterRoutes(r, idem). All ingest routes have idem
// installed before the handler per AGENTS.md Pattern 8 (writes are
// atomic via Dispatcher + jobs media.enrich enqueue).
package clips

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clips/aistock"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// errClipDispatcherUnavailable is the fail-closed sentinel surfaced by
// every clips API writer in PR 6 (codex/qdrant-api-writers-fail-closed):
// when the canonical AssetMutationDispatcher is not wired at composition
// time (test fixtures, partial deploys), the four write endpoints
// (CreateClip, UploadVideoClip — ClipAction::ReuploadClip and
// sound_effect Generate are documented in their own clusters) return
// HTTP 503 with this message instead of silently falling back to a raw
// repo.Upsert write that would corrupt Qdrant semantics.
//
// Operational response: the operator should investigate why the
// composition root did not inject the dispatcher. The Wave 22 task-2
// (PR-6) gate `scripts/ci-bypass-audit.sh` ratchets this counter to
// zero — any new caller that bypasses AssetMutationDispatcher fails CI.
//
// Moved from clip_create.go (deleted in Split 2, June 2026).
var errClipDispatcherUnavailable = errors.New("clips API write unavailable: AssetMutationDispatcher not wired (QDRANT-asset-mutation isolation; production composition root must wire *outbox.Dispatcher via clipsDispatcherAdapter)")

// IngestDeps is the constructor bag for IngestHandler. The 7 fields
// below are exactly the deps the 3 moved methods touch — no more, no
// less. 6 fields removed July 2026 (dead code — UploadVideoClip was
// migrated to uploadUC.Execute which handles artifact staging, Drive
// folder resolve, ffprobe, metadata, and indexing internally).
type IngestDeps struct {
	Dispatcher   appclips.ClipIndexDispatcherPort
	AssetTreeSvc *assettree.Service
	JobsSvc      jobs.Service
	ClipsRepo    appclips.ClipRepositoryPort
	EnrichUC     *appclips.EnrichUseCase
	UploadUC     *appupload.UseCase
	AIStockUC    *aistock.UseCase
	Log          *zap.Logger
}

// IngestHandler owns the 3 ingest routes. Receiver-on-pattern-B:
// constructed in NewHandler from an IngestDeps shape extracted from
// the orchestrator Deps.
// 6 fields removed July 2026 (dead code — UploadVideoClip was migrated
// to uploadUC.Execute).
type IngestHandler struct {
	dispatcher   appclips.ClipIndexDispatcherPort
	assetTreeSvc *assettree.Service
	jobsSvc      jobs.Service
	clipsRepo    appclips.ClipRepositoryPort
	enrichUC     *appclips.EnrichUseCase
	uploadUC     *appupload.UseCase
	aiStockUC    *aistock.UseCase
	log          *zap.Logger
}

// NewIngestHandler constructs an IngestHandler with the supplied
// IngestDeps. Nil fields are tolerated for test fixtures (each method
// does its own nil-check); production wiring supplies all 12 via the
// orchestrator Deps shape.
func NewIngestHandler(d IngestDeps) *IngestHandler {
	return &IngestHandler{
		dispatcher:   d.Dispatcher,
		assetTreeSvc: d.AssetTreeSvc,
		jobsSvc:      d.JobsSvc,
		clipsRepo:    d.ClipsRepo,
		enrichUC:     d.EnrichUC,
		uploadUC:     d.UploadUC,
		aiStockUC:    d.AIStockUC,
		log:          d.Log,
	}
}

// repoForSource resolves a clip source to its canonical repository
// via the shared ClipsRepository. All clip-type sources share the same
// concrete repo in production. Returns nil for voiceover/images.
func (ih *IngestHandler) repoForSource(source string) appclips.ClipRepositoryPort {
	if ih.clipsRepo == nil {
		return nil
	}
	if !artifacts.IsClipsSource(source) {
		return nil
	}
	return ih.clipsRepo
} // RegisterRoutes installs the ingest routes on the supplied gin
// router group. All routes are writes (idem-protected per PR8).
//
// Route table:
//
//	POST  /:source/clips           -> CreateClip      (write+idem)
//	PATCH /:source/clips/:id       -> UpdateClip      (write+idem)
//	POST  /upload-video            -> UploadVideoClip (write+idem)
//	POST  /ingest/ai-stock         -> CreateAIStockClip (write+idem)
func (ih *IngestHandler) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {

	r.POST("/:source/clips", idem, ih.CreateClip)
	r.PATCH("/:source/clips/:id", idem, ih.UpdateClip)
	r.POST("/upload-video", idem, ih.UploadVideoClip)
	r.POST("/ingest/ai-stock", idem, ih.CreateAIStockClip)
}

// ──────────────────────────────────────────────────────────────────────
