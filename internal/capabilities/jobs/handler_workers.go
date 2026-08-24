// Package jobs (api/jobs) — handler_workers.go holds the
// internal-worker-broker HTTP transport used by the remote cmd/worker
// binary to claim and complete jobs. Wave 14 close (June 2026): this
// receiver was absorbed from the standalone internal/api/workers/
// package as a sibling to JobsHandler.
//
// Critical contract — MOUNTED ON A NON-API PREFIX:
//   - JobsHandler          mounts on `/jobs` → /api/jobs/{, stats, :id ...}
//   - WorkersBrokerHandler mounts on remoteshared.InternalPathPrefix
//     (typically /internal/v1/) → NOT under /api/.
//
// See internal/api/server.go::Router.SetWorkerHandler and
// remoteshared.InternalPathPrefix for the exact routing context.
//
// Two receivers coexist in the same package: the public-facing JobsHandler
// (admin lifecycle) and the internal-workers broker (worker binary RPC).
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue/assets"
	completiontransport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/primitives"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Broker is the narrow port for worker session RPC. Satisfied by
// *appjobs.Service in production; tests can stub.
//
// Issue 15e (June 2026): type alias to appjobs.Broker — single source
// of truth. The 8-method interface lives canonically in
// internal/capabilities/jobs/queue/broker.go.
type Broker = appjobs.Broker

// AssetTransferService is the narrow port for the worker binary's
// asset push/pull. Satisfied by *jobs.assets.AssetTransferService.
type AssetTransferService interface {
	Download(ctx context.Context, assetID string) (io.ReadCloser, string, error)
	InitiateUpload(ctx context.Context, assetID string) (*assets.UploadResponse, error)
	Upload(ctx context.Context, assetID, filename string, content io.Reader) error
	FinalizeUpload(ctx context.Context, assetID string) error
}

// WorkersBrokerHandler is the worker-binary RPC handler. Mounted on
// remoteshared.InternalPathPrefix by the server, NOT on /api/.
type WorkersBrokerHandler struct {
	broker Broker
	assets AssetTransferService
	log    *zap.Logger
}

// NewWorkersBrokerHandler creates a new worker broker HTTP handler.
func NewWorkersBrokerHandler(broker Broker, assets AssetTransferService, log *zap.Logger) *WorkersBrokerHandler {
	return &WorkersBrokerHandler{broker: broker, assets: assets, log: log}
}

// RegisterRoutes registers the worker-broker routes on the supplied
// RouterGroup. Caller is expected to mount this on the internal
// prefix (NOT /api/).
func (h *WorkersBrokerHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/workers/register", h.RegisterWorker)
	r.POST("/workers/heartbeat", h.Heartbeat)
	r.POST("/jobs/claim", h.Claim)
	r.POST("/jobs/:id/renew", h.Renew)
	r.POST("/jobs/:id/progress", h.Progress)
	r.POST("/jobs/:id/complete", h.Complete)
	r.POST("/jobs/:id/complete-with-artifacts", h.CompleteWithArtifacts)
	r.POST("/jobs/:id/fail", h.Fail)
	r.GET("/jobs/:id/cancelled", h.IsCancelled)
	r.GET("/worker-assets/:asset_id/download", h.DownloadAsset)
	r.POST("/worker-assets/uploads/initiate", h.InitiateUpload)
	r.POST("/worker-assets/uploads/:asset_id/content", h.UploadAssetContent)
	r.POST("/worker-assets/uploads/finalize", h.FinalizeUpload)
}

type registerWorkerRequest struct {
	WorkerID     string                     `json:"worker_id"`
	Name         string                     `json:"name,omitempty"`
	Version      string                     `json:"version,omitempty"`
	Hostname     string                     `json:"hostname,omitempty"`
	Capabilities appjobs.WorkerCapabilities `json:"capabilities"`
}

