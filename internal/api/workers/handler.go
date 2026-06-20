package workers

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	assets "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
)

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

type AssetTransferService interface {
	Download(ctx context.Context, assetID string) (io.ReadCloser, string, error)
	InitiateUpload(ctx context.Context, assetID string) (*assets.UploadResponse, error)
	Upload(ctx context.Context, assetID, filename string, content io.Reader) error
	FinalizeUpload(ctx context.Context, assetID string) error
}

type InternalworkerHandler struct {
	broker Broker
	assets AssetTransferService
	log    *zap.Logger
}

func NewInternalworkerHandler(broker Broker, assets AssetTransferService, log *zap.Logger) *InternalworkerHandler {
	return &InternalworkerHandler{broker: broker, assets: assets, log: log}
}

func (h *InternalworkerHandler) RegisterRoutes(r *gin.RouterGroup) {
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

func (h *InternalworkerHandler) RegisterWorker(c *gin.Context) {
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

func (h *InternalworkerHandler) Heartbeat(c *gin.Context) {
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

func (h *InternalworkerHandler) Claim(c *gin.Context) {
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

func (h *InternalworkerHandler) Renew(c *gin.Context) {
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

func (h *InternalworkerHandler) Progress(c *gin.Context) {
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

func (h *InternalworkerHandler) Complete(c *gin.Context) {
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

func (h *InternalworkerHandler) Fail(c *gin.Context) {
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

func (h *InternalworkerHandler) IsCancelled(c *gin.Context) {
	cancelled, err := h.broker.IsCancelled(c.Request.Context(), c.Param("id"), c.Query("lease_id"))
	if err != nil {
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"cancelled": cancelled})
}

func (h *InternalworkerHandler) DownloadAsset(c *gin.Context) {
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

func (h *InternalworkerHandler) InitiateUpload(c *gin.Context) {
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

func (h *InternalworkerHandler) UploadAssetContent(c *gin.Context) {
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

func (h *InternalworkerHandler) FinalizeUpload(c *gin.Context) {
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
