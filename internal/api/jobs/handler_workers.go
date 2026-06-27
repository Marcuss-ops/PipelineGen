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
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	assets "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
)

// Broker is the narrow port for worker session RPC. Satisfied by
// *appjobs.Service in production; tests can stub.
type Broker interface {
	RegisterWorker(ctx context.Context, cmd appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error)
	Heartbeat(ctx context.Context, cmd appjobs.HeartbeatCommand) error
	Claim(ctx context.Context, cmd appjobs.ClaimCommand) (*appjobs.Lease, error)
	Renew(ctx context.Context, cmd appjobs.RenewCommand) (*appjobs.Lease, error)
	Progress(ctx context.Context, cmd appjobs.ProgressCommand) error
	Complete(ctx context.Context, cmd appjobs.CompleteCommand) error
	Fail(ctx context.Context, cmd appjobs.FailCommand) error
	IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error)
}

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
		api.BadRequest(c, err.Error())
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
		api.InternalError(c, err)
		return
	}
	api.OK(c, session)
}

func (h *WorkersBrokerHandler) Heartbeat(c *gin.Context) {
	var req struct {
		WorkerID        string `json:"worker_id"`
		WorkerSessionID string `json:"worker_session_id"`
		SessionTTL      int64  `json:"session_ttl_seconds,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	if err := h.broker.Heartbeat(c.Request.Context(), appjobs.HeartbeatCommand{
		WorkerID:        req.WorkerID,
		WorkerSessionID: req.WorkerSessionID,
		SessionTTL:      time.Duration(req.SessionTTL) * time.Second,
	}); err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) Claim(c *gin.Context) {
	var req appjobs.ClaimCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	lease, err := h.broker.Claim(c.Request.Context(), req)
	if err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, lease)
}

func (h *WorkersBrokerHandler) Renew(c *gin.Context) {
	var req appjobs.RenewCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	lease, err := h.broker.Renew(c.Request.Context(), req)
	if err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, lease)
}

func (h *WorkersBrokerHandler) Progress(c *gin.Context) {
	var req appjobs.ProgressCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Progress(c.Request.Context(), req); err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) Complete(c *gin.Context) {
	var req appjobs.CompleteCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Complete(c.Request.Context(), req); err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) Fail(c *gin.Context) {
	var req appjobs.FailCommand
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Fail(c.Request.Context(), req); err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) IsCancelled(c *gin.Context) {
	cancelled, err := h.broker.IsCancelled(c.Request.Context(), c.Param("id"), c.Query("lease_id"))
	if err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"cancelled": cancelled})
}

func (h *WorkersBrokerHandler) DownloadAsset(c *gin.Context) {
	if h.assets == nil {
		api.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
		return
	}
	rc, filename, err := h.assets.Download(c.Request.Context(), c.Param("asset_id"))
	if err != nil {
		api.InternalError(c, err)
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
		api.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
		return
	}
	var req struct {
		AssetID string `json:"asset_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	out, err := h.assets.InitiateUpload(c.Request.Context(), req.AssetID)
	if err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, out)
}

func (h *WorkersBrokerHandler) UploadAssetContent(c *gin.Context) {
	if h.assets == nil {
		api.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
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
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"ok": true})
}

func (h *WorkersBrokerHandler) FinalizeUpload(c *gin.Context) {
	if h.assets == nil {
		api.Error(c, http.StatusNotImplemented, "asset transfer service not configured")
		return
	}
	var req struct {
		AssetID string `json:"asset_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
		return
	}
	if err := h.assets.FinalizeUpload(c.Request.Context(), req.AssetID); err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"ok": true})
}

var _ = http.StatusOK