func (h *WorkersBrokerHandler) RegisterWorker(c *gin.Context) {
	var req registerWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	session, err := h.broker.RegisterWorker(c.Request.Context(), appjobs.RegisterWorkerCommand{
		WorkerID:     req.WorkerID,
		Name:         req.Name,
		Version:      req.Version,
		Hostname:     req.Hostname,
		Capabilities: req.Capabilities,
		SessionTTL:   90 * time.Second,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, session)
}

func (h *WorkersBrokerHandler) Heartbeat(c *gin.Context) {
	var req struct {
		WorkerID        string `json:"worker_id"`
		WorkerSessionID string `json:"worker_session_id"`
		SessionTTL      int64  `json:"session_ttl_seconds,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	if err := h.broker.Heartbeat(c.Request.Context(), appjobs.HeartbeatCommand{
		WorkerID:        req.WorkerID,
		WorkerSessionID: req.WorkerSessionID,
		SessionTTL:      time.Duration(req.SessionTTL) * time.Second,
	}); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) Claim(c *gin.Context) {
	var req appjobs.ClaimCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	lease, err := h.broker.Claim(c.Request.Context(), req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, lease)
}

func (h *WorkersBrokerHandler) Renew(c *gin.Context) {
	var req appjobs.RenewCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	lease, err := h.broker.Renew(c.Request.Context(), req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, lease)
}

func (h *WorkersBrokerHandler) Progress(c *gin.Context) {
	var req appjobs.ProgressCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Progress(c.Request.Context(), req); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) Complete(c *gin.Context) {
	var req appjobs.CompleteCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Complete(c.Request.Context(), req); err != nil {
		// P1 #15 (July 2026): the complete-jobs path emits the
		// canonical 7-kind wire envelope via MapErrorToHTTP.
		// MapErrorToHTTP returns true (and aborts the gin chain)
		// when err matches one of the 7 canonical kinds; false
		// falls through to apiutil.InternalError so the 500 path
		// stays intact for genuine unknowns.
		if completiontransport.MapErrorToHTTP(c, err) {
			return
		}
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

// CompleteArtifactsRequest is the typed HTTP-in DTO for
// POST /internal/v1/jobs/:id/complete-with-artifacts. It mirrors
// appjobs.CompleteWithArtifactsCommand but exposes StagedArtifacts
// (the pre-publish artifact reference catalog submitted by the
// caller) under the canonical wire-field name.
//
// P0-COMPL-5-WIRE-NAMING (July 2026): the wire field was renamed
// ArtifactManifest → StagedArtifacts. The HTTP wire now ships the
// pre-publish StagedArtifactReference envelope (3-field minimum:
// ArtifactID + Destination + optional SHA256 hint) instead of the
// pre-published bytes convention. The Sender is responsible for
// converting StagedArtifactReference → PublishedArtifact (canonical
// 7-field envelope with Drive FileID/link/checksum) post-publish,
// per the PublishAndCompleteUseCase surface at
// internal/capabilities/jobs/policy/publish_and_complete_use_case.go
// (the EXPAND-phase canonical; handler-wiring to the use case is
// the BACKFILL phase, forward-pointer P0-COMPL-5-HANDLER-WIRE).
//
// CRITICAL CONTRACT (godlike/07 no-fake-availability): the body
// MUST NOT contain local Creator paths. The asset transport
// already uploads the artifacts to the Sender over the
// /worker-assets/* surface — this endpoint only references them
// by ID. Any non-canonical field (LocalPath, SourcePath) is a
// regression of the cutover and surfaces immediately in code
// review (CI Check 53 on the production tree enforces the
// structural shape).
type CompleteArtifactsRequest struct {
	WorkerID         string          `json:"worker_id"`
	WorkerSessionID  string          `json:"worker_session_id"`
	LeaseID          string          `json:"lease_id"`
	ExpectedRevision int             `json:"expected_revision"`
	ResultData       json.RawMessage `json:"result_data"`
	// StagedArtifacts is the canonical pre-publish reference slice
	// (P0-COMPL-5-WIRE-NAMING, July 2026). The caller ships the
	// minimum identifier envelope (ArtifactID + Destination + SHA256
	// hint); the Sender converts each reference to a canonical
	// PublishedArtifact envelope (Drive FileID/link/checksum +
	// Location populated by the prepare pipeline).
	StagedArtifacts []*remote.StagedArtifactReference `json:"staged_artifacts"`
	// OutboxEvents is optional; mirror of CompleteWithArtifactsCommand.
	OutboxEvents json.RawMessage `json:"outbox_events,omitempty"`
}

// CompleteArtifactsResponse is the typed HTTP-out DTO. The
// canonical post-completion SUCCEEDED status is surfaced
// uniformly; AssetIDs is forward-declared so the wire contract
// survives the future PR where Broker.CompleteWithArtifacts
// returns the typed FinalizationResult acknowledgment (the
// canonical-server-side ack surface). For now the slice is
// always empty — godlike/07 no-fake-availability: never invent
// IDs that did not come from the typed return.
// PR-DOMAIN-PRIMITIVES-NOMINAL (July 2026): JobID is the canonical
// nominal type (zero-cost on the wire — Go's `type X string` emits
// the underlying string in JSON unchanged). The handler wraps the
// URL :id with primitives.NewJobID at the boundary so the response
// DTO carries a typed value.
type CompleteArtifactsResponse struct {
	JobID    primitives.JobID `json:"job_id"`
	Status   string           `json:"status"`
	AssetIDs []string         `json:"asset_ids"`
}

// CompleteWithArtifacts forwards a successful artifact-producing
// job outcome from a remote worker through the JobFinalizer
// spine. The body carries the worker's published artifact
// manifest + result data; the broker deserialises and writes
// asset records, versions, locations, outbox events in the SAME
// transaction as the SUCCEEDED transition (atomic per
// godlike/07).
//
// Wire contract: mounted on remoteshared.InternalPathPrefix
// (typically /internal/v1/) and gated by WorkerAuth — the same
// boundary as the legacy /complete surface. The internal-only
// path deliberately mirrors the legacy route's wire role
// (server-to-server worker RPC, never exposed under /api).
//
// NOTE: the response AssetIDs slice is currently always empty
// (forward-declared). When the underlying Broker interface is
// extended to return the typed FinalizationResult in a future
// PR, the handler will thread the asset IDs from the typed
// return into the response struct without changing the wire
// shape.
func (h *WorkersBrokerHandler) CompleteWithArtifacts(c *gin.Context) {
	var req CompleteArtifactsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	// P0-COMPL-5-WIRE-NAMING: marshal the typed StagedArtifacts slice
	// to canonical JSON bytes for the cmd field (which is still
	// json.RawMessage for the worker-side pipeline byte-stability). The
	// canonical envelope conversion (StagedArtifactReference → PublishedArtifact
	// with Drive FileID/link/checksum post-publish) is the future BACKFILL
	// phase via PublishAndCompleteUseCase.Execute (forward-pointer
	// P0-COMPL-5-HANDLER-WIRE).
	stagedBytes, marshalErr := json.Marshal(req.StagedArtifacts)
	if marshalErr != nil {
		apiutil.BadRequest(c, fmt.Sprintf("staged_artifacts marshal: %v", marshalErr))
		return
	}
	cmd := appjobs.CompleteWithArtifactsCommand{
		WorkerID:         req.WorkerID,
		WorkerSessionID:  req.WorkerSessionID,
		JobID:            c.Param("id"),
		LeaseID:          req.LeaseID,
		ExpectedRevision: req.ExpectedRevision,
		ResultData:       req.ResultData,
		StagedArtifacts:  stagedBytes,
		OutboxEvents:     req.OutboxEvents,
	}
	assetIDs, err := h.broker.CompleteWithArtifacts(c.Request.Context(), cmd)
	if err != nil {
		// P1 #15 (July 2026): the complete-with-artifacts path
		// emits the canonical 7-kind wire envelope via
		// MapErrorToHTTP. Same semantics as Complete:
		// true = aborts gin chain + sets typed envelope; false =
		// fall-through to apiutil.InternalError for unmapped errors.
		if completiontransport.MapErrorToHTTP(c, err) {
			return
		}
		apiutil.InternalError(c, err)
		return
	}
	// AZIONE 5 (July 2026): coerce nil AssetIDs to empty slice so the
	// wire field always serializes as "[]" rather than "null". The
	// SQLite broker returns non-nil (make slice with cap), but stubs
	// and error paths may return nil. The handler is the canonical
	// defense boundary.
	if assetIDs == nil {
		assetIDs = []string{}
	}
	apiutil.OK(c, CompleteArtifactsResponse{
		JobID:    primitives.NewJobID(cmd.JobID),
		Status:   string(jobs.StatusSucceeded),
		AssetIDs: assetIDs,
	})
}

func (h *WorkersBrokerHandler) Fail(c *gin.Context) {
	var req appjobs.FailCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Fail(c.Request.Context(), req); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) IsCancelled(c *gin.Context) {
	cancelled, err := h.broker.IsCancelled(c.Request.Context(), c.Param("id"), c.Query("lease_id"))
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"cancelled": cancelled})
}

func (h *WorkersBrokerHandler) DownloadAsset(c *gin.Context) {
	if h.assets == nil {
		apiutil.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
		return
	}
	rc, filename, err := h.assets.Download(c.Request.Context(), c.Param("asset_id"))
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	defer rc.Close()
	if filename != "" {
		c.Header("X-Filename", filename)
		c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	c.DataFromReader(http.StatusOK, -1, "application/octet-stream", rc, nil)
}

func (h *WorkersBrokerHandler) InitiateUpload(c *gin.Context) {
	if h.assets == nil {
		apiutil.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
		return
	}
	var req struct {
		AssetID string `json:"asset_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	out, err := h.assets.InitiateUpload(c.Request.Context(), req.AssetID)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, out)
}

func (h *WorkersBrokerHandler) UploadAssetContent(c *gin.Context) {
	if h.assets == nil {
		apiutil.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
		return
	}
	filename := c.GetHeader("X-Filename")
	if filename == "" {
		filename = c.Query("filename")
	}
	if filename == "" {
		filename = c.Param("asset_id")
	}
	if err := h.assets.Upload(c.Request.Context(), c.Param("asset_id"), filename, c.Request.Body); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) FinalizeUpload(c *gin.Context) {
	if h.assets == nil {
		apiutil.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
		return
	}
	var req struct {
		AssetID string `json:"asset_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	if err := h.assets.FinalizeUpload(c.Request.Context(), req.AssetID); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}
